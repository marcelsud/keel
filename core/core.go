// Package core runs the embedded NATS server and Keel storage service.
package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/marcelsud/keel/internal/protocol"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
)

type Options struct {
	Host                  string
	Port                  int // Zero uses 4222; -1 chooses a free port.
	DataDir               string
	Token                 string
	Credentials           *Credentials
	SQL                   bool
	SQLTransactionTimeout time.Duration
	Cluster               *ClusterOptions
}

type Core struct {
	server   *server.Server
	dataLock *os.File
	nc       *nats.Conn
	js       nats.JetStreamContext
	kv       nats.KeyValue
	service  micro.Service
	close    sync.Once
	closed   chan struct{}
	gate     sync.Mutex
	closing  bool
	active   sync.WaitGroup
	sql      *sqlStore
}

func Start(options Options) (_ *Core, err error) {
	if options.DataDir == "" {
		return nil, errors.New("data directory is required")
	}
	if options.Host == "" {
		options.Host = "127.0.0.1"
	}
	if options.Port == 0 {
		options.Port = 4222
	}
	lock, err := lockDataDirectory(options.DataDir)
	if err != nil {
		return nil, err
	}
	serverOptions := &server.Options{
		Host: options.Host, Port: options.Port, StoreDir: options.DataDir,
		JetStream: true, NoSigs: true, NoLog: true, Authorization: options.Token,
	}
	if err := configureAuth(serverOptions, options.Credentials); err != nil {
		_ = lock.Close()
		return nil, err
	}
	if err := configureCluster(serverOptions, options); err != nil {
		_ = lock.Close()
		return nil, err
	}
	s, err := server.NewServer(serverOptions)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	c := &Core{server: s, dataLock: lock, closed: make(chan struct{})}
	defer func() {
		if err != nil {
			c.Close()
		}
	}()
	startup := &startupLogger{failed: make(chan error, 1)}
	s.SetLogger(startup, false, false)
	go s.Start()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-startup.failed:
			return nil, err
		default:
		}
		if s.ReadyForConnections(50 * time.Millisecond) {
			break
		}
		if time.Now().After(deadline) {
			return nil, errors.New("embedded NATS did not become ready")
		}
	}
	c.nc, err = nats.Connect(s.ClientURL(), nats.InProcessServer(s), coreAuth(options), nats.ClosedHandler(func(*nats.Conn) { close(c.closed) }))
	if err != nil {
		return nil, err
	}
	c.js, err = c.nc.JetStream(nats.MaxWait(5 * time.Second))
	if err != nil {
		return nil, err
	}
	if err = c.initializeStorage(options); err != nil {
		return nil, err
	}
	c.service, err = micro.AddService(c.nc, micro.Config{Name: "keel-core", Version: "0.1.0"})
	if err != nil {
		return nil, err
	}
	group := c.service.AddGroup(protocol.Prefix + ".core")
	for _, operation := range protocol.Operations {
		if err = group.AddEndpoint(operation, micro.HandlerFunc(func(r micro.Request) { c.handle(operation, r) })); err != nil {
			return nil, err
		}
	}
	c.sql, err = startSQL(c, options)
	if err != nil {
		return nil, err
	}
	if err = c.nc.Flush(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Core) URL() string { return c.server.ClientURL() }

func (c *Core) Close() {
	c.close.Do(func() {
		defer c.dataLock.Close()
		c.gate.Lock()
		c.closing = true
		c.gate.Unlock()
		if c.service != nil {
			_ = c.service.Stop()
		}
		c.active.Wait()
		if c.sql != nil {
			c.sql.close()
		}
		if c.nc != nil {
			if c.nc.Drain() != nil {
				c.nc.Close()
			}
			<-c.closed
		}
		c.server.Shutdown()
		c.server.WaitForShutdown()
	})
}

var errDataDirectoryInUse = errors.New("local environment is already running; stop the existing keel or keel dev process before starting another")

func lockDataDirectory(directory string) (*os.File, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(directory, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("data directory %s: %w", directory, err)
	}
	return file, nil
}

type startupLogger struct{ failed chan error }

func (*startupLogger) Noticef(string, ...any) {}
func (*startupLogger) Warnf(string, ...any)   {}
func (*startupLogger) Errorf(string, ...any)  {}
func (*startupLogger) Debugf(string, ...any)  {}
func (*startupLogger) Tracef(string, ...any)  {}
func (logger *startupLogger) Fatalf(format string, args ...any) {
	select {
	case logger.failed <- fmt.Errorf("embedded NATS: "+format, args...):
	default:
	}
}

func (c *Core) handle(operation string, r micro.Request) {
	c.gate.Lock()
	if c.closing {
		c.gate.Unlock()
		_ = r.Error("503", "core is stopping", nil)
		return
	}
	c.active.Add(1)
	c.gate.Unlock()
	defer c.active.Done()
	var input protocol.Request
	err := json.Unmarshal(r.Data(), &input)
	var result any
	if err != nil {
		err = protocol.BadRequest("request must be a JSON object")
	} else {
		result, err = c.execute(operation, input)
	}
	respond(r, result, err)
}

func respond(r micro.Request, result any, err error) {
	if err == nil {
		var data []byte
		data, err = json.Marshal(result)
		if err == nil {
			_ = r.Respond(data)
			return
		}
	}
	var public *protocol.Error
	if errors.As(err, &public) {
		_ = r.Error(strconv.Itoa(public.Code), public.Message, nil)
		return
	}
	slog.Error("core request failed", "error", err)
	_ = r.Error("500", "storage operation failed", nil)
}

func required(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return protocol.BadRequest(field + " must not be empty")
	}
	return nil
}

func object(data json.RawMessage) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	if json.Unmarshal(data, &result) != nil || result == nil {
		return nil, protocol.BadRequest("document must be a JSON object")
	}
	if _, ok := result["id"]; ok {
		return nil, protocol.BadRequest("document cannot supply id")
	}
	return result, nil
}

func missing(err error) bool {
	return errors.Is(err, nats.ErrKeyNotFound) || errors.Is(err, nats.ErrKeyDeleted)
}

func (c *Core) execute(operation string, input protocol.Request) (any, error) {
	if strings.HasPrefix(operation, "kv_") {
		if err := required(input.Key, "key"); err != nil {
			return nil, err
		}
		key := "kv." + protocol.Encode(input.Namespace) + "." + protocol.Encode(input.Key)
		switch operation {
		case "kv_get":
			entry, err := c.kv.Get(key)
			if missing(err) {
				return protocol.Value{Found: false}, nil
			}
			if err != nil {
				return nil, err
			}
			return protocol.Value{Found: true, Value: entry.Value(), Revision: entry.Revision()}, nil
		case "kv_cas":
			if !json.Valid(input.Value) {
				return nil, protocol.BadRequest("value must be JSON")
			}
			var revision uint64
			var err error
			if input.Revision == 0 {
				revision, err = c.kv.Create(key, input.Value)
			} else {
				revision, err = c.kv.Update(key, input.Value, input.Revision)
			}
			if errors.Is(err, nats.ErrKeyExists) {
				return nil, &protocol.Error{Code: 409, Message: "KV revision conflict; reread before retrying"}
			}
			return revision, err
		case "kv_set":
			if !json.Valid(input.Value) {
				return nil, protocol.BadRequest("value must be JSON")
			}
			_, err := c.kv.Put(key, input.Value)
			return nil, err
		case "kv_delete":
			return c.remove(key)
		}
	}
	if strings.HasPrefix(operation, "collection_") {
		if err := required(input.Collection, "collection"); err != nil {
			return nil, err
		}
		prefix := "doc." + protocol.Encode(input.Collection) + "."
		if operation == "collection_list" {
			return c.listCollection(prefix)
		}
		if operation == "collection_insert" {
			if _, err := object(input.Document); err != nil {
				return nil, err
			}
			id := input.ID
			if id == "" {
				var bytes [16]byte
				if _, err := rand.Read(bytes[:]); err != nil {
					return nil, err
				}
				bytes[6] = (bytes[6] & 0x0f) | 0x40
				bytes[8] = (bytes[8] & 0x3f) | 0x80
				id = fmt.Sprintf("%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:])
			} else if !protocol.ValidName(id) {
				return nil, protocol.BadRequest("invalid document id")
			}
			var err error
			if input.IfNeverExisted {
				_, err = c.kv.Update(prefix+id, input.Document, 0)
			} else {
				_, err = c.kv.Create(prefix+id, input.Document)
			}
			if errors.Is(err, nats.ErrKeyExists) {
				return nil, &protocol.Error{Code: 409, Message: "document already exists"}
			}
			return id, err
		}
		if err := required(input.ID, "id"); err != nil {
			return nil, err
		}
		// IDs occupy one KV key token, never a subject or a key pattern.
		if !protocol.ValidName(input.ID) {
			return nil, protocol.BadRequest("invalid document id")
		}
		key := prefix + input.ID
		switch operation {
		case "collection_get":
			entry, err := c.kv.Get(key)
			if missing(err) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return collectionDocument(entry, input.ID)
		case "collection_delete":
			return c.remove(key)
		case "collection_update":
			patch, err := object(input.Document)
			if err != nil {
				return nil, err
			}
			for range 16 {
				entry, err := c.kv.Get(key)
				if missing(err) {
					return false, nil
				}
				if err != nil {
					return nil, err
				}
				var document map[string]json.RawMessage
				if err = json.Unmarshal(entry.Value(), &document); err != nil {
					return nil, err
				}
				for k, v := range patch {
					document[k] = v
				}
				data, err := json.Marshal(document)
				if err != nil {
					return nil, err
				}
				_, err = c.kv.Update(key, data, entry.Revision())
				if errors.Is(err, nats.ErrKeyExists) {
					continue
				}
				return err == nil, err
			}
			return nil, &protocol.Error{Code: 409, Message: "document changed concurrently; retry the update"}
		}
	}
	switch operation {
	case "publish", "enqueue":
		if err := required(input.Topic, "topic or queue"); err != nil {
			return nil, err
		}
		if !json.Valid(input.Payload) {
			return nil, protocol.BadRequest("payload must be JSON")
		}
		kind := "events"
		if operation == "enqueue" {
			kind = "jobs"
		}
		_, err := c.js.Publish(protocol.Prefix+"."+kind+"."+protocol.Encode(input.Topic), input.Payload)
		return nil, err
	case "consumer":
		return c.consumer(input)
	}
	return nil, protocol.BadRequest("unknown operation")
}

func collectionDocument(entry nats.KeyValueEntry, id string) (map[string]json.RawMessage, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(entry.Value(), &document); err != nil {
		return nil, err
	}
	document["id"], _ = json.Marshal(id)
	return document, nil
}

// listCollection reads only this collection's initial values, not later updates.
// ponytail: cap a listing at 1000 documents / 512 KiB; add pagination for larger collections.
func (c *Core) listCollection(prefix string) ([]map[string]json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	watcher, err := c.kv.Watch(prefix+"*", nats.IgnoreDeletes(), nats.Context(ctx))
	if err != nil {
		return nil, err
	}
	defer func() {
		// Unsubscribe cannot release a callback blocked on a full updates channel.
		drained := make(chan struct{})
		go func() {
			defer close(drained)
			for range watcher.Updates() {
			}
		}()
		_ = watcher.Stop()
		<-drained
	}()
	documents := map[string]map[string]json.RawMessage{}
	sizes := map[string]int{}
	bytes := 2
	errors := watcher.Error()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err, ok := <-errors:
			if !ok {
				errors = nil
			} else if err != nil {
				return nil, err
			}
		case entry, ok := <-watcher.Updates():
			if !ok {
				return nil, fmt.Errorf("collection listing stopped before completion")
			}
			if entry == nil {
				ids := make([]string, 0, len(documents))
				for id := range documents {
					ids = append(ids, id)
				}
				slices.Sort(ids)
				result := make([]map[string]json.RawMessage, 0, len(ids))
				for _, id := range ids {
					result = append(result, documents[id])
				}
				return result, nil
			}
			id := strings.TrimPrefix(entry.Key(), prefix)
			document, err := collectionDocument(entry, id)
			if err != nil {
				return nil, err
			}
			encoded, err := json.Marshal(document)
			if err != nil {
				return nil, err
			}
			bytes += len(encoded) + 1 - sizes[id]
			sizes[id], documents[id] = len(encoded)+1, document
			if len(documents) > 1000 || bytes > 512*1024 {
				return nil, &protocol.Error{Code: 413, Message: "collection listing exceeds the prototype response limit (1000 documents / 512 KiB)"}
			}
		}
	}
}

func (c *Core) remove(key string) (bool, error) {
	for range 16 {
		entry, err := c.kv.Get(key)
		if missing(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		err = c.kv.Delete(key, nats.LastRevision(entry.Revision()))
		if errors.Is(err, nats.ErrKeyExists) {
			continue
		}
		return err == nil, err
	}
	return false, &protocol.Error{Code: 409, Message: "value changed concurrently; retry the delete"}
}

func (c *Core) consumer(input protocol.Request) (any, error) {
	if err := required(input.Topic, "topic or queue"); err != nil {
		return nil, err
	}
	if err := required(input.Name, "consumer name"); err != nil {
		return nil, err
	}
	stream, kind, policy := protocol.Events, "events", nats.DeliverNewPolicy
	if input.Kind == "worker" {
		stream, kind, policy = protocol.Jobs, "jobs", nats.DeliverAllPolicy
	} else if input.Kind != "subscription" {
		return nil, protocol.BadRequest("invalid consumer kind")
	}
	name := protocol.ConsumerName(input.Kind, input.Topic, input.Name)
	subject := protocol.Prefix + "." + kind + "." + protocol.Encode(input.Topic)
	_, err := c.js.AddConsumer(stream, &nats.ConsumerConfig{
		Durable: name, FilterSubject: subject, DeliverPolicy: policy, AckPolicy: nats.AckExplicitPolicy,
		AckWait: 30 * time.Second, MaxDeliver: -1, MaxAckPending: 1024,
	})
	return protocol.Consumer{Stream: stream, Name: name, Subject: subject}, err
}

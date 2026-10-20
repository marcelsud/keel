// Package sdk provides Keel actions, storage, events and workers over NATS.
package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/marcelsud/keel/internal/protocol"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
)

type Error = protocol.Error
type Handler func(ctx *Context, input json.RawMessage) (any, error)
type ConsumerHandler func(ctx *Context, payload json.RawMessage) error

type Options struct {
	URL, Name, Version, Token, User string
	HTTP                            *HTTPOptions
}

type Context struct {
	context.Context
	KV         *KV
	Collection *Collections
	Pubsub     *Pubsub
	Queue      *Queue
	SQL        *SQL
	Log        *slog.Logger
	app        *App
}

func (c *Context) Call(service, action string, input, output any) error {
	return c.app.Call(c.Context, service, action, input, output)
}

type binding struct {
	kind, topic, name string
	handler           ConsumerHandler
}

type endpoint struct {
	subject  string
	handler  func(*Context, []byte) ([]byte, error)
	metadata map[string]string
}

type App struct {
	HTTP             *HTTPServer
	httpRoutes       []httpRoute
	httpGate         sync.Mutex
	httpClosing      bool
	httpActive       sync.WaitGroup
	KV               *KV
	Collection       *Collections
	Pubsub           *Pubsub
	Queue            *Queue
	SQL              *SQL
	nc               *nats.Conn
	options          Options
	mu               sync.Mutex
	started, stopped bool
	endpoints        map[string]endpoint
	bindings         []binding
	service          micro.Service
	cancel           context.CancelFunc
	workers          sync.WaitGroup
	closed           chan struct{}
	gate             sync.Mutex
	closing          bool
	active           sync.WaitGroup
}

func Connect(options Options) (*App, error) {
	if !protocol.ValidName(options.Name) || options.Name == "keel-core" {
		return nil, errors.New("invalid service name")
	}
	if options.Version == "" {
		options.Version = "0.1.0"
	}
	if options.URL == "" {
		options.URL = nats.DefaultURL
	}
	closed := make(chan struct{})
	auth := nats.Token(options.Token)
	if options.User != "" {
		auth = nats.UserInfo(options.User, options.Token)
	}
	inbox := "_INBOX"
	if options.User != "" {
		inbox = "keel.inbox." + options.User
	}
	nc, err := nats.Connect(options.URL, nats.Name(options.Name), auth, nats.CustomInboxPrefix(inbox), nats.Timeout(5*time.Second), nats.ClosedHandler(func(*nats.Conn) { close(closed) }))
	if err != nil {
		return nil, err
	}
	a := &App{nc: nc, options: options, endpoints: make(map[string]endpoint), closed: closed}
	a.KV = &KV{app: a}
	a.Collection = &Collections{app: a}
	a.Pubsub = &Pubsub{app: a}
	a.Queue = &Queue{app: a}
	a.SQL = &SQL{app: a, scope: options.Name}
	return a, nil
}

func (a *App) handlerContext(ctx context.Context) *Context {
	return &Context{Context: ctx, KV: a.KV, Collection: a.Collection, Pubsub: a.Pubsub, Queue: a.Queue, SQL: a.SQL, Log: slog.Default(), app: a}
}

func (a *App) Action(name string, handler Handler) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started || a.stopped {
		return errors.New("declare actions before start")
	}
	if !protocol.ValidName(name) || handler == nil {
		return errors.New("action requires a valid name and handler")
	}
	if _, ok := a.endpoints[name]; ok {
		return errors.New("duplicate action: " + name)
	}
	a.endpoints[name] = endpoint{subject: protocol.ActionSubject(a.options.Name, name), handler: func(ctx *Context, data []byte) ([]byte, error) {
		if !json.Valid(data) {
			return nil, &Error{Code: 400, Message: "input must be JSON"}
		}
		result, err := handler(ctx, data)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}}
	return nil
}

func (a *App) Subscription(topic, name string, handler ConsumerHandler) error {
	return a.bind(binding{kind: "subscription", topic: topic, name: name, handler: handler})
}

func (a *App) Worker(queue string, handler ConsumerHandler) error {
	return a.bind(binding{kind: "worker", topic: queue, name: queue, handler: handler})
}

func (a *App) bind(b binding) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started || a.stopped {
		return errors.New("declare consumers before start")
	}
	if b.topic == "" || b.name == "" || b.handler == nil {
		return errors.New("consumer requires topic, name and handler")
	}
	for _, existing := range a.bindings {
		if b.kind == existing.kind && b.topic == existing.topic && b.name == existing.name {
			return errors.New("duplicate consumer")
		}
	}
	a.bindings = append(a.bindings, b)
	return nil
}

func (a *App) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return errors.New("app is stopped")
	}
	if a.started {
		return nil
	}
	service, err := micro.AddService(a.nc, micro.Config{Name: a.options.Name, Version: a.options.Version})
	if err != nil {
		return err
	}
	var subscriptions []*nats.Subscription
	cleanup := func() {
		_ = service.Stop()
		for _, s := range subscriptions {
			_ = s.Unsubscribe()
		}
	}
	for name, route := range a.endpoints {
		err = service.AddEndpoint(name, micro.HandlerFunc(func(r micro.Request) { a.handle(r, route.handler) }), micro.WithEndpointSubject(route.subject), micro.WithEndpointMetadata(route.metadata))
		if err != nil {
			cleanup()
			return err
		}
	}
	js, err := a.nc.JetStream()
	if err != nil {
		cleanup()
		return err
	}
	for _, b := range a.bindings {
		var consumer protocol.Consumer
		if err = a.core(ctx, "consumer", protocol.Request{Kind: b.kind, Topic: b.topic, Name: b.name}, &consumer); err != nil {
			cleanup()
			return err
		}
		sub, err := js.PullSubscribe(consumer.Subject, consumer.Name, nats.Bind(consumer.Stream, consumer.Name))
		if err != nil {
			cleanup()
			return err
		}
		subscriptions = append(subscriptions, sub)
	}
	if err = a.nc.Flush(); err != nil {
		cleanup()
		return err
	}
	if err = a.startHTTP(); err != nil {
		cleanup()
		return err
	}
	a.service = service
	fetchCtx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.started = true
	for i, b := range a.bindings {
		a.workers.Add(1)
		go a.consume(fetchCtx, subscriptions[i], b.handler)
	}
	return nil
}

func (a *App) handle(r micro.Request, handler func(*Context, []byte) ([]byte, error)) {
	a.gate.Lock()
	if a.closing {
		a.gate.Unlock()
		_ = r.Error("503", "app is stopping", nil)
		return
	}
	a.active.Add(1)
	a.gate.Unlock()
	defer a.active.Done()
	defer func() {
		if failure := recover(); failure != nil {
			slog.Error("action panicked", "error", failure)
			_ = r.Error("500", "handler failed", nil)
		}
	}()
	data, err := handler(a.handlerContext(context.Background()), r.Data())
	if err == nil {
		_ = r.Respond(data)
		return
	}
	var public *Error
	if errors.As(err, &public) {
		_ = r.Error(strconv.Itoa(public.Code), public.Message, nil)
		return
	}
	slog.Error("action failed", "error", err)
	_ = r.Error("500", "handler failed", nil)
}

func (a *App) consume(ctx context.Context, sub *nats.Subscription, handler ConsumerHandler) {
	defer a.workers.Done()
	defer sub.Unsubscribe()
	for ctx.Err() == nil {
		messages, err := sub.Fetch(1, nats.MaxWait(250*time.Millisecond))
		if errors.Is(err, nats.ErrTimeout) {
			continue
		}
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("consumer stopped", "error", err)
			}
			return
		}
		for _, message := range messages {
			if ctx.Err() != nil {
				_ = message.Nak()
				continue
			}
			a.deliver(message, handler)
		}
	}
}

func (a *App) deliver(message *nats.Msg, handler ConsumerHandler) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				_ = message.InProgress()
			}
		}
	}()
	defer close(done)
	var err error
	func() {
		defer func() {
			if failure := recover(); failure != nil {
				err = errors.New("consumer panicked")
				slog.Error("consumer panicked", "error", failure)
			}
		}()
		err = handler(a.handlerContext(context.Background()), message.Data)
	}()
	if err != nil {
		slog.Error("delivery failed", "error", err)
		_ = message.NakWithDelay(time.Second)
		return
	}
	if err = message.AckSync(); err != nil {
		slog.Error("acknowledgement failed", "error", err)
	}
}

func (a *App) Call(ctx context.Context, service, action string, input, output any) error {
	if !protocol.ValidName(service) || !protocol.ValidName(action) {
		return errors.New("invalid service or action name")
	}
	return a.request(ctx, protocol.ActionSubject(service, action), input, output)
}

func (a *App) core(ctx context.Context, operation string, input protocol.Request, output any) error {
	return a.request(ctx, protocol.Prefix+".core."+operation, input, output)
}

func (a *App) request(ctx context.Context, subject string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	message, err := a.requestBytes(ctx, subject, data)
	if err != nil {
		return err
	}
	if output == nil {
		return nil
	}
	return json.Unmarshal(message, output)
}

func (a *App) requestBytes(ctx context.Context, subject string, data []byte) ([]byte, error) {
	if _, bounded := ctx.Deadline(); !bounded {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	message, err := a.nc.RequestWithContext(ctx, subject, data)
	if err != nil {
		return nil, err
	}
	if code := message.Header.Get("Nats-Service-Error-Code"); code != "" {
		number, err := strconv.Atoi(code)
		if err != nil {
			return nil, errors.New("invalid service error code")
		}
		return nil, &Error{Code: number, Message: message.Header.Get("Nats-Service-Error")}
	}
	return message.Data, nil
}

func (a *App) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return nil
	}
	a.stopped = true
	err := a.stopHTTP()
	a.gate.Lock()
	a.closing = true
	a.gate.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
	if a.service != nil {
		if stopErr := a.service.Stop(); err == nil {
			err = stopErr
		}
	}
	a.active.Wait()
	a.workers.Wait()
	if drainErr := a.nc.Drain(); drainErr != nil {
		a.nc.Close()
		if err == nil {
			err = drainErr
		}
	}
	<-a.closed
	return err
}

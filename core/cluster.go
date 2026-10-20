package core

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/marcelsud/keel/internal/protocol"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// ClusterOptions configures one embedded NATS member. Members have distinct
// names, listeners and data directories, and share the same credentials.
type ClusterOptions struct {
	Name      string   `json:"name"`
	Node      string   `json:"node"`
	Listen    string   `json:"listen"`
	Routes    []string `json:"routes"`
	Replicas  int      `json:"replicas,omitempty"`
	SQLListen string   `json:"sqlListen,omitempty"`
	SQLPeers  []string `json:"sqlPeers,omitempty"`
}

func configureCluster(options *server.Options, config Options) error {
	cluster := config.Cluster
	if cluster == nil {
		return nil
	}
	if !protocol.ValidName(cluster.Name) || !protocol.ValidName(cluster.Node) {
		return errors.New("cluster and node names are required and must be valid names")
	}
	if config.Credentials == nil {
		return errors.New("clustering requires per-service credentials shared by all members")
	}
	if cluster.Replicas != 0 && cluster.Replicas != 3 && cluster.Replicas != 5 {
		return errors.New("cluster replicas must be 3 or 5")
	}
	host, port, err := net.SplitHostPort(cluster.Listen)
	if err != nil {
		return fmt.Errorf("cluster listener must be host:port: %w", err)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return errors.New("cluster listener requires a fixed port between 1 and 65535")
	}
	if host == "" {
		return errors.New("cluster listener requires a host")
	}
	options.ServerName = cluster.Node
	options.Cluster = server.ClusterOpts{Name: cluster.Name, Host: host, Port: number, Username: "keel-cluster", Password: config.Credentials.Admin}
	transport, err := clusterTLS(config)
	if err != nil {
		return err
	}
	options.Cluster.TLSConfig = transport
	for _, address := range cluster.Routes {
		route, err := url.Parse(address)
		if err != nil || route.Scheme != "nats-route" || route.Hostname() == "" || route.Port() == "" || route.User != nil || route.Path != "" || route.RawQuery != "" || route.Fragment != "" {
			return errors.New("cluster routes must be nats-route://host:port URLs without credentials, paths, or queries")
		}
		route.User = url.UserPassword("keel-cluster", config.Credentials.Admin)
		options.Routes = append(options.Routes, route)
	}
	return nil
}

func (c *Core) initializeStorage(options Options) error {
	replicas := 1
	if options.Cluster != nil {
		replicas = options.Cluster.Replicas
		if replicas == 0 {
			replicas = 3
		}
	}
	initialize := func() error {
		var err error
		c.kv, err = c.js.CreateKeyValue(&nats.KeyValueConfig{Bucket: protocol.Bucket, Storage: nats.FileStorage, Replicas: replicas})
		if err != nil {
			return err
		}
		for _, config := range []*nats.StreamConfig{
			{Name: protocol.Events, Subjects: []string{protocol.Prefix + ".events.>"}, Storage: nats.FileStorage, Retention: nats.LimitsPolicy, Replicas: replicas},
			{Name: protocol.Jobs, Subjects: []string{protocol.Prefix + ".jobs.>"}, Storage: nats.FileStorage, Retention: nats.WorkQueuePolicy, Replicas: replicas},
		} {
			if _, err = c.js.AddStream(config); err != nil {
				return err
			}
		}
		return nil
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		err := initialize()
		if err == nil {
			return nil
		}
		if options.Cluster == nil || time.Now().After(deadline) {
			return fmt.Errorf("initialize persistent storage: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

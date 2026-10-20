package core_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/marcelsud/keel/core"
	"github.com/marcelsud/keel/sdk"
	"github.com/nats-io/nats.go"
)

func freeClusterAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}

func TestClusterReplicatesStorageAndLosesQuorum(t *testing.T) {
	credentials := &core.Credentials{Admin: "test-cluster-admin-000000000000", Services: map[string]string{"alpha": "test-cluster-alpha-000000000000"}}
	addresses := []string{freeClusterAddress(t), freeClusterAddress(t), freeClusterAddress(t)}
	options := make([]core.Options, 3)
	for i := range options {
		routes := []string{}
		for j, address := range addresses {
			if i != j {
				routes = append(routes, "nats-route://"+address)
			}
		}
		options[i] = core.Options{Port: -1, DataDir: t.TempDir(), Credentials: credentials, Cluster: &core.ClusterOptions{Name: "keel-test", Node: fmt.Sprintf("node-%d", i), Listen: addresses[i], Routes: routes}}
	}
	type started struct {
		index int
		node  *core.Core
		err   error
	}
	ready := make(chan started, 3)
	for i := range options {
		go func() { node, err := core.Start(options[i]); ready <- started{i, node, err} }()
	}
	nodes := make([]*core.Core, 3)
	t.Cleanup(func() {
		for _, node := range nodes {
			if node != nil {
				node.Close()
			}
		}
	})
	var urls []string
	var first error
	for range 3 {
		item := <-ready
		if item.err != nil {
			first = item.err
			continue
		}
		nodes[item.index] = item.node
		urls = append(urls, item.node.URL())
	}
	if first != nil {
		t.Fatal(first)
	}
	app, err := sdk.Connect(sdk.Options{Name: "alpha", User: "alpha", Token: credentials.Services["alpha"], URL: strings.Join(urls, ",")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Stop() })
	ctx := context.Background()
	if err := app.KV.Set(ctx, "replicated", "survives"); err != nil {
		t.Fatal(err)
	}
	admin, err := nats.Connect(strings.Join(urls, ","), nats.UserInfo(core.AdminUser, credentials.Admin))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	js, err := admin.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	info, err := js.StreamInfo("KV_KEEL_DATA")
	if err != nil {
		t.Fatal(err)
	}
	if info.Config.Replicas != 3 || info.Cluster == nil {
		t.Fatal("storage is not replicated", info.Config)
	}
	leader := -1
	for i := range options {
		if options[i].Cluster.Node == info.Cluster.Leader {
			leader = i
		}
	}
	if leader < 0 {
		t.Fatal("unknown stream leader", info.Cluster)
	}
	nodes[leader].Close()
	nodes[leader] = nil
	deadline := time.Now().Add(15 * time.Second)
	for {
		attempt, cancel := context.WithTimeout(ctx, time.Second)
		var value string
		found, err := app.KV.Get(attempt, "replicated", &value)
		cancel()
		if err == nil && found && value == "survives" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("leader failover lost committed value: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := app.KV.Set(ctx, "after-failover", true); err != nil {
		t.Fatal(err)
	}
	for i, node := range nodes {
		if node != nil {
			node.Close()
			nodes[i] = nil
			break
		}
	}
	attempt, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if err := app.KV.Set(attempt, "without-quorum", true); err == nil {
		t.Fatal("write acknowledged without quorum")
	}
}

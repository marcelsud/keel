package core_test

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcelsud/keel/core"
	"github.com/marcelsud/keel/sdk"
)

func TestStartRejectsSharedDataDirectory(t *testing.T) {
	directory := t.TempDir()
	first, err := core.Start(core.Options{Port: -1, DataDir: directory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Close)
	address, err := url.Parse(first.URL())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(address.Port())
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range []int{port, -1} {
		started := time.Now()
		second, err := core.Start(core.Options{Port: port, DataDir: directory})
		if second != nil {
			second.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "already running") {
			t.Fatalf("duplicate environment: %v", err)
		}
		if time.Since(started) > 2*time.Second {
			t.Fatal("duplicate startup waited for readiness instead of failing immediately")
		}
	}
	client, err := sdk.Connect(sdk.Options{Name: "lock-check", URL: first.URL()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Stop() })
	if err := client.KV.Set(context.Background(), "survives", "value"); err != nil {
		t.Fatal("first environment stopped working", err)
	}
	if err := client.Stop(); err != nil {
		t.Fatal(err)
	}
	first.Close()
	restarted, err := core.Start(core.Options{Port: -1, DataDir: directory})
	if err != nil {
		t.Fatal("lock was not released on shutdown", err)
	}
	t.Cleanup(restarted.Close)
	reader, err := sdk.Connect(sdk.Options{Name: "lock-reader", URL: restarted.URL()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Stop() })
	var value string
	found, err := reader.KV.Get(context.Background(), "survives", &value)
	if err != nil || !found || value != "value" {
		t.Fatal("stored data did not survive restart", found, value, err)
	}
}

func TestStartReportsOccupiedPortAndReleasesDataDirectory(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	directory := t.TempDir()
	started := time.Now()
	instance, err := core.Start(core.Options{Port: listener.Addr().(*net.TCPAddr).Port, DataDir: directory})
	if instance != nil {
		instance.Close()
	}
	if err == nil || !strings.Contains(err.Error(), listener.Addr().String()) || !strings.Contains(err.Error(), "listening on port") {
		t.Fatalf("expected actual bind error and address: %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("port conflict waited for readiness instead of reporting the bind error")
	}
	restarted, err := core.Start(core.Options{Port: -1, DataDir: directory})
	if err != nil {
		t.Fatal("failed startup kept the directory locked", err)
	}
	restarted.Close()
}

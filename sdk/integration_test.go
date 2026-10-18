package sdk_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcelsud/keel/core"
	"github.com/marcelsud/keel/sdk"
	"github.com/nats-io/nats.go"
)

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func startCore(t *testing.T) *core.Core {
	t.Helper()
	c, err := core.Start(core.Options{Port: -1, DataDir: t.TempDir(), Token: "test-token"})
	check(t, err)
	t.Cleanup(c.Close)
	return c
}

func connect(t *testing.T, c *core.Core, name string) *sdk.App {
	t.Helper()
	a, err := sdk.Connect(sdk.Options{URL: c.URL(), Name: name, Token: "test-token"})
	check(t, err)
	t.Cleanup(func() { check(t, a.Stop()) })
	return a
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(8 * time.Second):
		t.Fatal("delivery timed out")
		var zero T
		return zero
	}
}

func TestStorageAndActions(t *testing.T) {
	c := startCore(t)
	a := connect(t, c, "go")
	ctx := context.Background()
	check(t, a.Action("echo", func(_ *sdk.Context, input json.RawMessage) (any, error) { return input, nil }))
	check(t, a.Action("fail", func(ctx *sdk.Context, _ json.RawMessage) (any, error) {
		if err := ctx.KV.Set(ctx, "independent", true); err != nil {
			return nil, err
		}
		return nil, errors.New("expected failure")
	}))
	check(t, a.Start(ctx))
	var echoed map[string]any
	check(t, a.Call(ctx, "go", "echo", map[string]any{"value": 42}, &echoed))
	if echoed["value"] != float64(42) {
		t.Fatal(echoed)
	}
	var value any
	found, err := a.KV.Get(ctx, "missing", &value)
	check(t, err)
	if found {
		t.Fatal("missing key was found")
	}
	check(t, a.KV.Set(ctx, "nullable", nil))
	found, err = a.KV.Get(ctx, "nullable", &value)
	check(t, err)
	if !found || value != nil {
		t.Fatal("stored null was not preserved")
	}
	check(t, a.KV.Namespace("settings.日本語").Set(ctx, "theme.*", "dark"))
	found, err = a.KV.Get(ctx, "theme.*", &value)
	check(t, err)
	if found {
		t.Fatal("namespace leaked into raw KV")
	}
	found, err = a.KV.Namespace("settings.日本語").Get(ctx, "theme.*", &value)
	check(t, err)
	if !found || value != "dark" {
		t.Fatal(value)
	}
	id, err := a.Collection.Insert(ctx, "notes", map[string]any{"body": "first", "nested": map[string]int{"a": 1}})
	check(t, err)
	updated, err := a.Collection.Update(ctx, "notes", id, map[string]any{"nested": map[string]int{"b": 2}})
	check(t, err)
	if !updated {
		t.Fatal("update failed")
	}
	var document map[string]any
	found, err = a.Collection.Get(ctx, "notes", id, &document)
	check(t, err)
	if !found || document["id"] != id || document["body"] != "first" || len(document["nested"].(map[string]any)) != 1 {
		t.Fatal(document)
	}
	_, err = a.Collection.Insert(ctx, "notes", map[string]any{"id": "forbidden"})
	var serviceErr *sdk.Error
	if !errors.As(err, &serviceErr) || serviceErr.Code != 400 {
		t.Fatalf("expected 400, got %v", err)
	}
	removed, err := a.Collection.Delete(ctx, "notes", id)
	check(t, err)
	if !removed {
		t.Fatal("delete failed")
	}
	removed, err = a.Collection.Delete(ctx, "notes", id)
	check(t, err)
	if removed {
		t.Fatal("duplicate delete returned true")
	}
	err = a.Call(ctx, "go", "fail", nil, nil)
	if !errors.As(err, &serviceErr) || serviceErr.Code != 500 {
		t.Fatalf("expected 500, got %v", err)
	}
	found, err = a.KV.Get(ctx, "independent", &value)
	check(t, err)
	if !found || value != true {
		t.Fatal("failed handler incorrectly rolled back an independent write")
	}
	nc, err := nats.Connect(c.URL(), nats.Token("test-token"))
	check(t, err)
	defer nc.Close()
	info, err := nc.Request("$SRV.INFO.go", nil, time.Second)
	check(t, err)
	var service struct {
		Name      string `json:"name"`
		Endpoints []any  `json:"endpoints"`
	}
	check(t, json.Unmarshal(info.Data, &service))
	if service.Name != "go" || len(service.Endpoints) != 2 {
		t.Fatal(string(info.Data))
	}
	response, err := nc.Request("keel.v1.core.kv_set", []byte("not JSON"), time.Second)
	check(t, err)
	if response.Header.Get("Nats-Service-Error-Code") != "400" {
		t.Fatal("malformed payload was accepted")
	}
}

func TestDurableDeliveryAndCompetingWorkers(t *testing.T) {
	c := startCore(t)
	a, b := connect(t, c, "one"), connect(t, c, "two")
	ctx := context.Background()
	events := make(chan string, 4)
	check(t, a.Subscription("note.created", "first", func(_ *sdk.Context, _ json.RawMessage) error { events <- "first"; return nil }))
	check(t, b.Subscription("note.created", "second", func(_ *sdk.Context, _ json.RawMessage) error { events <- "second"; return nil }))
	jobs := make(chan int, 4)
	var attempts atomic.Int32
	handler := func(_ *sdk.Context, _ json.RawMessage) error {
		attempt := int(attempts.Add(1))
		if attempt == 1 {
			return errors.New("retry this delivery")
		}
		jobs <- attempt
		return nil
	}
	check(t, a.Worker("notify", handler))
	check(t, b.Worker("notify", handler))
	check(t, a.Start(ctx))
	check(t, b.Start(ctx))
	check(t, a.Pubsub.Publish(ctx, "note.created", map[string]string{"id": "1"}))
	first, second := receive(t, events), receive(t, events)
	if first == second {
		t.Fatal("named subscriptions did not fan out")
	}
	check(t, a.Queue.Enqueue(ctx, "notify", nil))
	if receive(t, jobs) != 2 {
		t.Fatal("job did not retry exactly once")
	}
	check(t, a.Stop())
	check(t, b.Stop())
	if attempts.Load() != 2 {
		t.Fatal("job was processed by more than one worker")
	}
}

func TestPersistenceAndOfflineConsumers(t *testing.T) {
	dir := t.TempDir()
	c, err := core.Start(core.Options{Port: -1, DataDir: dir})
	check(t, err)
	t.Cleanup(func() { c.Close() })
	a, err := sdk.Connect(sdk.Options{URL: c.URL(), Name: "first"})
	check(t, err)
	t.Cleanup(func() { _ = a.Stop() })
	ctx := context.Background()
	check(t, a.KV.Set(ctx, "persistent", 42))
	id, err := a.Collection.Insert(ctx, "notes", map[string]string{"body": "saved"})
	check(t, err)
	check(t, a.Subscription("offline", "stable", func(_ *sdk.Context, _ json.RawMessage) error { return nil }))
	check(t, a.Start(ctx))
	check(t, a.Stop())
	publisher, err := sdk.Connect(sdk.Options{URL: c.URL(), Name: "publisher"})
	check(t, err)
	check(t, publisher.Queue.Enqueue(ctx, "offline", map[string]string{"body": "saved"}))
	check(t, publisher.Pubsub.Publish(ctx, "offline", map[string]string{"body": "saved"}))
	check(t, publisher.Stop())
	c.Close()
	c, err = core.Start(core.Options{Port: -1, DataDir: dir})
	check(t, err)
	b, err := sdk.Connect(sdk.Options{URL: c.URL(), Name: "second"})
	check(t, err)
	t.Cleanup(func() { check(t, b.Stop()) })
	var value int
	found, err := b.KV.Get(ctx, "persistent", &value)
	check(t, err)
	if !found || value != 42 {
		t.Fatal("KV did not survive restart")
	}
	var document map[string]any
	found, err = b.Collection.Get(ctx, "notes", id, &document)
	check(t, err)
	if !found || document["body"] != "saved" {
		t.Fatal("collection did not survive restart")
	}
	delivered := make(chan string, 2)
	check(t, b.Subscription("offline", "stable", func(_ *sdk.Context, _ json.RawMessage) error { delivered <- "event"; return nil }))
	check(t, b.Worker("offline", func(_ *sdk.Context, _ json.RawMessage) error { delivered <- "job"; return nil }))
	check(t, b.Start(ctx))
	if receive(t, delivered) == receive(t, delivered) {
		t.Fatal("offline job or event was lost")
	}
}

func TestStopDrainsActiveAction(t *testing.T) {
	c := startCore(t)
	a := connect(t, c, "drain")
	entered, release := make(chan bool, 1), make(chan struct{})
	check(t, a.Action("slow", func(ctx *sdk.Context, _ json.RawMessage) (any, error) {
		entered <- true
		<-release
		return true, ctx.KV.Set(ctx, "drained", true)
	}))
	check(t, a.Start(context.Background()))
	caller := connect(t, c, "caller")
	result := make(chan error, 1)
	go func() { result <- caller.Call(context.Background(), "drain", "slow", nil, nil) }()
	receive(t, entered)
	stopped := make(chan error, 1)
	go func() { stopped <- a.Stop() }()
	select {
	case err := <-stopped:
		t.Fatalf("stop returned before handler completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	check(t, receive(t, result))
	check(t, receive(t, stopped))
}

func TestTypeScriptInteroperability(t *testing.T) {
	if os.Getenv("KEEL_TYPESCRIPT_TEST") != "1" {
		t.Skip("set KEEL_TYPESCRIPT_TEST=1 after building the TypeScript SDK")
	}
	c := startCore(t)
	a := connect(t, c, "go")
	check(t, a.Action("echo", func(_ *sdk.Context, input json.RawMessage) (any, error) { return input, nil }))
	check(t, a.Start(context.Background()))
	fixture, err := filepath.Abs("../sdks/typescript/test/fixture.mjs")
	check(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, "node", fixture)
	command.Env = append(os.Environ(), "KEEL_URL="+c.URL(), "KEEL_TOKEN=test-token")
	stdout, err := command.StdoutPipe()
	check(t, err)
	command.Stderr = os.Stderr
	check(t, command.Start())
	t.Cleanup(func() { cancel(); _ = command.Wait() })
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
		}
	}()
	if receive(t, ready) != "ready" {
		t.Fatal("TypeScript fixture did not start")
	}
	var result map[string]any
	check(t, a.Call(context.Background(), "typescript", "roundtrip", map[string]string{"body": "hello"}, &result))
	if result["body"] != "hello" || result["language"] != "typescript" {
		t.Fatal(result)
	}
	check(t, a.KV.Set(context.Background(), "from-go", "shared"))
	check(t, a.Call(context.Background(), "typescript", "shared", nil, &result))
	if result["value"] != "shared" {
		t.Fatal(result)
	}
	check(t, a.Queue.Enqueue(context.Background(), "typescript", nil))
	deadline := time.Now().Add(5 * time.Second)
	for {
		var done bool
		found, err := a.KV.Get(context.Background(), "typescript-job", &done)
		check(t, err)
		if found && done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("TypeScript worker did not process Go job")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

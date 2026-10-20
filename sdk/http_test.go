package sdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcelsud/keel/sdk"
)

func TestHTTPRouting(t *testing.T) {
	a := &sdk.App{}
	check(t, a.Route("GET", "/notes/:id", func(ctx *sdk.HTTPContext) (any, error) {
		return map[string]any{"params": ctx.Params, "query": ctx.Query}, nil
	}))
	check(t, a.Route("GET", "/notes/new", func(*sdk.HTTPContext) (any, error) { return "literal", nil }))
	check(t, a.Route("HEAD", "/notes/new", sdk.HTTPRoute{Status: 202, Handler: func(*sdk.HTTPContext) (any, error) { return nil, nil }}))
	check(t, a.Route("GET", "/raw", func(*sdk.HTTPContext) (any, error) {
		return &http.Response{StatusCode: 201, Header: http.Header{"X-Test": {"ok"}}, Body: io.NopCloser(strings.NewReader("raw"))}, nil
	}))
	check(t, a.Route("POST", "/body", sdk.HTTPRoute{
		Status: 201,
		Body: func(value any) (any, error) {
			body, ok := value.(map[string]any)
			if !ok {
				return nil, errors.New("body must be an object")
			}
			if _, ok := body["title"].(string); !ok {
				return nil, errors.New("title must be a string")
			}
			return body, nil
		},
		Handler: func(ctx *sdk.HTTPContext) (any, error) { return ctx.Body, nil },
		Output:  func(value any, _ *sdk.HTTPContext) (any, error) { return map[string]any{"data": value}, nil },
	}))
	for _, status := range []int{204, 205, 304} {
		check(t, a.Route("DELETE", "/empty/"+strconv.Itoa(status), sdk.HTTPRoute{Status: status, Handler: func(*sdk.HTTPContext) (any, error) { return make(chan int), nil }}))
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
		want, allow        string
	}{
		{"GET", "/notes/new", "", 200, `"literal"`, ""},
		{"GET", "/notes/a%2Fb?tag=a&tag=b", "", 200, `{"params":{"id":"a/b"},"query":{"tag":["a","b"]}}`, ""},
		{"HEAD", "/notes/new", "", 202, "", ""},
		{"HEAD", "/notes/id", "", 200, "", ""},
		{"HEAD", "/missing", "", 404, "", ""},
		{"OPTIONS", "/notes/new", "", 204, "", "GET, HEAD, OPTIONS"},
		{"DELETE", "/notes/new", "", 405, `{"error":"Method not allowed"}`, "GET, HEAD, OPTIONS"},
		{"GET", "/missing", "", 404, `{"error":"Not found"}`, ""},
		{"GET", "/%6eotes/new", "", 404, `{"error":"Not found"}`, ""},
		{"GET", "/raw", "", 201, "raw", ""},
		{"POST", "/body", `{"title":"hello"}`, 201, `{"data":{"title":"hello"}}`, ""},
		{"POST", "/body", `{"title":1}`, 400, `{"error":"title must be a string"}`, ""},
		{"POST", "/body", `{`, 400, `{"error":"Request body must contain valid JSON"}`, ""},
		{"POST", "/body", `{} {}`, 400, `{"error":"Request body must contain valid JSON"}`, ""},
		{"POST", "/body", "  ", 400, `{"error":"Request body must contain valid JSON"}`, ""},
		{"DELETE", "/empty/204", "", 204, "", ""},
		{"DELETE", "/empty/205", "", 205, "", ""},
		{"DELETE", "/empty/304", "", 304, "", ""},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			if w.Code != tc.status || strings.TrimSpace(w.Body.String()) != tc.want || w.Header().Get("Allow") != tc.allow {
				t.Fatalf("status=%d body=%q allow=%q", w.Code, w.Body.String(), w.Header().Get("Allow"))
			}
		})
	}
	for _, path := range []string{"relative", "/a//b", "/a/../b", "/a/%2e%2e/b", "/café", "/:id/:id"} {
		if err := a.Route("GET", path, func(*sdk.HTTPContext) (any, error) { return nil, nil }); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	if err := a.Route("GET", "/notes/:other", sdk.HTTPRoute{Action: "echo"}); err == nil {
		t.Fatal("accepted duplicate route shape")
	}
}

func TestHTTPActionAndShutdown(t *testing.T) {
	c := startCore(t)
	a, err := sdk.Connect(sdk.Options{URL: c.URL(), Name: "http", Token: "test-token", HTTP: &sdk.HTTPOptions{Address: "127.0.0.1:0"}})
	check(t, err)
	t.Cleanup(func() { check(t, a.Stop()) })
	check(t, a.Action("echo", func(ctx *sdk.Context, input json.RawMessage) (any, error) {
		if string(input) == `{"bad":true}` {
			return nil, &sdk.Error{Code: 400, Message: "invalid input"}
		}
		return input, nil
	}))
	check(t, a.Route("POST", "/notes", sdk.HTTPRoute{Action: "echo", Status: 201}))
	check(t, a.Action("null", func(*sdk.Context, json.RawMessage) (any, error) { return nil, nil }))
	check(t, a.Route("GET", "/null", sdk.HTTPRoute{Action: "null"}))
	entered, release := make(chan struct{}), make(chan struct{})
	check(t, a.Route("GET", "/slow", sdk.HTTPRoute{Action: "echo", Input: func(*sdk.HTTPContext) (any, error) {
		close(entered)
		<-release
		return map[string]bool{"drained": true}, nil
	}}))
	check(t, a.Start(context.Background()))
	client := &http.Client{Timeout: 5 * time.Second}
	null, err := client.Get(a.HTTP.URL + "/null")
	check(t, err)
	data, err := io.ReadAll(null.Body)
	null.Body.Close()
	check(t, err)
	if null.StatusCode != 200 || string(data) != "null" {
		t.Fatalf("null action: %d %s", null.StatusCode, data)
	}
	for _, tc := range []struct {
		body   string
		status int
	}{{`{"title":"hello"}`, 201}, {`{"bad":true}`, 400}, {`{`, 400}} {
		response, err := client.Post(a.HTTP.URL+"/notes", "application/json", strings.NewReader(tc.body))
		check(t, err)
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("%s: status %d", tc.body, response.StatusCode)
		}
	}
	if err := a.Route("GET", "/late", sdk.HTTPRoute{Action: "echo"}); err == nil {
		t.Fatal("accepted route after Start")
	}
	result := make(chan error, 1)
	go func() {
		response, err := client.Get(a.HTTP.URL + "/slow")
		if err == nil {
			defer response.Body.Close()
			var body map[string]bool
			err = json.NewDecoder(response.Body).Decode(&body)
			if response.StatusCode != 200 || !body["drained"] {
				err = errors.New("request did not drain")
			}
		}
		result <- err
	}()
	receive(t, entered)
	stopped := make(chan error, 1)
	go func() { stopped <- a.Stop() }()
	// Wait for shutdown to reject new work before releasing the admitted request.
	deadline := time.Now().Add(3 * time.Second)
	for {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "/missing", nil))
		if w.Code == 503 {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("shutdown did not reject new HTTP work")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	check(t, receive(t, result))
	check(t, receive(t, stopped))
}

func TestHTTPStartupRetry(t *testing.T) {
	c := startCore(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	check(t, err)
	defer occupied.Close()
	a, err := sdk.Connect(sdk.Options{URL: c.URL(), Name: "http-retry", Token: "test-token", HTTP: &sdk.HTTPOptions{Address: occupied.Addr().String()}})
	check(t, err)
	defer a.Stop()
	check(t, a.Action("echo", func(_ *sdk.Context, input json.RawMessage) (any, error) { return input, nil }))
	check(t, a.Route("POST", "/", sdk.HTTPRoute{Action: "echo"}))
	if err := a.Start(context.Background()); err == nil {
		t.Fatal("expected occupied port failure")
	}
	check(t, occupied.Close())
	check(t, a.Start(context.Background()))
	response, err := http.Post(a.HTTP.URL, "application/json", strings.NewReader(`{"retry":true}`))
	check(t, err)
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
}

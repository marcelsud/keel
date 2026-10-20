package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/marcelsud/keel/internal/protocol"
)

// HTTPOptions starts an HTTP listener with the app. Address defaults to 127.0.0.1:3000.
type HTTPOptions struct{ Address string }

type HTTPContext struct {
	Request *http.Request
	Params  map[string]string
	Query   map[string]any // One string, or []string for repeated keys.
	Body    any
}

type HTTPHandler func(*HTTPContext) (any, error)
type HTTPParser func(any) (any, error)

// HTTPRoute uses either Handler or the name of a JSON Action on this app.
// Input defaults to the JSON request body. Output runs after the action returns.
type HTTPRoute struct {
	Handler HTTPHandler
	Action  string
	Input   func(*HTTPContext) (any, error)
	Output  func(any, *HTTPContext) (any, error)
	Body    HTTPParser
	Params  func(map[string]string) error
	Query   func(map[string]any) error
	Status  int
}

type HTTPServer struct {
	URL    string
	server *http.Server
}

type httpRoute struct {
	method, shape string
	segments      []string
	options       HTTPRoute
}

var httpMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
var httpParameter = regexp.MustCompile(`^[A-Za-z_]\w*$`)

func pathSegments(path string) []string {
	return strings.Split(strings.TrimSuffix(path, "/"), "/")[1:]
}

// Route declares a direct HTTPHandler or an HTTPRoute before Start.
// Return *http.Response from a handler to control headers, status and raw body.
func (a *App) Route(method, path string, definition any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started || a.stopped {
		return errors.New("declare routes before start")
	}
	if !slices.Contains(httpMethods, method) {
		return fmt.Errorf("unsupported HTTP method: %s", method)
	}
	u, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#*\\") || strings.Contains(path, "//") || u.EscapedPath() != path {
		return errors.New("routes require an absolute URL-encoded path with literal segments or :parameters")
	}
	segments := pathSegments(path)
	shape := slices.Clone(segments)
	names := make(map[string]bool)
	for i, segment := range segments {
		decoded, _ := url.PathUnescape(segment)
		if decoded == "." || decoded == ".." {
			return errors.New("route paths cannot contain dot segments")
		}
		if strings.HasPrefix(segment, ":") {
			name := segment[1:]
			if !httpParameter.MatchString(name) || names[name] {
				return errors.New("invalid or repeated route parameter")
			}
			names[name], shape[i] = true, ":"
		}
	}
	key := strings.Join(shape, "/")
	for _, route := range a.httpRoutes {
		if route.method == method && route.shape == key {
			return fmt.Errorf("duplicate route: %s %s", method, path)
		}
	}
	var options HTTPRoute
	switch value := definition.(type) {
	case HTTPRoute:
		options = value
	case HTTPHandler:
		options.Handler = value
	case func(*HTTPContext) (any, error):
		options.Handler = value
	default:
		return errors.New("a route requires a handler or HTTPRoute")
	}
	if (options.Handler != nil) == (options.Action != "") || (options.Input != nil && options.Action == "") {
		return errors.New("a route requires exactly one handler or action; input requires an action")
	}
	if options.Action != "" && !protocol.ValidName(options.Action) {
		return errors.New("invalid action name")
	}
	if options.Status != 0 && (options.Status < 200 || options.Status > 599) {
		return errors.New("route status must be between 200 and 599")
	}
	a.httpGate.Lock()
	defer a.httpGate.Unlock()
	a.httpRoutes = append(a.httpRoutes, httpRoute{method, key, segments, options})
	slices.SortStableFunc(a.httpRoutes, func(a, b httpRoute) int {
		for i := 0; i < min(len(a.segments), len(b.segments)); i++ {
			x, y := strings.HasPrefix(a.segments[i], ":"), strings.HasPrefix(b.segments[i], ":")
			if x != y {
				if x {
					return 1
				}
				return -1
			}
		}
		return 0
	})
	return nil
}

func (a *App) startHTTP() error {
	if a.options.HTTP == nil {
		return nil
	}
	address := a.options.HTTP.Address
	if address == "" {
		address = "127.0.0.1:3000"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: a, ReadHeaderTimeout: 5 * time.Second}
	a.HTTP = &HTTPServer{URL: "http://" + listener.Addr().String(), server: server}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server failed", "error", err)
		}
	}()
	return nil
}

// ServeHTTP also supports hosting the app in an existing net/http server.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.httpGate.Lock()
	if a.httpClosing {
		a.httpGate.Unlock()
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	a.httpActive.Add(1)
	routes := slices.Clone(a.httpRoutes)
	a.httpGate.Unlock()
	defer a.httpActive.Done()
	defer func() {
		if failure := recover(); failure != nil {
			slog.Error("HTTP handler panicked", "error", failure)
			writeHTTPError(w, r, 500, "Internal server error")
		}
	}()
	segments := pathSegments(r.URL.EscapedPath())
	decoded := make([]string, len(segments))
	for i, segment := range segments {
		var err error
		decoded[i], err = url.PathUnescape(segment)
		if err != nil {
			writeHTTPError(w, r, 400, "Invalid path encoding")
			return
		}
	}
	var matched *httpRoute
	allowed := map[string]bool{}
	for i := range routes {
		route := &routes[i]
		if len(route.segments) != len(segments) {
			continue
		}
		matches := true
		for j, part := range route.segments {
			if (strings.HasPrefix(part, ":") && segments[j] == "") || (!strings.HasPrefix(part, ":") && part != segments[j]) {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		allowed[route.method] = true
		if route.method == r.Method && (matched == nil || matched.method != r.Method) {
			matched = route
		}
		if matched == nil && r.Method == "HEAD" && route.method == "GET" {
			matched = route
		}
	}
	if len(allowed) == 0 {
		writeHTTPError(w, r, 404, "Not found")
		return
	}
	if matched == nil {
		if allowed["GET"] {
			allowed["HEAD"] = true
		}
		allowed["OPTIONS"] = true
		var methods []string
		for _, method := range httpMethods {
			if allowed[method] {
				methods = append(methods, method)
			}
		}
		w.Header().Set("Allow", strings.Join(methods, ", "))
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
		} else {
			writeHTTPError(w, r, 405, "Method not allowed")
		}
		return
	}
	ctx := &HTTPContext{Request: r, Params: map[string]string{}, Query: map[string]any{}}
	for i, part := range matched.segments {
		if strings.HasPrefix(part, ":") {
			ctx.Params[part[1:]] = decoded[i]
		}
	}
	for key, values := range r.URL.Query() {
		if len(values) == 1 {
			ctx.Query[key] = values[0]
		} else {
			ctx.Query[key] = values
		}
	}
	result, err, presenting := a.invokeHTTP(matched.options, ctx)
	if err != nil {
		var public *Error
		if !presenting && errors.As(err, &public) && public.Code >= 400 && public.Code < 600 {
			writeHTTPError(w, r, public.Code, public.Message)
			return
		}
		slog.Error("HTTP request failed", "error", err)
		writeHTTPError(w, r, 500, "Internal server error")
		return
	}
	status := matched.options.Status
	if response, ok := result.(*http.Response); ok {
		if response.Body != nil {
			defer response.Body.Close()
		}
		for key, values := range response.Header {
			w.Header()[key] = slices.Clone(values)
		}
		w.WriteHeader(response.StatusCode)
		if r.Method != "HEAD" && response.StatusCode != 204 && response.StatusCode != 205 && response.StatusCode != 304 && response.Body != nil {
			_, _ = io.Copy(w, response.Body)
		}
		return
	}
	if status == 0 {
		status = 200
		if result == nil && matched.options.Action == "" {
			status = 204
		}
	}
	if status == 204 || status == 205 || status == 304 {
		w.WriteHeader(status)
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		writeHTTPError(w, r, 500, "Internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		_, _ = w.Write(data)
	}
}

func (a *App) invokeHTTP(options HTTPRoute, ctx *HTTPContext) (result any, err error, presenting bool) {
	badInput := func(err error) error { return &Error{Code: 400, Message: err.Error()} }
	if options.Params != nil {
		if err = options.Params(ctx.Params); err != nil {
			return nil, badInput(err), false
		}
	}
	if options.Query != nil {
		if err = options.Query(ctx.Query); err != nil {
			return nil, badInput(err), false
		}
	}
	readBody := func() error {
		ctx.Body = map[string]any{}
		if ctx.Request.Body == nil {
			return nil
		}
		decoder := json.NewDecoder(ctx.Request.Body)
		if err := decoder.Decode(&ctx.Body); err != nil {
			if errors.Is(err, io.EOF) && ctx.Request.ContentLength == 0 {
				ctx.Body = map[string]any{}
				return nil
			}
			return &Error{Code: 400, Message: "Request body must contain valid JSON"}
		}
		var extra any
		if !errors.Is(decoder.Decode(&extra), io.EOF) {
			return &Error{Code: 400, Message: "Request body must contain valid JSON"}
		}
		return nil
	}
	if options.Body != nil {
		if err = readBody(); err != nil {
			return nil, err, false
		}
		ctx.Body, err = options.Body(ctx.Body)
		if err != nil {
			return nil, badInput(err), false
		}
	}
	if options.Action != "" {
		var input any
		if options.Input != nil {
			input, err = options.Input(ctx)
		} else {
			if options.Body == nil {
				err = readBody()
			}
			input = ctx.Body
		}
		if err == nil {
			err = a.Call(ctx.Request.Context(), a.options.Name, options.Action, input, &result)
		}
	} else {
		result, err = options.Handler(ctx)
	}
	if err != nil {
		return nil, err, false
	}
	if options.Output != nil {
		result, err = options.Output(result, ctx)
	}
	return result, err, true
}

func writeHTTPError(w http.ResponseWriter, r *http.Request, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
	}
}

func (a *App) stopHTTP() error {
	a.httpGate.Lock()
	a.httpClosing = true
	a.httpGate.Unlock()
	var err error
	if a.HTTP != nil {
		err = a.HTTP.server.Shutdown(context.Background())
	}
	a.httpActive.Wait()
	return err
}

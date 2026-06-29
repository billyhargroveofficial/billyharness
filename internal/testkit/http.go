package testkit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type RoundTripFunc func(*http.Request) (*http.Response, error)

func (fn RoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type Route struct {
	Method  string
	Path    string
	Handler http.HandlerFunc
}

func NewRouteServer(t testing.TB, routes ...Route) *httptest.Server {
	t.Helper()
	handlers := make(map[string]http.HandlerFunc, len(routes))
	for _, route := range routes {
		if route.Handler == nil {
			t.Fatalf("nil handler for %s %s", route.Method, route.Path)
		}
		handlers[route.Method+" "+route.Path] = route.Handler
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler := handlers[r.Method+" "+r.URL.Path]
		if handler == nil {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func DecodeJSON(t testing.TB, r *http.Request, out any) bool {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		t.Errorf("decode JSON request: %v", err)
		return false
	}
	return true
}

func WriteJSON(t testing.TB, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode JSON response: %v", err)
	}
}

func WriteJSONLines(t testing.TB, w http.ResponseWriter, values ...any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	for _, value := range values {
		if err := enc.Encode(value); err != nil {
			t.Errorf("encode JSON line response: %v", err)
			return
		}
	}
}

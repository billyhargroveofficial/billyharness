package telegrambot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type telegramAPIHandler func(http.ResponseWriter, *http.Request, map[string]any)

func newTelegramAPIClient(t testing.TB, token string, handlers map[string]telegramAPIHandler) *Client {
	t.Helper()
	server := newTelegramAPIServer(t, handlers)
	return NewClient(ClientOptions{BaseURL: server.URL, Token: token, MinInterval: time.Nanosecond})
}

func newTelegramAPIServer(t testing.TB, handlers map[string]telegramAPIHandler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := telegramMethodFromPath(r.URL.Path)
		handler := handlers[method]
		if handler == nil {
			t.Errorf("unexpected telegram path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode telegram payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		handler(w, r, payload)
	}))
	t.Cleanup(server.Close)
	return server
}

func telegramMethodFromPath(path string) string {
	_, method, ok := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !ok {
		return ""
	}
	return method
}

func writeTelegramResult(tw http.ResponseWriter, result any) {
	tw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(tw).Encode(map[string]any{
		"ok":     true,
		"result": result,
	})
}

func writeTelegramError(tw http.ResponseWriter, status int, description string) {
	tw.Header().Set("Content-Type", "application/json")
	tw.WriteHeader(status)
	_ = json.NewEncoder(tw).Encode(map[string]any{
		"ok":          false,
		"error_code":  status,
		"description": description,
	})
}

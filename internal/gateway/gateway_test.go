package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestGatewaySessionRunStreamsEvents(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov, err := provider.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(cfg, prov, tools.NewRegistry(cfg))

	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.ID == "" {
		t.Fatal("empty session id")
	}

	body := bytes.NewBufferString(`{"prompt":"through gateway"}`)
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/run", body))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	var content strings.Builder
	scanner := bufio.NewScanner(run.Body)
	for scanner.Scan() {
		var event protocol.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == protocol.EventAssistantDelta {
			content.WriteString(event.Data.(string))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if got := content.String(); got != "mock: through gateway" {
		t.Fatalf("content = %q", got)
	}
}

func TestGatewayRunAcceptsModelOverrides(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov, err := provider.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(cfg, prov, tools.NewRegistry(cfg))

	body := bytes.NewBufferString(`{"prompt":"override","model":"mock","thinking":"disabled","reasoning_effort":"high","max_tool_rounds":3}`)
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/run", body))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	if !strings.Contains(run.Body.String(), "mock: override") {
		t.Fatalf("unexpected body=%s", run.Body.String())
	}
}

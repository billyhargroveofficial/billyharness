package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/attachments"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestGatewaySessionInputAdmissionHashesAttachmentMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	store := attachments.DefaultStore()
	ref1, err := store.StoreImageBytes("one.png", gatewayPNGBytes(t, 1, 1), protocol.AttachmentDetailLow)
	if err != nil {
		t.Fatal(err)
	}
	ref2, err := store.StoreImageBytes("two.png", gatewayPNGBytes(t, 2, 1), protocol.AttachmentDetailLow)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	body1, err := json.Marshal(gatewayapi.SessionInputRequest{InputID: "input-attach", Prompt: "look", Attachments: []protocol.AttachmentRef{ref1}})
	if err != nil {
		t.Fatal(err)
	}
	admit := httptest.NewRecorder()
	server.Handler().ServeHTTP(admit, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/inputs", bytes.NewReader(body1)))
	if admit.Code != http.StatusCreated {
		t.Fatalf("admit status = %d body=%s", admit.Code, admit.Body.String())
	}
	duplicate := httptest.NewRecorder()
	server.Handler().ServeHTTP(duplicate, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/inputs", bytes.NewReader(body1)))
	if duplicate.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d body=%s", duplicate.Code, duplicate.Body.String())
	}
	body2, err := json.Marshal(gatewayapi.SessionInputRequest{InputID: "input-attach", Prompt: "look", Attachments: []protocol.AttachmentRef{ref2}})
	if err != nil {
		t.Fatal(err)
	}
	conflict := httptest.NewRecorder()
	server.Handler().ServeHTTP(conflict, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/inputs", bytes.NewReader(body2)))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d body=%s", conflict.Code, conflict.Body.String())
	}

	inputsPath := filepath.Join(storeDir, created.ID, sessionInputsJSONLName)
	raw, err := os.ReadFile(inputsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "data:image") || strings.Contains(string(raw), "base64") {
		t.Fatalf("input inbox leaked image bytes: %s", raw)
	}
	records := readSessionInputRecords(t, inputsPath)
	if len(records) != 1 || len(records[0].Request.Attachments) != 1 || records[0].Request.Attachments[0].ID != ref1.ID {
		t.Fatalf("input records = %#v", records)
	}
}

func TestGatewaySessionRunPersistsImageOnlyAttachmentMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	ref, err := attachments.DefaultStore().StoreImageBytes("screen.png", gatewayPNGBytes(t, 1, 1), "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov := &gatewayScriptedProvider{steps: [][]provider.Event{{{Kind: provider.EventContent, Text: "ok"}, {Kind: provider.EventDone}}}}
	server := NewServerWithOptions(cfg, prov, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: filepath.Join(t.TempDir(), "gateway-sessions")})
	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(RunRequest{InputID: "image-run", Attachments: []protocol.AttachmentRef{ref}})
	if err != nil {
		t.Fatal(err)
	}
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", bytes.NewReader(body)))
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	session, ok := server.session(created.ID)
	if !ok {
		t.Fatal("session missing")
	}
	messages := session.messages()
	if len(messages) < 2 || messages[len(messages)-2].AttachmentCount() != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	status := session.Status()
	if status.AttachmentCount != 1 || status.ImageSubmissions != 1 {
		t.Fatalf("status = %#v", status)
	}
	contextRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(contextRec, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID+"/context", nil))
	if contextRec.Code != http.StatusOK {
		t.Fatalf("context status = %d body=%s", contextRec.Code, contextRec.Body.String())
	}
	var contextResp SessionContextResponse
	if err := json.Unmarshal(contextRec.Body.Bytes(), &contextResp); err != nil {
		t.Fatal(err)
	}
	if contextResp.AttachmentCount != 1 || contextResp.ImageSubmissions != 1 || strings.Contains(contextRec.Body.String(), "data:image") || strings.Contains(contextRec.Body.String(), "base64") {
		t.Fatalf("context response = %#v", contextResp)
	}
}

func TestGatewaySessionRunRejectsStaleAttachmentBeforeProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	store := attachments.DefaultStore()
	ref, err := store.StoreImageBytes("screen.png", gatewayPNGBytes(t, 1, 1), "")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := store.Resolve(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(resolved.Path); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	prov := &gatewayScriptedProvider{steps: [][]provider.Event{{{Kind: provider.EventContent, Text: "should not run"}, {Kind: provider.EventDone}}}}
	server := NewServerWithOptions(cfg, prov, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: filepath.Join(t.TempDir(), "gateway-sessions")})
	create := httptest.NewRecorder()
	server.Handler().ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(RunRequest{InputID: "stale-image", Attachments: []protocol.AttachmentRef{ref}})
	if err != nil {
		t.Fatal(err)
	}
	run := httptest.NewRecorder()
	server.Handler().ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/v1/sessions/"+created.ID+"/run", bytes.NewReader(body)))
	if run.Code != http.StatusBadRequest {
		t.Fatalf("run status = %d body=%s", run.Code, run.Body.String())
	}
	if prov.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", prov.calls)
	}
}

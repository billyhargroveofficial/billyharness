package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

func TestGatewaySessionStoreLoadsLegacySnapshot(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "mock"
	cfg.Model = "mock"
	storeDir := filepath.Join(t.TempDir(), "gateway-sessions")
	if err := writeLegacySnapshot(filepath.Join(storeDir, "legacy-session.json"), storedSession{
		ID:      "legacy-session",
		Created: time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC),
		Updated: time.Date(2026, 6, 28, 12, 1, 0, 0, time.UTC),
		Messages: []protocol.Message{
			{Role: protocol.RoleSystem, Content: "system"},
			{Role: protocol.RoleUser, Content: "old prompt"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions(cfg, provider.Mock{}, tools.NewRegistry(cfg), ServerOptions{SessionStoreDir: storeDir})
	get := httptest.NewRecorder()
	server.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/sessions/legacy-session", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", get.Code, get.Body.String())
	}
	var got struct {
		Messages []protocol.Message `json:"messages"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 2 || got.Messages[1].Content != "old prompt" {
		t.Fatalf("legacy messages = %#v", got.Messages)
	}
	inspection, err := InspectStoredSession(storeDir, "legacy-session")
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Legacy || !inspection.MessageSnapshotReady || inspection.EventReplayReady || inspection.OfflineReplayReady || inspection.MessageCount != 2 {
		t.Fatalf("legacy inspection = %#v", inspection)
	}
	if !storedSessionHasReadiness(inspection.Readiness, storedSessionReadinessMessageSnapshotReady) ||
		!storedSessionHasReadiness(inspection.Readiness, storedSessionReadinessEventReplayMissing) {
		t.Fatalf("legacy readiness = %#v", inspection.Readiness)
	}
}

package mcpstatus

import (
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/mcpclient"
)

func TestFormatShowsTransportCatalogAndDiagnostics(t *testing.T) {
	text := Format(Response{
		Enabled: true,
		Servers: []mcpclient.ServerStatus{{
			Name:           "fake",
			Transport:      "stdio",
			Enabled:        true,
			Connected:      true,
			State:          "connected",
			TransportState: "connected",
			CatalogState:   "catalog_stale",
			ToolCount:      2,
			Diagnostics: []mcpclient.StatusDiagnostic{{
				Code:     "catalog_stale",
				Severity: "warning",
				Message:  "catalog refresh pending",
			}},
		}},
	})
	for _, want := range []string{"transport:connected", "catalog:catalog_stale", "diagnostic catalog_stale/warning: catalog refresh pending"} {
		if !strings.Contains(text, want) {
			t.Fatalf("formatted status missing %q:\n%s", want, text)
		}
	}
}

package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/runstate"
)

type sessionContextEpochAdmission struct {
	Run     *protocol.ContextEpoch
	Current *protocol.ContextEpoch
}

func (s *Server) sessionContextEpochAdmission(ctx context.Context, session *Session, settings runSettings, userMessage protocol.Message) sessionContextEpochAdmission {
	runMessages := append(session.messages(), userMessage)
	runMessages, _ = agent.ReconcileProjectContextMessages(settings.instructions, runMessages)
	currentMessages := agent.InitialMessagesFromSettings(settings.instructions)
	return sessionContextEpochAdmission{
		Run:     s.contextEpochForMessages(ctx, settings, runMessages),
		Current: s.contextEpochForMessages(ctx, settings, currentMessages),
	}
}

func (s *Server) contextEpochForMessages(ctx context.Context, settings runSettings, messages []protocol.Message) *protocol.ContextEpoch {
	var specs []protocol.ToolSpec
	var mcpStatusHash string
	if s != nil && s.registry != nil {
		toolSet := s.registry.SnapshotWithToolPolicyAndCapabilities(ctx, settings.toolPolicy, settings.capabilities)
		specs = toolSet.Specs()
		mcpStatusHash = toolSet.MCPStatusSnapshotHash()
	}
	snapshot := runstate.NewSnapshot(runstate.SnapshotInput{
		Provider:              settings.provider,
		Profile:               settings.profile,
		Runtime:               settings.runtime,
		ToolPolicy:            settings.toolPolicy,
		MCP:                   settings.mcp,
		MCPStatusSnapshotHash: mcpStatusHash,
		DocsIndexHash:         docsIndexHash(settings.instructions),
	}, messages, specs)
	epoch := runstate.CloneContextEpoch(snapshot.ContextEpoch)
	if epoch != nil {
		epoch.Policy = memoryDriftPolicySessionLocked
	}
	return epoch
}

func firstContextEpoch(values ...*protocol.ContextEpoch) *protocol.ContextEpoch {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func cloneContextEpochDrift(drift *protocol.ContextEpochDrift) *protocol.ContextEpochDrift {
	if drift == nil {
		return nil
	}
	cloned := *drift
	cloned.ChangedFields = append([]string(nil), drift.ChangedFields...)
	cloned.Locked = runstate.CloneContextEpoch(drift.Locked)
	cloned.Current = runstate.CloneContextEpoch(drift.Current)
	return &cloned
}

func contextEpochHash(epoch *protocol.ContextEpoch) string {
	if epoch == nil {
		return ""
	}
	return strings.TrimSpace(epoch.Hash)
}

func addContextEpochToRunStarted(event protocol.Event, epoch *protocol.ContextEpoch, drift *protocol.ContextEpochDrift) protocol.Event {
	if epoch == nil && drift == nil {
		return event
	}
	data := mapFromEventData(event.Data)
	if data == nil {
		data = map[string]any{}
	}
	if epoch != nil {
		data["context_epoch"] = runstate.CloneContextEpoch(epoch)
		if epoch.Hash != "" {
			data["context_epoch_hash"] = epoch.Hash
		}
	}
	if drift != nil {
		data["context_epoch_drift"] = cloneContextEpochDrift(drift)
		if drift.Status != "" {
			data["context_epoch_status"] = drift.Status
		}
		if drift.Warning != "" {
			data["context_epoch_warning"] = drift.Warning
		}
	}
	event.Data = data
	return event
}

func mapFromEventData(data any) map[string]any {
	if data == nil {
		return nil
	}
	if m, ok := data.(map[string]any); ok {
		out := make(map[string]any, len(m))
		for key, value := range m {
			out[key] = value
		}
		return out
	}
	body, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil
	}
	return out
}

func docsIndexHash(settings config.InstructionSettings) string {
	roots := append([]string(nil), settings.WorkspaceRoots...)
	for i := range roots {
		roots[i] = filepath.Clean(strings.TrimSpace(roots[i]))
	}
	sort.Strings(roots)
	type docsIndexEntry struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Bytes  int    `json:"bytes,omitempty"`
	}
	var entries []docsIndexEntry
	seen := map[string]bool{}
	for _, root := range roots {
		if root == "" || root == "." || seen[root] {
			continue
		}
		seen[root] = true
		for _, rel := range []string{"llms.txt", filepath.Join("docs", "README.md")} {
			path := filepath.Join(root, rel)
			body, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			sum := sha256.Sum256(body)
			entries = append(entries, docsIndexEntry{
				Path:   filepath.ToSlash(path),
				SHA256: hex.EncodeToString(sum[:]),
				Bytes:  len(body),
			})
		}
	}
	if len(entries) == 0 {
		return ""
	}
	payload, err := json.Marshal(entries)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

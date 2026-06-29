package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type StoredSessionList struct {
	Dir      string                 `json:"dir"`
	Sessions []StoredSessionSummary `json:"sessions"`
	Warnings []string               `json:"warnings,omitempty"`
}

type StoredSessionSummary struct {
	ID                 string                  `json:"id"`
	CreatedAt          time.Time               `json:"created_at,omitempty"`
	UpdatedAt          time.Time               `json:"updated_at,omitempty"`
	MessageCount       int                     `json:"message_count"`
	HistorySeq         int64                   `json:"history_seq,omitempty"`
	EventSeq           int64                   `json:"event_seq,omitempty"`
	LastEvent          string                  `json:"last_event,omitempty"`
	Owner              gatewayapi.SessionOwner `json:"owner,omitempty"`
	Legacy             bool                    `json:"legacy,omitempty"`
	OfflineReplayReady bool                    `json:"offline_replay_ready"`
}

type StoredSessionInspection struct {
	Dir                string                         `json:"dir"`
	SessionID          string                         `json:"session_id"`
	SessionDir         string                         `json:"session_dir,omitempty"`
	Legacy             bool                           `json:"legacy,omitempty"`
	Manifest           StoredSessionManifest          `json:"manifest,omitempty"`
	Files              []StoredSessionFile            `json:"files,omitempty"`
	History            StoredSessionHistoryInspection `json:"history,omitempty"`
	Events             StoredSessionEventsInspection  `json:"events,omitempty"`
	MessageCount       int                            `json:"message_count,omitempty"`
	OfflineReplayReady bool                           `json:"offline_replay_ready"`
	Warnings           []string                       `json:"warnings,omitempty"`
}

type StoredSessionManifest struct {
	SchemaVersion             int                     `json:"schema_version,omitempty"`
	SessionID                 string                  `json:"session_id,omitempty"`
	CreatedAt                 time.Time               `json:"created_at,omitempty"`
	UpdatedAt                 time.Time               `json:"updated_at,omitempty"`
	HistoryJSONL              string                  `json:"history_jsonl,omitempty"`
	EventsJSONL               string                  `json:"events_jsonl,omitempty"`
	SnapshotJSON              string                  `json:"snapshot_json,omitempty"`
	ConfigSnapshotJSON        string                  `json:"config_snapshot_json,omitempty"`
	ModelProviderSnapshotJSON string                  `json:"model_provider_snapshot_json,omitempty"`
	MCPSnapshotJSON           string                  `json:"mcp_snapshot_json,omitempty"`
	HistorySeq                int64                   `json:"history_seq,omitempty"`
	EventSeq                  int64                   `json:"event_seq,omitempty"`
	MessageCount              int                     `json:"message_count,omitempty"`
	Owner                     gatewayapi.SessionOwner `json:"owner,omitempty"`
	HistorySHA256             string                  `json:"history_sha256,omitempty"`
}

type StoredSessionFile struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Bytes  int64  `json:"bytes,omitempty"`
}

type StoredSessionHistoryInspection struct {
	Path          string `json:"path,omitempty"`
	Exists        bool   `json:"exists"`
	Records       int64  `json:"records,omitempty"`
	LastSeq       int64  `json:"last_seq,omitempty"`
	LastKind      string `json:"last_kind,omitempty"`
	MessageCount  int    `json:"message_count,omitempty"`
	HistorySHA256 string `json:"history_sha256,omitempty"`
}

type StoredSessionEventsInspection struct {
	Path                  string         `json:"path,omitempty"`
	Exists                bool           `json:"exists"`
	Records               int            `json:"records,omitempty"`
	LastSeq               int64          `json:"last_seq,omitempty"`
	LastEvent             string         `json:"last_event,omitempty"`
	OutputRefs            int            `json:"output_refs,omitempty"`
	OutputRefsVerified    bool           `json:"output_refs_verified,omitempty"`
	OutputRefBytes        int64          `json:"output_ref_bytes,omitempty"`
	MissingOutputRefs     int            `json:"missing_output_refs,omitempty"`
	OutputRefHashMismatch int            `json:"output_ref_hash_mismatch,omitempty"`
	EventTypes            map[string]int `json:"event_types,omitempty"`
}

func ListStoredSessions(dir string) (StoredSessionList, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		dir = DefaultSessionStoreDir()
	}
	out := StoredSessionList{Dir: dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, err
	}
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if entry.Name() == sessionIndexDirName {
			continue
		}
		id := entry.Name()
		inspection, err := InspectStoredSession(dir, id)
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		out.Sessions = append(out.Sessions, inspectionSummary(inspection))
		seen[inspection.SessionID] = struct{}{}
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if _, ok := seen[id]; ok {
			continue
		}
		inspection, err := InspectStoredSession(dir, id)
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		out.Sessions = append(out.Sessions, inspectionSummary(inspection))
		seen[inspection.SessionID] = struct{}{}
	}
	sort.Slice(out.Sessions, func(i, j int) bool {
		return out.Sessions[i].UpdatedAt.After(out.Sessions[j].UpdatedAt)
	})
	return out, nil
}

func InspectStoredSession(dir, id string) (StoredSessionInspection, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		dir = DefaultSessionStoreDir()
	}
	cleanID, err := cleanSessionID(id)
	if err != nil {
		return StoredSessionInspection{}, err
	}
	sessionDir := filepath.Join(dir, cleanID)
	out := StoredSessionInspection{
		Dir:        dir,
		SessionID:  cleanID,
		SessionDir: sessionDir,
	}
	manifestPath := filepath.Join(sessionDir, sessionManifestName)
	manifest, err := readSessionManifest(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return inspectLegacyStoredSession(dir, cleanID)
		}
		return out, err
	}
	out.Manifest = storedSessionManifest(manifest)
	out.Files = append(out.Files,
		inspectStoredSessionFile("manifest", manifestPath),
		inspectStoredSessionFile("history", filepath.Join(sessionDir, sessionFileName(manifest.HistoryJSONL, sessionHistoryJSONLName))),
		inspectStoredSessionFile("events", filepath.Join(sessionDir, sessionFileName(manifest.EventsJSONL, sessionEventsJSONLName))),
		inspectStoredSessionFile("legacy_snapshot", filepath.Join(dir, sessionFileName(manifest.SnapshotJSON, cleanID+".json"))),
		inspectStoredSessionFile("config_snapshot", filepath.Join(sessionDir, sessionFileName(manifest.ConfigSnapshotJSON, sessionConfigSnapshotName))),
		inspectStoredSessionFile("model_provider_snapshot", filepath.Join(sessionDir, sessionFileName(manifest.ModelProviderSnapshotJSON, sessionModelSnapshotName))),
		inspectStoredSessionFile("mcp_snapshot", filepath.Join(sessionDir, sessionFileName(manifest.MCPSnapshotJSON, sessionMCPSnapshotName))),
	)
	historyPath := filepath.Join(sessionDir, sessionFileName(manifest.HistoryJSONL, sessionHistoryJSONLName))
	history, err := replaySessionHistory(historyPath, cleanID)
	if err != nil {
		return out, err
	}
	out.History = StoredSessionHistoryInspection{
		Path:          historyPath,
		Exists:        fileExists(historyPath),
		Records:       history.lastSeq,
		LastSeq:       history.lastSeq,
		MessageCount:  len(history.messages),
		HistorySHA256: history.historySHA256,
	}
	if history.lastSeq == 1 {
		out.History.LastKind = sessionHistoryCreated
	} else if history.lastSeq > 1 {
		out.History.LastKind = sessionHistorySnapshot
	}
	eventsPath := filepath.Join(sessionDir, sessionFileName(manifest.EventsJSONL, sessionEventsJSONLName))
	events, err := replaySessionEventsAfter(eventsPath, cleanID, 0)
	if err != nil {
		return out, err
	}
	out.Events = inspectSessionEvents(eventsPath, events)
	out.MessageCount = len(history.messages)
	out.OfflineReplayReady = out.History.Exists && out.History.Records > 0
	if !hasExistingFile(out.Files, "config_snapshot") {
		out.Warnings = append(out.Warnings, "config snapshot missing")
	}
	if !hasExistingFile(out.Files, "model_provider_snapshot") {
		out.Warnings = append(out.Warnings, "model/provider snapshot missing")
	}
	if !hasExistingFile(out.Files, "mcp_snapshot") {
		out.Warnings = append(out.Warnings, "MCP snapshot missing")
	}
	return out, nil
}

func inspectLegacyStoredSession(dir, id string) (StoredSessionInspection, error) {
	path := filepath.Join(dir, id+".json")
	file := inspectStoredSessionFile("legacy_snapshot", path)
	if !file.Exists {
		return StoredSessionInspection{Dir: dir, SessionID: id, Files: []StoredSessionFile{file}}, os.ErrNotExist
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return StoredSessionInspection{}, err
	}
	var record storedSession
	if err := json.Unmarshal(bytes, &record); err != nil {
		return StoredSessionInspection{}, err
	}
	out := StoredSessionInspection{
		Dir:                dir,
		SessionID:          id,
		Legacy:             true,
		Files:              []StoredSessionFile{file},
		MessageCount:       len(record.Messages),
		OfflineReplayReady: len(record.Messages) > 0,
		Warnings:           []string{"legacy snapshot only; JSONL manifest/history/events missing"},
	}
	out.History = StoredSessionHistoryInspection{
		Path:         path,
		Exists:       true,
		Records:      1,
		LastSeq:      1,
		LastKind:     "legacy.snapshot",
		MessageCount: len(record.Messages),
	}
	out.Manifest = StoredSessionManifest{
		SessionID:    id,
		CreatedAt:    record.Created,
		UpdatedAt:    record.Updated,
		MessageCount: len(record.Messages),
		SnapshotJSON: filepath.Base(path),
	}
	return out, nil
}

func inspectStoredSessionFile(name, path string) StoredSessionFile {
	file := StoredSessionFile{Name: name, Path: filepath.Clean(path)}
	info, err := os.Stat(file.Path)
	if err != nil || info.IsDir() {
		return file
	}
	file.Exists = true
	file.Bytes = info.Size()
	return file
}

func inspectSessionEvents(path string, events []protocol.Event) StoredSessionEventsInspection {
	out := StoredSessionEventsInspection{
		Path:       path,
		Exists:     fileExists(path),
		Records:    len(events),
		EventTypes: map[string]int{},
	}
	for _, event := range events {
		if event.Seq > out.LastSeq {
			out.LastSeq = event.Seq
			out.LastEvent = string(event.Type)
		}
		if event.Type != "" {
			out.EventTypes[string(event.Type)]++
		}
		if event.Type == protocol.EventToolOutputRefCreated {
			ref := outputRefFromEvent(event)
			out.OutputRefs++
			if ref.OutputRef == "" {
				out.MissingOutputRefs++
				continue
			}
			info, err := os.Stat(ref.OutputRef)
			if err != nil || info.IsDir() {
				out.MissingOutputRefs++
				continue
			}
			out.OutputRefBytes += info.Size()
			if ref.Bytes > 0 && info.Size() != ref.Bytes {
				out.OutputRefHashMismatch++
				continue
			}
			if ref.SHA256 != "" {
				ok, err := fileSHA256Matches(ref.OutputRef, ref.SHA256)
				if err != nil || !ok {
					out.OutputRefHashMismatch++
				}
			}
		}
	}
	out.OutputRefsVerified = out.OutputRefs > 0 && out.MissingOutputRefs == 0 && out.OutputRefHashMismatch == 0
	if len(out.EventTypes) == 0 {
		out.EventTypes = nil
	}
	return out
}

type outputRefEventData struct {
	OutputRef string `json:"output_ref"`
	SHA256    string `json:"output_ref_sha256"`
	Bytes     int64  `json:"output_ref_bytes"`
}

func outputRefFromEvent(event protocol.Event) outputRefEventData {
	body, err := json.Marshal(event.Data)
	if err != nil {
		return outputRefEventData{}
	}
	var out outputRefEventData
	_ = json.Unmarshal(body, &out)
	return out
}

func fileSHA256Matches(path, want string) (bool, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(bytes)
	return strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(want)), nil
}

func storedSessionManifest(manifest sessionManifest) StoredSessionManifest {
	return StoredSessionManifest{
		SchemaVersion:             manifest.SchemaVersion,
		SessionID:                 manifest.SessionID,
		CreatedAt:                 manifest.CreatedAt,
		UpdatedAt:                 manifest.UpdatedAt,
		HistoryJSONL:              manifest.HistoryJSONL,
		EventsJSONL:               manifest.EventsJSONL,
		SnapshotJSON:              manifest.SnapshotJSON,
		ConfigSnapshotJSON:        manifest.ConfigSnapshotJSON,
		ModelProviderSnapshotJSON: manifest.ModelProviderSnapshotJSON,
		MCPSnapshotJSON:           manifest.MCPSnapshotJSON,
		HistorySeq:                manifest.HistorySeq,
		EventSeq:                  manifest.EventSeq,
		MessageCount:              manifest.MessageCount,
		Owner:                     manifest.Owner,
		HistorySHA256:             manifest.HistorySHA256,
	}
}

func inspectionSummary(inspection StoredSessionInspection) StoredSessionSummary {
	summary := StoredSessionSummary{
		ID:                 inspection.SessionID,
		CreatedAt:          inspection.Manifest.CreatedAt,
		UpdatedAt:          inspection.Manifest.UpdatedAt,
		MessageCount:       inspection.MessageCount,
		HistorySeq:         inspection.History.LastSeq,
		EventSeq:           inspection.Events.LastSeq,
		LastEvent:          inspection.Events.LastEvent,
		Owner:              inspection.Manifest.Owner,
		Legacy:             inspection.Legacy,
		OfflineReplayReady: inspection.OfflineReplayReady,
	}
	if summary.UpdatedAt.IsZero() {
		summary.UpdatedAt = summary.CreatedAt
	}
	return summary
}

func hasExistingFile(files []StoredSessionFile, name string) bool {
	for _, file := range files {
		if file.Name == name {
			return file.Exists
		}
	}
	return false
}

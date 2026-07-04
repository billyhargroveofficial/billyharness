package clientux

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

const TUIDebugSnapshotSchemaVersion = 1

type TUIDebugSnapshot struct {
	SchemaVersion int                `json:"schema_version"`
	Session       TUIDebugSession    `json:"session"`
	Runtime       TUIDebugRuntime    `json:"runtime"`
	Stream        TUIDebugStream     `json:"stream"`
	Projector     TUIDebugProjector  `json:"projector"`
	Viewport      TUIDebugViewport   `json:"viewport"`
	Selection     TUIDebugSelection  `json:"selection"`
	Transcript    TUIDebugTranscript `json:"transcript"`
	Export        TUIDebugExport     `json:"export"`
	Stale         TUIDebugStale      `json:"stale"`
	Hints         []string           `json:"hints,omitempty"`
	BlockKinds    map[string]int     `json:"block_kinds,omitempty"`
	CellTypes     map[string]int     `json:"cell_types,omitempty"`
	Metadata      map[string]string  `json:"metadata,omitempty"`
}

type TUIDebugSession struct {
	LocalChatID         string `json:"local_chat_id,omitempty"`
	LocalTitleBytes     int    `json:"local_title_bytes,omitempty"`
	LocalTitleHash      string `json:"local_title_hash,omitempty"`
	GatewaySessionID    string `json:"gateway_session_id,omitempty"`
	GatewayURL          string `json:"gateway_url,omitempty"`
	LastGatewayEventSeq int64  `json:"last_gateway_event_seq,omitempty"`
	SettingsPath        string `json:"settings_path,omitempty"`
	SessionsDir         string `json:"sessions_dir,omitempty"`
}

type TUIDebugRuntime struct {
	Mode            string `json:"mode"`
	Provider        string `json:"provider,omitempty"`
	SelectedModel   string `json:"selected_model,omitempty"`
	ActiveModel     string `json:"active_model,omitempty"`
	Profile         string `json:"profile,omitempty"`
	AccessMode      string `json:"access_mode,omitempty"`
	ReasoningKind   string `json:"reasoning_kind,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	Busy            bool   `json:"busy"`
	Status          string `json:"status,omitempty"`
	Error           string `json:"error,omitempty"`
}

type TUIDebugStream struct {
	PendingEvents    int  `json:"pending_events"`
	BatchScheduled   bool `json:"batch_scheduled"`
	ChannelBuffered  int  `json:"channel_buffered"`
	ChannelCapacity  int  `json:"channel_capacity"`
	PendingUserInput bool `json:"pending_user_input"`
}

type TUIDebugProjector struct {
	Available         bool   `json:"available"`
	RunState          string `json:"run_state,omitempty"`
	LastSeq           int64  `json:"last_seq,omitempty"`
	SeqGapAfter       int64  `json:"seq_gap_after,omitempty"`
	SeqGapGot         int64  `json:"seq_gap_got,omitempty"`
	ModelCalls        int    `json:"model_calls"`
	ToolCalls         int    `json:"tool_calls"`
	TrackedTools      int    `json:"tracked_tools"`
	ContextThresholds int    `json:"context_thresholds"`
	TurnChanges       int    `json:"turn_changes"`
	AssistantBytes    int    `json:"assistant_bytes"`
	ReasoningBytes    int    `json:"reasoning_bytes"`
	ErrorCount        int    `json:"error_count"`
	LastError         string `json:"last_error,omitempty"`
}

type TUIDebugViewport struct {
	AppWidth     int  `json:"app_width,omitempty"`
	AppHeight    int  `json:"app_height,omitempty"`
	Width        int  `json:"width,omitempty"`
	Height       int  `json:"height,omitempty"`
	XOffset      int  `json:"x_offset,omitempty"`
	YOffset      int  `json:"y_offset,omitempty"`
	TotalLines   int  `json:"total_lines,omitempty"`
	VisibleLines int  `json:"visible_lines,omitempty"`
	AtBottom     bool `json:"at_bottom"`
	FollowOutput bool `json:"follow_output"`
	ReflowCount  int  `json:"reflow_count,omitempty"`
}

type TUIDebugSelection struct {
	Active        bool          `json:"active"`
	Selecting     bool          `json:"selecting"`
	Start         TUIDebugPoint `json:"start"`
	End           TUIDebugPoint `json:"end"`
	ByteStart     int           `json:"byte_start,omitempty"`
	ByteEnd       int           `json:"byte_end,omitempty"`
	SelectedBytes int           `json:"selected_bytes,omitempty"`
	SelectedRunes int           `json:"selected_runes,omitempty"`
	SelectedHash  string        `json:"selected_hash,omitempty"`
}

type TUIDebugPoint struct {
	Row int `json:"row"`
	Col int `json:"col"`
}

type TUIDebugTranscript struct {
	Blocks              int    `json:"blocks"`
	SelectedIndex       int    `json:"selected_index"`
	SelectedCellID      string `json:"selected_cell_id,omitempty"`
	SelectedKind        string `json:"selected_kind,omitempty"`
	SelectedCellType    string `json:"selected_cell_type,omitempty"`
	SelectedCallID      string `json:"selected_call_id,omitempty"`
	SelectedStepID      string `json:"selected_step_id,omitempty"`
	SelectedToolName    string `json:"selected_tool_name,omitempty"`
	ViewportBytes       int    `json:"viewport_bytes,omitempty"`
	ViewportHash        string `json:"viewport_hash,omitempty"`
	SelectableLineCount int    `json:"selectable_line_count,omitempty"`
	CacheEntries        int    `json:"cache_entries,omitempty"`
	CollapsedEntries    int    `json:"collapsed_entries,omitempty"`
	Stale               bool   `json:"stale"`
}

type TUIDebugExport struct {
	Mode      string `json:"mode,omitempty"`
	Target    string `json:"target,omitempty"`
	RawBytes  int    `json:"raw_bytes,omitempty"`
	RawHash   string `json:"raw_hash,omitempty"`
	RichBytes int    `json:"rich_bytes,omitempty"`
	RichHash  string `json:"rich_hash,omitempty"`
}

type TUIDebugStale struct {
	TranscriptProjector bool `json:"transcript_projector"`
	FileSearchActive    bool `json:"file_search_active"`
	FileSearchError     bool `json:"file_search_error"`
	SlashDismissed      bool `json:"slash_dismissed"`
}

func RedactTUIDebugSnapshot(snapshot TUIDebugSnapshot, extra ...string) TUIDebugSnapshot {
	body, err := secrets.RedactJSON(snapshot, extra...)
	if err != nil {
		snapshot.Runtime.Error = secrets.Redact(snapshot.Runtime.Error, extra...)
		return snapshot
	}
	var out TUIDebugSnapshot
	if err := json.Unmarshal(body, &out); err != nil {
		snapshot.Runtime.Error = secrets.Redact(snapshot.Runtime.Error, extra...)
		return snapshot
	}
	return out
}

func FormatTUIDebugSnapshot(snapshot TUIDebugSnapshot) string {
	snapshot = RedactTUIDebugSnapshot(snapshot)
	var b strings.Builder
	fmt.Fprintf(&b, "schema: %d\n", snapshot.SchemaVersion)
	fmt.Fprintf(&b, "session: local=%s gateway=%s last_seq=%d mode=%s title=%d:%s\n",
		debugValue(snapshot.Session.LocalChatID),
		debugValue(snapshot.Session.GatewaySessionID),
		snapshot.Session.LastGatewayEventSeq,
		debugValue(snapshot.Runtime.Mode),
		snapshot.Session.LocalTitleBytes,
		debugValue(snapshot.Session.LocalTitleHash),
	)
	fmt.Fprintf(&b, "runtime: provider=%s selected_model=%s active_model=%s profile=%s access_mode=%s busy=%t status=%s\n",
		debugValue(snapshot.Runtime.Provider),
		debugValue(snapshot.Runtime.SelectedModel),
		debugValue(snapshot.Runtime.ActiveModel),
		debugValue(snapshot.Runtime.Profile),
		debugValue(snapshot.Runtime.AccessMode),
		snapshot.Runtime.Busy,
		debugValue(snapshot.Runtime.Status),
	)
	if strings.TrimSpace(snapshot.Runtime.Error) != "" {
		fmt.Fprintf(&b, "runtime error: %s\n", strings.TrimSpace(snapshot.Runtime.Error))
	}
	fmt.Fprintf(&b, "stream: pending=%d scheduled=%t channel=%d/%d pending_user_input=%t\n",
		snapshot.Stream.PendingEvents,
		snapshot.Stream.BatchScheduled,
		snapshot.Stream.ChannelBuffered,
		snapshot.Stream.ChannelCapacity,
		snapshot.Stream.PendingUserInput,
	)
	fmt.Fprintf(&b, "projector: available=%t run_state=%s last_seq=%d tools=%d tracked=%d model_calls=%d errors=%d\n",
		snapshot.Projector.Available,
		debugValue(snapshot.Projector.RunState),
		snapshot.Projector.LastSeq,
		snapshot.Projector.ToolCalls,
		snapshot.Projector.TrackedTools,
		snapshot.Projector.ModelCalls,
		snapshot.Projector.ErrorCount,
	)
	if strings.TrimSpace(snapshot.Projector.LastError) != "" {
		fmt.Fprintf(&b, "projector error: %s\n", strings.TrimSpace(snapshot.Projector.LastError))
	}
	if snapshot.Projector.SeqGapGot > 0 {
		fmt.Fprintf(&b, "projector seq gap: after=%d got=%d\n", snapshot.Projector.SeqGapAfter, snapshot.Projector.SeqGapGot)
	}
	fmt.Fprintf(&b, "viewport: app=%dx%d viewport=%dx%d offset=%d,%d lines=%d visible=%d at_bottom=%t follow=%t reflows=%d\n",
		snapshot.Viewport.AppWidth,
		snapshot.Viewport.AppHeight,
		snapshot.Viewport.Width,
		snapshot.Viewport.Height,
		snapshot.Viewport.XOffset,
		snapshot.Viewport.YOffset,
		snapshot.Viewport.TotalLines,
		snapshot.Viewport.VisibleLines,
		snapshot.Viewport.AtBottom,
		snapshot.Viewport.FollowOutput,
		snapshot.Viewport.ReflowCount,
	)
	fmt.Fprintf(&b, "selection: active=%t selecting=%t start=%d,%d end=%d,%d bytes=%d:%d selected_bytes=%d selected_runes=%d hash=%s\n",
		snapshot.Selection.Active,
		snapshot.Selection.Selecting,
		snapshot.Selection.Start.Row,
		snapshot.Selection.Start.Col,
		snapshot.Selection.End.Row,
		snapshot.Selection.End.Col,
		snapshot.Selection.ByteStart,
		snapshot.Selection.ByteEnd,
		snapshot.Selection.SelectedBytes,
		snapshot.Selection.SelectedRunes,
		debugValue(snapshot.Selection.SelectedHash),
	)
	fmt.Fprintf(&b, "transcript: blocks=%d selected=%d id=%s cell=%s/%s call_id=%s step_id=%s tool=%s viewport_hash=%s viewport_bytes=%d cache=%d collapsed=%d stale=%t\n",
		snapshot.Transcript.Blocks,
		snapshot.Transcript.SelectedIndex,
		debugValue(snapshot.Transcript.SelectedCellID),
		debugValue(snapshot.Transcript.SelectedKind),
		debugValue(snapshot.Transcript.SelectedCellType),
		debugValue(snapshot.Transcript.SelectedCallID),
		debugValue(snapshot.Transcript.SelectedStepID),
		debugValue(snapshot.Transcript.SelectedToolName),
		debugValue(snapshot.Transcript.ViewportHash),
		snapshot.Transcript.ViewportBytes,
		snapshot.Transcript.CacheEntries,
		snapshot.Transcript.CollapsedEntries,
		snapshot.Transcript.Stale,
	)
	fmt.Fprintf(&b, "export: mode=%s target=%s raw=%d:%s rich=%d:%s\n",
		debugValue(snapshot.Export.Mode),
		debugValue(snapshot.Export.Target),
		snapshot.Export.RawBytes,
		debugValue(snapshot.Export.RawHash),
		snapshot.Export.RichBytes,
		debugValue(snapshot.Export.RichHash),
	)
	fmt.Fprintf(&b, "stale: transcript_projector=%t file_search_active=%t file_search_error=%t slash_dismissed=%t\n",
		snapshot.Stale.TranscriptProjector,
		snapshot.Stale.FileSearchActive,
		snapshot.Stale.FileSearchError,
		snapshot.Stale.SlashDismissed,
	)
	if len(snapshot.BlockKinds) > 0 {
		fmt.Fprintf(&b, "block kinds: %s\n", formatDebugCounts(snapshot.BlockKinds))
	}
	if len(snapshot.CellTypes) > 0 {
		fmt.Fprintf(&b, "cell types: %s\n", formatDebugCounts(snapshot.CellTypes))
	}
	for _, hint := range snapshot.Hints {
		if strings.TrimSpace(hint) != "" {
			fmt.Fprintf(&b, "hint: %s\n", strings.TrimSpace(hint))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func DebugHash(text string) string {
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func debugValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}

func formatDebugCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", debugValue(key), counts[key]))
	}
	return strings.Join(parts, ",")
}

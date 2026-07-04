package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
	sessionpkg "github.com/billyhargroveofficial/billyharness/internal/session"
	tuitranscript "github.com/billyhargroveofficial/billyharness/internal/tui/transcript"
)

func sessionsCmd(args []string) error {
	return sessionsCommand(args, os.Stdout)
}

func sessionsCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return sessionsListCommand(nil, out)
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "help", "-h", "--help":
		printSessionsUsage(out)
		return nil
	case "list", "ls":
		return sessionsListCommand(args[1:], out)
	case "inspect", "show":
		return sessionsInspectCommand(args[1:], out)
	case "debug":
		return sessionsDebugCommand(args[1:], out)
	case "context", "ctx":
		return sessionsContextCommand(args[1:], out)
	case "export":
		return sessionsExportCommand(args[1:], out)
	case "index":
		return sessionsIndexCommand(args[1:], out)
	case "search":
		return sessionsSearchCommand(args[1:], out)
	case "tools":
		return sessionsToolsCommand(args[1:], out)
	case "errors":
		return sessionsErrorsCommand(args[1:], out)
	case "usage":
		return sessionsUsageCommand(args[1:], out)
	case "runs":
		return sessionsRunsCommand(args[1:], out)
	case "import":
		return sessionsImportCommand(args[1:], out)
	default:
		return fmt.Errorf("unknown sessions command %q", args[0])
	}
}

func printSessionsUsage(out io.Writer) {
	fmt.Fprintln(out, "Usage: fast-agent-harness sessions <command> [args]")
	fmt.Fprintln(out, "  sessions list [-dir DIR] [-json]")
	fmt.Fprintln(out, "  sessions inspect [-dir DIR] [-json] SESSION_ID")
	fmt.Fprintln(out, "  sessions debug [-gateway URL] [-json] SESSION_ID")
	fmt.Fprintln(out, "  sessions context [-dir DIR] [-json] SESSION_ID")
	fmt.Fprintln(out, "  sessions export [-dir DIR] [-mode raw|rich] [-json] SESSION_ID")
	fmt.Fprintln(out, "  sessions index rebuild|show|delete [-dir DIR] [-json]")
	fmt.Fprintln(out, "  sessions search|tools|errors|runs|usage ...")
}

func sessionsListCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions list", flag.ExitOnError)
	dir := fs.String("dir", gateway.DefaultSessionStoreDir(), "gateway session store directory")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	list, err := gateway.ListStoredSessions(*dir)
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(list)
	}
	fmt.Fprintf(out, "billyharness sessions\n")
	fmt.Fprintf(out, "dir: %s\n", list.Dir)
	if len(list.Sessions) == 0 {
		fmt.Fprintln(out, "sessions: none")
	}
	for _, session := range list.Sessions {
		marker := "jsonl"
		if session.Legacy {
			marker = "legacy"
		}
		fmt.Fprintf(out, "- %s %s messages=%d history=%d events=%d last=%s snapshot=%t event_replay=%t replay=%t\n",
			session.ID,
			marker,
			session.MessageCount,
			session.HistorySeq,
			session.EventSeq,
			emptyDash(session.LastEvent),
			session.MessageSnapshotReady,
			session.EventReplayReady,
			session.OfflineReplayReady,
		)
	}
	for _, warning := range list.Warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
	return nil
}

func sessionsDebugCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions debug", flag.ExitOnError)
	gatewayURL := fs.String("gateway", "", "gateway base URL")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: sessions debug [-gateway URL] [-json] SESSION_ID")
	}
	baseURL := normalizeGatewayURL(*gatewayURL)
	if baseURL == "" {
		cfg, err := resolveRuntimeConfig()
		if err != nil {
			return err
		}
		discovered, ok := discoverGatewayURL(context.Background(), cfg)
		if !ok {
			return fmt.Errorf("gateway unavailable: %s", gatewayclient.UnavailableHint(normalizeGatewayURL(cfg.GatewayAddr)))
		}
		baseURL = discovered
	}
	raw, err := gatewayclient.New(baseURL).SessionInspectRaw(context.Background(), fs.Arg(0))
	if err != nil {
		return err
	}
	var inspection gateway.StoredSessionInspection
	if err := json.Unmarshal(raw, &inspection); err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(inspection)
	}
	printSessionInspection(out, inspection)
	return nil
}

func sessionsInspectCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions inspect", flag.ExitOnError)
	dir := fs.String("dir", gateway.DefaultSessionStoreDir(), "gateway session store directory")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: sessions inspect [-dir DIR] [-json] SESSION_ID")
	}
	inspection, err := gateway.InspectStoredSession(*dir, fs.Arg(0))
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(inspection)
	}
	printSessionInspection(out, inspection)
	return nil
}

func sessionsContextCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions context", flag.ExitOnError)
	dir := fs.String("dir", gateway.DefaultSessionStoreDir(), "gateway session store directory")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: sessions context [-dir DIR] [-json] SESSION_ID")
	}
	cfg, err := resolveRuntimeConfig()
	if err != nil {
		return err
	}
	resp, err := gateway.StoredSessionContext(*dir, fs.Arg(0), cfg.RuntimeLimits())
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}
	fmt.Fprintln(out, gatewayclient.FormatSessionContext(resp))
	return nil
}

func sessionsExportCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions export", flag.ExitOnError)
	dir := fs.String("dir", gateway.DefaultSessionStoreDir(), "gateway session store directory")
	modeFlag := fs.String("mode", tuitranscript.ExportModeRich, "transcript mode: raw or rich")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: sessions export [-dir DIR] [-mode raw|rich] [-json] SESSION_ID")
	}
	mode := tuitranscript.NormalizeExportMode(*modeFlag)
	if mode == "" {
		return fmt.Errorf("unknown transcript mode %q", *modeFlag)
	}
	data, err := gateway.LoadStoredSessionTranscript(*dir, fs.Arg(0))
	if err != nil {
		return err
	}
	text := secrets.Redact(tuitranscript.FormatSession(data.Messages, data.Events, mode))
	if *jsonOut {
		body, err := secrets.RedactJSONIndent(struct {
			Mode       string                          `json:"mode"`
			Text       string                          `json:"text"`
			Transcript gateway.StoredSessionTranscript `json:"transcript"`
		}{
			Mode:       mode,
			Text:       text,
			Transcript: data,
		}, "", "  ")
		if err != nil {
			return err
		}
		body = append(body, '\n')
		_, err = out.Write(body)
		return err
	}
	if text != "" {
		fmt.Fprintln(out, text)
	}
	return nil
}

func sessionsIndexCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: sessions index rebuild|show|delete [-dir DIR] [-json]")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "rebuild":
		return sessionsIndexRebuildCommand(args[1:], out)
	case "show":
		return sessionsIndexShowCommand(args[1:], out)
	case "delete", "rm":
		return sessionsIndexDeleteCommand(args[1:], out)
	default:
		return fmt.Errorf("unknown sessions index command %q", args[0])
	}
}

func sessionsIndexRebuildCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions index rebuild", flag.ExitOnError)
	dir := fs.String("dir", gateway.DefaultSessionStoreDir(), "gateway session store directory")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	index, err := gateway.RebuildStoredSessionIndex(*dir)
	if err != nil {
		return err
	}
	diagnostics, err := gateway.RebuildStoredSessionDiagnosticsIndex(*dir)
	if err != nil {
		return err
	}
	if err := printSessionIndex(out, index, *jsonOut); err != nil {
		return err
	}
	if !*jsonOut {
		fmt.Fprintf(out, "diagnostics: text=%d tools=%d errors=%d runs=%d usage=%d\n",
			diagnostics.TextRowCount,
			diagnostics.ToolRowCount,
			diagnostics.ErrorRowCount,
			diagnostics.RunRowCount,
			diagnostics.UsageRowCount,
		)
	}
	return nil
}

func sessionsIndexShowCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions index show", flag.ExitOnError)
	dir := fs.String("dir", gateway.DefaultSessionStoreDir(), "gateway session store directory")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	index, err := gateway.ReadStoredSessionIndex(*dir)
	if err != nil {
		return err
	}
	return printSessionIndex(out, index, *jsonOut)
}

func sessionsIndexDeleteCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions index delete", flag.ExitOnError)
	dir := fs.String("dir", gateway.DefaultSessionStoreDir(), "gateway session store directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := gateway.DeleteStoredSessionIndex(*dir); err != nil {
		return err
	}
	fmt.Fprintln(out, "deleted session index")
	return nil
}

type sessionsDiagnosticsOptions struct {
	dir       string
	jsonOut   bool
	limit     int
	sessionID string
}

type sessionsQueryResult[T any] struct {
	Dir       string    `json:"dir"`
	BuiltAt   time.Time `json:"built_at"`
	Query     string    `json:"query,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Limit     int       `json:"limit"`
	Total     int       `json:"total"`
	Rows      []T       `json:"rows"`
}

func sessionsSearchCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions search", flag.ExitOnError)
	opts := addSessionsDiagnosticsFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: sessions search [-dir DIR] [-session SESSION_ID] [-limit N] [-json] QUERY")
	}
	query := strings.TrimSpace(fs.Arg(0))
	if query == "" {
		return fmt.Errorf("sessions search query cannot be empty")
	}
	index, err := readSessionsDiagnosticsIndex(opts.dir)
	if err != nil {
		return err
	}
	queryLower := strings.ToLower(query)
	var rows []gateway.StoredSessionTextRow
	for _, row := range index.TextRows {
		if !matchesSession(row.SessionID, opts.sessionID) {
			continue
		}
		if strings.Contains(strings.ToLower(row.Text), queryLower) {
			rows = append(rows, row)
		}
	}
	total := len(rows)
	rows = limitRows(rows, opts.limit)
	if opts.jsonOut {
		return printJSON(out, sessionsQueryResult[gateway.StoredSessionTextRow]{
			Dir:       index.Dir,
			BuiltAt:   index.BuiltAt,
			Query:     query,
			SessionID: opts.sessionID,
			Limit:     normalizeLimit(opts.limit),
			Total:     total,
			Rows:      rows,
		})
	}
	fmt.Fprintln(out, "billyharness session search")
	fmt.Fprintf(out, "dir: %s\n", index.Dir)
	fmt.Fprintf(out, "query: %s\n", query)
	printSessionFilter(out, opts.sessionID, opts.limit, total)
	if len(rows) == 0 {
		fmt.Fprintln(out, "matches: none")
		return nil
	}
	for _, row := range rows {
		fmt.Fprintf(out, "- %s message=%d role=%s bytes=%d%s text=%s\n",
			row.SessionID,
			row.MessageIndex,
			row.Role,
			row.TextBytes,
			truncatedMarker(row.Truncated),
			snippet(row.Text, 180),
		)
	}
	return nil
}

func sessionsToolsCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions tools", flag.ExitOnError)
	opts := addSessionsDiagnosticsFlags(fs)
	name := fs.String("name", "", "filter by tool name")
	status := fs.String("status", "", "filter by status")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: sessions tools [-dir DIR] [-session SESSION_ID] [-name TOOL] [-status STATUS] [-limit N] [-json]")
	}
	index, err := readSessionsDiagnosticsIndex(opts.dir)
	if err != nil {
		return err
	}
	var rows []gateway.StoredSessionToolRow
	for _, row := range index.ToolRows {
		if !matchesSession(row.SessionID, opts.sessionID) ||
			!matchesOptional(row.Name, *name) ||
			!matchesOptional(row.Status, *status) {
			continue
		}
		rows = append(rows, row)
	}
	total := len(rows)
	rows = limitRows(rows, opts.limit)
	if opts.jsonOut {
		return printJSON(out, sessionsQueryResult[gateway.StoredSessionToolRow]{
			Dir:       index.Dir,
			BuiltAt:   index.BuiltAt,
			SessionID: opts.sessionID,
			Limit:     normalizeLimit(opts.limit),
			Total:     total,
			Rows:      rows,
		})
	}
	fmt.Fprintln(out, "billyharness session tools")
	fmt.Fprintf(out, "dir: %s\n", index.Dir)
	printSessionFilter(out, opts.sessionID, opts.limit, total)
	if len(rows) == 0 {
		fmt.Fprintln(out, "tools: none")
		return nil
	}
	for _, row := range rows {
		fmt.Fprintf(out, "- %s seq=%d tool=%s status=%s call=%s attempt=%s duration_ms=%d output_ref=%s error=%s args=%s\n",
			row.SessionID,
			row.Seq,
			emptyDash(row.Name),
			emptyDash(row.Status),
			emptyDash(row.CallID),
			emptyDash(row.AttemptID),
			row.DurationMS,
			emptyDash(row.OutputRefID),
			emptyDash(snippet(row.Error, 120)),
			emptyDash(snippet(row.ArgsPreview, 120)),
		)
	}
	return nil
}

func sessionsErrorsCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions errors", flag.ExitOnError)
	opts := addSessionsDiagnosticsFlags(fs)
	query := fs.String("query", "", "filter by error text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: sessions errors [-dir DIR] [-session SESSION_ID] [-query TEXT] [-limit N] [-json]")
	}
	index, err := readSessionsDiagnosticsIndex(opts.dir)
	if err != nil {
		return err
	}
	var rows []gateway.StoredSessionErrorRow
	for _, row := range index.ErrorRows {
		if !matchesSession(row.SessionID, opts.sessionID) || !matchesOptional(row.Error, *query) {
			continue
		}
		rows = append(rows, row)
	}
	total := len(rows)
	rows = limitRows(rows, opts.limit)
	if opts.jsonOut {
		return printJSON(out, sessionsQueryResult[gateway.StoredSessionErrorRow]{
			Dir:       index.Dir,
			BuiltAt:   index.BuiltAt,
			Query:     *query,
			SessionID: opts.sessionID,
			Limit:     normalizeLimit(opts.limit),
			Total:     total,
			Rows:      rows,
		})
	}
	fmt.Fprintln(out, "billyharness session errors")
	fmt.Fprintf(out, "dir: %s\n", index.Dir)
	printSessionFilter(out, opts.sessionID, opts.limit, total)
	if len(rows) == 0 {
		fmt.Fprintln(out, "errors: none")
		return nil
	}
	for _, row := range rows {
		fmt.Fprintf(out, "- %s seq=%d event=%s name=%s status=%s call=%s error=%s\n",
			row.SessionID,
			row.Seq,
			emptyDash(row.EventType),
			emptyDash(row.Name),
			emptyDash(row.Status),
			emptyDash(row.CallID),
			snippet(row.Error, 180),
		)
	}
	return nil
}

func sessionsUsageCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions usage", flag.ExitOnError)
	opts := addSessionsDiagnosticsFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: sessions usage [-dir DIR] [-session SESSION_ID] [-limit N] [-json]")
	}
	index, err := readSessionsDiagnosticsIndex(opts.dir)
	if err != nil {
		return err
	}
	var rows []gateway.StoredSessionUsageRow
	for _, row := range index.UsageRows {
		if matchesSession(row.SessionID, opts.sessionID) {
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return usageTotal(rows[i]) > usageTotal(rows[j])
	})
	total := len(rows)
	rows = limitRows(rows, opts.limit)
	if opts.jsonOut {
		return printJSON(out, sessionsQueryResult[gateway.StoredSessionUsageRow]{
			Dir:       index.Dir,
			BuiltAt:   index.BuiltAt,
			SessionID: opts.sessionID,
			Limit:     normalizeLimit(opts.limit),
			Total:     total,
			Rows:      rows,
		})
	}
	fmt.Fprintln(out, "billyharness session usage")
	fmt.Fprintf(out, "dir: %s\n", index.Dir)
	printSessionFilter(out, opts.sessionID, opts.limit, total)
	if len(rows) == 0 {
		fmt.Fprintln(out, "usage: none")
		return nil
	}
	for _, row := range rows {
		fmt.Fprintf(out, "- %s run=%s status=%s total=%d input=%d output=%d cache_hit=%d cache_miss=%d reasoning=%d model_calls=%d tool_calls=%d\n",
			row.SessionID,
			emptyDash(row.RunID),
			emptyDash(row.Status),
			usageTotal(row),
			row.InputTokens,
			row.OutputTokens,
			row.CacheHitTokens,
			row.CacheMissTokens,
			row.ReasoningTokens,
			row.ModelCalls,
			row.ToolCalls,
		)
	}
	return nil
}

func sessionsRunsCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions runs", flag.ExitOnError)
	opts := addSessionsDiagnosticsFlags(fs)
	status := fs.String("status", "", "filter by run status")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: sessions runs [-dir DIR] [-session SESSION_ID] [-status STATUS] [-limit N] [-json]")
	}
	index, err := readSessionsDiagnosticsIndex(opts.dir)
	if err != nil {
		return err
	}
	var rows []gateway.StoredSessionRunRow
	for _, row := range index.RunRows {
		if !matchesSession(row.SessionID, opts.sessionID) || !matchesOptional(row.Status, *status) {
			continue
		}
		rows = append(rows, row)
	}
	total := len(rows)
	rows = limitRows(rows, opts.limit)
	if opts.jsonOut {
		return printJSON(out, sessionsQueryResult[gateway.StoredSessionRunRow]{
			Dir:       index.Dir,
			BuiltAt:   index.BuiltAt,
			SessionID: opts.sessionID,
			Limit:     normalizeLimit(opts.limit),
			Total:     total,
			Rows:      rows,
		})
	}
	fmt.Fprintln(out, "billyharness session runs")
	fmt.Fprintf(out, "dir: %s\n", index.Dir)
	printSessionFilter(out, opts.sessionID, opts.limit, total)
	if len(rows) == 0 {
		fmt.Fprintln(out, "runs: none")
		return nil
	}
	for _, row := range rows {
		fmt.Fprintf(out, "- %s run=%s status=%s seq=%d..%d started=%s ended=%s error=%s\n",
			row.SessionID,
			emptyDash(row.RunID),
			emptyDash(row.Status),
			row.StartSeq,
			row.EndSeq,
			emptyDash(row.StartedAt),
			emptyDash(row.EndedAt),
			emptyDash(snippet(row.Error, 120)),
		)
	}
	return nil
}

func sessionsImportCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sessions import", flag.ExitOnError)
	input := fs.String("input", "", "external transcript file")
	format := fs.String("format", sessionpkg.ImportFormatAuto, "input format: auto, jsonl, or markdown")
	jsonOut := fs.Bool("json", false, "print converted messages/events as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *input == "" && fs.NArg() == 1 {
		*input = fs.Arg(0)
	}
	if *input == "" || fs.NArg() > 1 {
		return fmt.Errorf("usage: sessions import [-input FILE] [-format auto|jsonl|markdown] [-json]")
	}
	raw, err := os.ReadFile(*input)
	if err != nil {
		return err
	}
	result, err := sessionpkg.ImportTranscript(raw, sessionpkg.ImportOptions{
		Source: *input,
		Format: *format,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(out, result)
	}
	fmt.Fprintln(out, "billyharness session import")
	fmt.Fprintf(out, "source: %s\n", result.Source)
	fmt.Fprintf(out, "format: %s\n", result.Format)
	fmt.Fprintf(out, "messages: %d imported + 1 marker = %d\n", result.Diagnostics.ImportedMessages, result.Diagnostics.MessageCount)
	fmt.Fprintf(out, "approx_tokens: %d\n", result.Diagnostics.ApproxTokens)
	fmt.Fprintf(out, "roles: user=%d assistant=%d system=%d\n", result.Diagnostics.UserMessages, result.Diagnostics.AssistantMessages, result.Diagnostics.SystemMessages)
	if len(result.Diagnostics.Warnings) == 0 {
		fmt.Fprintln(out, "warnings: none")
	} else {
		fmt.Fprintln(out, "warnings:")
		for _, warning := range result.Diagnostics.Warnings {
			if warning.Line > 0 {
				fmt.Fprintf(out, "- line=%d reason=%s detail=%s\n", warning.Line, warning.Reason, emptyDash(warning.Detail))
			} else {
				fmt.Fprintf(out, "- reason=%s detail=%s\n", warning.Reason, emptyDash(warning.Detail))
			}
		}
	}
	fmt.Fprintln(out, "json: rerun with -json to print converted messages/events")
	return nil
}

func addSessionsDiagnosticsFlags(fs *flag.FlagSet) *sessionsDiagnosticsOptions {
	opts := &sessionsDiagnosticsOptions{}
	fs.StringVar(&opts.dir, "dir", gateway.DefaultSessionStoreDir(), "gateway session store directory")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON")
	fs.IntVar(&opts.limit, "limit", 20, "maximum rows to print")
	fs.StringVar(&opts.sessionID, "session", "", "filter by session id")
	return opts
}

func readSessionsDiagnosticsIndex(dir string) (gateway.StoredSessionDiagnosticsIndex, error) {
	index, err := gateway.ReadStoredSessionDiagnosticsIndex(dir)
	if err == nil {
		return index, nil
	}
	action := fmt.Sprintf("run `fast-agent-harness sessions index rebuild -dir %s`", dir)
	if errors.Is(err, os.ErrNotExist) {
		return gateway.StoredSessionDiagnosticsIndex{}, fmt.Errorf("session diagnostics index missing for %s; %s", dir, action)
	}
	return gateway.StoredSessionDiagnosticsIndex{}, fmt.Errorf("session diagnostics index unreadable for %s: %w; %s", dir, err, action)
}

func printSessionFilter(out io.Writer, sessionID string, limit, total int) {
	if strings.TrimSpace(sessionID) != "" {
		fmt.Fprintf(out, "session: %s\n", sessionID)
	}
	fmt.Fprintf(out, "limit: %d\n", normalizeLimit(limit))
	fmt.Fprintf(out, "rows: %d\n", total)
}

func printJSON(out io.Writer, value any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func limitRows[T any](rows []T, limit int) []T {
	limit = normalizeLimit(limit)
	if len(rows) <= limit {
		return rows
	}
	return rows[:limit]
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func matchesSession(rowSessionID, filter string) bool {
	filter = strings.TrimSpace(filter)
	return filter == "" || rowSessionID == filter
}

func matchesOptional(value, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(filter))
}

func snippet(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "..."
}

func truncatedMarker(truncated bool) string {
	if truncated {
		return " truncated=true"
	}
	return ""
}

func usageTotal(row gateway.StoredSessionUsageRow) int64 {
	return row.InputTokens + row.OutputTokens + row.CacheHitTokens + row.CacheMissTokens + row.ReasoningTokens
}

func printSessionIndex(out io.Writer, index gateway.StoredSessionIndex, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(index)
	}
	fmt.Fprintf(out, "billyharness session index\n")
	fmt.Fprintf(out, "dir: %s\n", index.Dir)
	fmt.Fprintf(out, "built: %s\n", index.BuiltAt.Format(time.RFC3339))
	fmt.Fprintf(out, "sessions: %d\n", index.SessionCount)
	for _, session := range index.Sessions {
		fmt.Fprintf(out, "- %s messages=%d history=%d events=%d snapshot=%t event_replay=%t replay=%t\n",
			session.ID,
			session.MessageCount,
			session.HistorySeq,
			session.EventSeq,
			session.MessageSnapshotReady,
			session.EventReplayReady,
			session.OfflineReplayReady,
		)
	}
	for _, warning := range index.Warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
	return nil
}

func printSessionInspection(out io.Writer, inspection gateway.StoredSessionInspection) {
	fmt.Fprintf(out, "billyharness session\n")
	fmt.Fprintf(out, "id: %s\n", inspection.SessionID)
	fmt.Fprintf(out, "dir: %s\n", inspection.Dir)
	if inspection.SessionDir != "" {
		fmt.Fprintf(out, "session dir: %s\n", inspection.SessionDir)
	}
	fmt.Fprintf(out, "mode: %s\n", sessionMode(inspection.Legacy))
	if owner := sessionOwnerText(inspection.Owner); owner != "" {
		fmt.Fprintf(out, "owner: %s\n", owner)
	}
	fmt.Fprintf(out, "messages: %d\n", inspection.MessageCount)
	if len(inspection.Readiness) > 0 {
		fmt.Fprintf(out, "readiness: %s\n", strings.Join(inspection.Readiness, ","))
	}
	fmt.Fprintf(out, "message snapshot: %t\n", inspection.MessageSnapshotReady)
	fmt.Fprintf(out, "event replay: %t\n", inspection.EventReplayReady)
	fmt.Fprintf(out, "offline replay: %t\n", inspection.OfflineReplayReady)
	if inspection.Manifest.SchemaVersion != 0 || inspection.Manifest.SessionID != "" {
		fmt.Fprintf(out, "manifest: schema=%d history=%s events=%s snapshots=%s,%s,%s\n",
			inspection.Manifest.SchemaVersion,
			emptyDash(inspection.Manifest.HistoryJSONL),
			emptyDash(inspection.Manifest.EventsJSONL),
			emptyDash(inspection.Manifest.ConfigSnapshotJSON),
			emptyDash(inspection.Manifest.ModelProviderSnapshotJSON),
			emptyDash(inspection.Manifest.MCPSnapshotJSON),
		)
	}
	fmt.Fprintf(out, "history: exists=%t records=%d messages=%d sha=%s\n",
		inspection.History.Exists,
		inspection.History.Records,
		inspection.History.MessageCount,
		emptyDash(inspection.History.HistorySHA256),
	)
	fmt.Fprintf(out, "events: exists=%t records=%d last=%s output_refs=%d verified=%t missing=%d hash_mismatch=%d\n",
		inspection.Events.Exists,
		inspection.Events.Records,
		emptyDash(inspection.Events.LastEvent),
		inspection.Events.OutputRefs,
		inspection.Events.OutputRefsVerified,
		inspection.Events.MissingOutputRefs,
		inspection.Events.OutputRefHashMismatch,
	)
	lifecycle := inspection.Events.Lifecycle
	fmt.Fprintf(out, "lifecycle: runs=%d/%d open=%d turns=%d/%d open=%d steps=%d/%d open=%d tool_attempts=%d/%d open=%d\n",
		lifecycle.RunsClosed,
		lifecycle.RunsStarted,
		lifecycle.RunsOpen,
		lifecycle.TurnsClosed,
		lifecycle.TurnsStarted,
		lifecycle.TurnsOpen,
		lifecycle.StepsClosed,
		lifecycle.StepsStarted,
		lifecycle.StepsOpen,
		lifecycle.ToolAttemptsClosed,
		lifecycle.ToolAttemptsStarted,
		lifecycle.ToolAttemptsOpen,
	)
	validation := inspection.Events.Validation
	fmt.Fprintf(out, "validation: valid=%t envelope=%t sequence=%t lifecycle=%t gaps=%d duplicates=%d unmatched_progress=%d unmatched_output_refs=%d unmatched_permissions=%d\n",
		validation.Valid,
		validation.EnvelopeValid,
		validation.SequenceValid,
		validation.LifecycleValid,
		validation.SequenceGaps,
		validation.DuplicateSeqs,
		validation.UnmatchedProgress,
		validation.UnmatchedOutputRefs,
		validation.UnmatchedPermissions,
	)
	if !validation.ClosedLifecycleValid || validation.ClosedLifecycleError != "" {
		fmt.Fprintf(out, "closed lifecycle: valid=%t error=%s\n",
			validation.ClosedLifecycleValid,
			emptyDash(validation.ClosedLifecycleError),
		)
	}
	if validation.Error != "" {
		fmt.Fprintf(out, "validation error: kind=%s line=%d record=%d error=%s\n",
			emptyDash(validation.CorruptionKind),
			validation.Line,
			validation.RecordNo,
			validation.Error,
		)
	}
	inputs := inspection.Inputs
	fmt.Fprintf(out, "inputs: exists=%t records=%d pending=%d promoted=%d completed=%d ambiguous=%d valid=%t\n",
		inputs.Exists,
		inputs.Records,
		inputs.Pending,
		inputs.Promoted,
		inputs.Completed,
		inputs.Ambiguous,
		inputs.ValidationValid,
	)
	if inputs.Error != "" {
		fmt.Fprintf(out, "inputs error: %s\n", inputs.Error)
	}
	terminal := inspection.Events.Terminal
	fmt.Fprintf(out, "terminal: state=%s event=%s run=%s seq=%d\n",
		emptyDash(terminal.State),
		emptyDash(terminal.Event),
		emptyDash(terminal.RunID),
		terminal.Seq,
	)
	if terminal.LastError != "" {
		fmt.Fprintf(out, "terminal error: %s\n", terminal.LastError)
	}
	projector := inspection.Events.Projector
	fmt.Fprintf(out, "projector: parity=%t run_state=%s last_seq=%d snapshot_seq=%d transcript_events=%d tool_calls=%d/%d assistant_bytes=%d reasoning_bytes=%d\n",
		projector.ParityOK,
		emptyDash(projector.RunState),
		projector.LastSeq,
		projector.SnapshotLastSeq,
		projector.TranscriptEvents,
		projector.ToolCallsProjected,
		projector.ToolCallsRaw,
		projector.AssistantBytes,
		projector.ReasoningBytes,
	)
	if projector.SeqGap != nil {
		fmt.Fprintf(out, "projector seq gap: after=%d got=%d\n", projector.SeqGap.AfterSeq, projector.SeqGap.GotSeq)
	}
	fmt.Fprintln(out, "files:")
	for _, file := range inspection.Files {
		fmt.Fprintf(out, "  - %s exists=%t bytes=%d path=%s\n", file.Name, file.Exists, file.Bytes, file.Path)
	}
	for _, warning := range inspection.Warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
}

func sessionOwnerText(owner gatewayapi.SessionOwner) string {
	var parts []string
	if owner.ClientType != "" {
		parts = append(parts, "client_type="+owner.ClientType)
	}
	if owner.TelegramChatID != 0 {
		parts = append(parts, fmt.Sprintf("telegram_chat=%d", owner.TelegramChatID))
	}
	if owner.TelegramThreadID != 0 {
		parts = append(parts, fmt.Sprintf("telegram_thread=%d", owner.TelegramThreadID))
	}
	if owner.TelegramUserID != 0 {
		parts = append(parts, fmt.Sprintf("telegram_user=%d", owner.TelegramUserID))
	}
	if owner.TUIChatID != "" {
		parts = append(parts, "tui_chat="+owner.TUIChatID)
	}
	if owner.Profile != "" {
		parts = append(parts, "profile="+owner.Profile)
	}
	if owner.Model != "" {
		parts = append(parts, "model="+owner.Model)
	}
	return strings.Join(parts, " ")
}

func sessionMode(legacy bool) string {
	if legacy {
		return "legacy"
	}
	return "jsonl"
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

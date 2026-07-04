package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	"github.com/billyhargroveofficial/billyharness/internal/mcpstatus"
	"github.com/billyhargroveofficial/billyharness/internal/runtimehost"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
	tuitranscript "github.com/billyhargroveofficial/billyharness/internal/tui/transcript"
)

type incidentCollectOptions struct {
	SessionID      string
	OutDir         string
	SessionDir     string
	RepoDir        string
	IncludeLogs    bool
	IncludeMCP     bool
	DoctorBuild    bool
	DoctorServices bool
	DoctorGateway  bool
	JSONOut        bool
	Timeout        time.Duration
}

type incidentBundleReport struct {
	Version     string               `json:"version"`
	GeneratedAt string               `json:"generated_at"`
	OutDir      string               `json:"out_dir"`
	SessionID   string               `json:"session_id"`
	SessionDir  string               `json:"session_dir"`
	RepoDir     string               `json:"repo_dir,omitempty"`
	Files       []incidentBundleFile `json:"files"`
	Warnings    []string             `json:"warnings,omitempty"`
}

type incidentBundleFile struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Bytes     int64  `json:"bytes,omitempty"`
	Redacted  bool   `json:"redacted"`
	Error     string `json:"error,omitempty"`
	Optional  bool   `json:"optional,omitempty"`
	Generated bool   `json:"generated,omitempty"`
}

var (
	incidentURLCredentialPattern = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)([^/\s@]+@)`)
	incidentURLSecretQuery       = regexp.MustCompile(`(?i)([?&](?:access_token|refresh_token|id_token|token|api[_-]?key|apikey|secret|password)=)[^&\\\s"'<>]+`)
	incidentHeaderSecretPattern  = regexp.MustCompile(`(?im)(^|\\[rn]|\r?\n)(\s*(?:authorization|proxy-authorization|x-api-key|api-key|cookie|set-cookie)\s*[:=]\s*)[^\\\r\n]+`)
)

func incidentCmd(args []string) error {
	return incidentCommand(args, os.Stdout, osDoctorRunner{})
}

func incidentCommand(args []string, out io.Writer, runner doctorCommandRunner) error {
	if len(args) == 0 {
		incidentUsage(out)
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "collect":
		return incidentCollectCommand(args[1:], out, runner)
	case "help", "-h", "--help":
		incidentUsage(out)
		return nil
	default:
		incidentUsage(out)
		return fmt.Errorf("unknown incident command %q", args[0])
	}
}

func incidentCollectCommand(args []string, out io.Writer, runner doctorCommandRunner) error {
	fs := flag.NewFlagSet("incident collect", flag.ExitOnError)
	sessionID := fs.String("session", "", "gateway session ID to collect")
	outDir := fs.String("out", "", "output directory for the incident bundle")
	sessionDir := fs.String("dir", gateway.DefaultSessionStoreDir(), "gateway session store directory")
	repoDir := fs.String("repo", "", "repository directory for doctor/hygiene checks")
	includeLogs := fs.Bool("logs", true, "include journalctl tails when available")
	includeMCP := fs.Bool("mcp", true, "include local MCP status")
	doctorBuild := fs.Bool("build", true, "include doctor build check")
	doctorServices := fs.Bool("services", true, "include doctor service checks")
	doctorGateway := fs.Bool("gateway", true, "include doctor gateway readiness check")
	jsonOut := fs.Bool("json", false, "print machine-readable bundle report")
	timeoutSec := fs.Int("timeout-sec", 10, "per-command timeout seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: incident collect -session SESSION_ID -out DIR")
	}
	opts := incidentCollectOptions{
		SessionID:      strings.TrimSpace(*sessionID),
		OutDir:         strings.TrimSpace(*outDir),
		SessionDir:     strings.TrimSpace(*sessionDir),
		RepoDir:        strings.TrimSpace(*repoDir),
		IncludeLogs:    *includeLogs,
		IncludeMCP:     *includeMCP,
		DoctorBuild:    *doctorBuild,
		DoctorServices: *doctorServices,
		DoctorGateway:  *doctorGateway,
		JSONOut:        *jsonOut,
		Timeout:        time.Duration(*timeoutSec) * time.Second,
	}
	resolved, err := config.ResolveStrict()
	if err != nil {
		return err
	}
	report, err := collectIncidentBundleFromResolved(context.Background(), resolved, opts, runner)
	if err != nil {
		return err
	}
	if opts.JSONOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	fmt.Fprintf(out, "incident bundle: %s\n", report.OutDir)
	fmt.Fprintf(out, "session: %s\n", report.SessionID)
	fmt.Fprintf(out, "files: %d\n", len(report.Files))
	for _, warning := range report.Warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
	return nil
}

func collectIncidentBundleFromResolved(ctx context.Context, resolved config.ResolvedConfig, opts incidentCollectOptions, runner doctorCommandRunner) (incidentBundleReport, error) {
	if strings.TrimSpace(opts.SessionID) == "" {
		return incidentBundleReport{}, fmt.Errorf("incident collect requires -session")
	}
	if strings.TrimSpace(opts.OutDir) == "" {
		return incidentBundleReport{}, fmt.Errorf("incident collect requires -out")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	sessionDir := filepath.Clean(opts.SessionDir)
	if sessionDir == "" || sessionDir == "." {
		sessionDir = gateway.DefaultSessionStoreDir()
	}
	outDir := filepath.Clean(opts.OutDir)
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return incidentBundleReport{}, err
	}
	if err := os.Chmod(outDir, 0o700); err != nil {
		return incidentBundleReport{}, err
	}

	report := incidentBundleReport{
		Version:     version,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		OutDir:      outDir,
		SessionID:   opts.SessionID,
		SessionDir:  sessionDir,
		RepoDir:     opts.RepoDir,
	}
	writer := incidentBundleWriter{root: outDir, report: &report}

	if err := writer.writeText("README.txt", "summary", incidentBundleReadme(report)); err != nil {
		return report, err
	}

	doctorOpts := doctorOptions{
		RepoDir:       opts.RepoDir,
		JSON:          true,
		CheckBuild:    opts.DoctorBuild,
		CheckServices: opts.DoctorServices,
		CheckGateway:  opts.DoctorGateway,
		Timeout:       opts.Timeout,
	}
	doctorReport := collectDoctorReportFromResolved(ctx, resolved, doctorOpts, runner)
	if err := writer.writeJSON("doctor.json", "doctor", doctorReport); err != nil {
		return report, err
	}
	var doctorText bytes.Buffer
	printDoctorReport(&doctorText, doctorReport)
	if err := writer.writeText("doctor.txt", "doctor", doctorText.String()); err != nil {
		return report, err
	}

	configReport := struct {
		Config      map[string]any            `json:"config"`
		Values      []config.ResolvedValue    `json:"values"`
		Diagnostics config.DiagnosticSnapshot `json:"diagnostics"`
		Warnings    []string                  `json:"warnings,omitempty"`
	}{
		Config:      resolved.SanitizedConfig(),
		Values:      resolved.SanitizedValues(),
		Diagnostics: resolved.DiagnosticSnapshot(),
		Warnings:    resolved.Warnings,
	}
	if err := writer.writeJSON("config.json", "config", configReport); err != nil {
		return report, err
	}
	if err := writer.writeText("config.txt", "config", config.FormatSummary(resolved.SanitizedValues(), resolved.Warnings)); err != nil {
		return report, err
	}

	authSnapshot := resolved.Config.ProviderAuthSnapshot()
	authStatus := credentials.CurrentStatusForRuntime(resolved.Config.AuthSettings(), authSnapshot.Provider, authSnapshot.Model)
	if err := writer.writeJSON("auth.json", "auth", authStatus); err != nil {
		return report, err
	}
	if err := writer.writeText("auth.txt", "auth", credentials.FormatStatusText(authStatus)); err != nil {
		return report, err
	}

	if opts.IncludeMCP {
		collectIncidentMCP(ctx, opts, resolved.Config, &writer)
	}

	inspection, err := gateway.InspectStoredSession(sessionDir, opts.SessionID)
	if err != nil {
		return report, err
	}
	if err := writer.writeJSON("session-inspect.json", "session_inspect", inspection); err != nil {
		return report, err
	}
	var inspectionText bytes.Buffer
	printSessionInspection(&inspectionText, inspection)
	if err := writer.writeText("session-inspect.txt", "session_inspect", inspectionText.String()); err != nil {
		return report, err
	}

	contextReport, err := gateway.StoredSessionContext(sessionDir, opts.SessionID, resolved.Config.RuntimeLimits())
	if err != nil {
		writer.writeOptionalError("session-context.error.txt", "session_context", err)
	} else {
		if err := writer.writeJSON("session-context.json", "session_context", contextReport); err != nil {
			return report, err
		}
		if err := writer.writeText("session-context.txt", "session_context", gatewayclient.FormatSessionContext(contextReport)); err != nil {
			return report, err
		}
	}

	transcript, err := gateway.LoadStoredSessionTranscript(sessionDir, opts.SessionID)
	if err != nil {
		writer.writeOptionalError("session-transcript.error.txt", "session_transcript", err)
	} else {
		if err := writer.writeJSON("session-transcript.json", "session_transcript", transcript); err != nil {
			return report, err
		}
		rich := tuitranscript.FormatSession(transcript.Messages, transcript.Events, tuitranscript.ExportModeRich)
		if err := writer.writeText("session-transcript-rich.md", "session_transcript", rich); err != nil {
			return report, err
		}
		raw := tuitranscript.FormatSession(transcript.Messages, transcript.Events, tuitranscript.ExportModeRaw)
		if err := writer.writeText("session-transcript-raw.md", "session_transcript", raw); err != nil {
			return report, err
		}
	}

	if inspection.Events.Path != "" && inspection.Events.Exists {
		if err := writer.copyTextFile("session-events.redacted.jsonl", "session_events", inspection.Events.Path); err != nil {
			return report, err
		}
	} else {
		writer.writeOptionalError("session-events.redacted.jsonl", "session_events", os.ErrNotExist)
	}

	if opts.IncludeLogs {
		collectIncidentLogs(ctx, opts, runner, &writer)
	}

	if err := writer.writeJSON("incident-manifest.json", "manifest", report); err != nil {
		return report, err
	}
	return report, nil
}

type incidentBundleWriter struct {
	root   string
	report *incidentBundleReport
}

func (w incidentBundleWriter) writeJSON(rel, kind string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return w.writeFile(rel, kind, body, false, true)
}

func (w incidentBundleWriter) writeText(rel, kind string, text string) error {
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return w.writeFile(rel, kind, []byte(text), false, true)
}

func (w incidentBundleWriter) copyTextFile(rel, kind, source string) error {
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return w.writeFile(rel, kind, body, false, true)
}

func (w incidentBundleWriter) writeOptionalError(rel, kind string, err error) {
	if err == nil {
		return
	}
	detail := err.Error()
	if errors.Is(err, os.ErrNotExist) {
		detail = "unavailable: " + detail
	}
	if w.report != nil {
		w.report.Warnings = append(w.report.Warnings, kind+": "+redactIncidentText(detail))
	}
	_ = w.writeFile(rel, kind, []byte("error: "+detail+"\n"), true, true)
}

func (w incidentBundleWriter) writeFile(rel, kind string, body []byte, optional bool, generated bool) error {
	rel, err := cleanIncidentBundleRelPath(rel)
	if err != nil {
		return err
	}
	body = []byte(redactIncidentText(string(body)))
	path := filepath.Join(w.root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		if optional && w.report != nil {
			w.report.Warnings = append(w.report.Warnings, rel+": "+redactIncidentText(err.Error()))
			w.report.Files = append(w.report.Files, incidentBundleFile{
				Path:     rel,
				Kind:     kind,
				Redacted: true,
				Error:    redactIncidentText(err.Error()),
				Optional: true,
			})
			return nil
		}
		return err
	}
	if w.report != nil {
		w.report.Files = append(w.report.Files, incidentBundleFile{
			Path:      rel,
			Kind:      kind,
			Bytes:     int64(len(body)),
			Redacted:  true,
			Optional:  optional,
			Generated: generated,
		})
	}
	return nil
}

func collectIncidentMCP(ctx context.Context, opts incidentCollectOptions, cfg config.Config, writer *incidentBundleWriter) {
	mcpCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	status, err := runtimehost.MCPStatus(mcpCtx, runtimehost.SettingsFromConfig(cfg))
	if err != nil {
		writer.writeOptionalError("mcp.error.txt", "mcp", err)
		return
	}
	if err := writer.writeJSON("mcp.json", "mcp", status); err != nil {
		writer.writeOptionalError("mcp.error.txt", "mcp", err)
		return
	}
	if err := writer.writeText("mcp.txt", "mcp", mcpstatus.Format(status)); err != nil {
		writer.writeOptionalError("mcp.error.txt", "mcp", err)
	}
}

func collectIncidentLogs(ctx context.Context, opts incidentCollectOptions, runner doctorCommandRunner, writer *incidentBundleWriter) {
	for _, service := range doctorManagedServices() {
		rel := filepath.Join("logs", service.Service+".log")
		out, err := runDoctorCommand(ctx, runner, "", opts.Timeout, "journalctl", "-u", service.Service, "-n", "200", "--no-pager")
		if err != nil {
			if isCommandMissing(err) {
				writer.writeOptionalError(rel, "logs", fmt.Errorf("journalctl unavailable"))
				continue
			}
			writer.writeOptionalError(rel, "logs", errors.New(commandErrorDetail(out, err)))
			continue
		}
		if err := writer.writeText(rel, "logs", out); err != nil {
			writer.writeOptionalError(rel, "logs", err)
		}
	}
}

func incidentBundleReadme(report incidentBundleReport) string {
	return strings.TrimSpace(fmt.Sprintf(`Billyharness incident bundle

Generated: %s
Session: %s
Session store: %s
Repo: %s

All generated text artifacts are passed through local redaction before they are written. Review paths and user content before sharing the bundle outside the operator machine.
`, report.GeneratedAt, report.SessionID, report.SessionDir, emptyDash(report.RepoDir)))
}

func redactIncidentText(text string) string {
	out := incidentURLCredentialPattern.ReplaceAllString(text, `${1}redacted:redacted@`)
	out = incidentURLSecretQuery.ReplaceAllString(out, `${1}[redacted]`)
	out = incidentHeaderSecretPattern.ReplaceAllString(out, `${1}${2}[redacted]`)
	out = secrets.Redact(out)
	out = incidentHeaderSecretPattern.ReplaceAllString(out, `${1}${2}[redacted]`)
	return out
}

func cleanIncidentBundleRelPath(rel string) (string, error) {
	rel = filepath.Clean(strings.TrimSpace(rel))
	if rel == "" || rel == "." {
		return "", fmt.Errorf("empty incident bundle path")
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe incident bundle path %q", rel)
	}
	return rel, nil
}

func incidentUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: fast-agent-harness incident collect -session SESSION_ID -out DIR")
	fmt.Fprintln(w, "       fast-agent-harness incident collect -session SESSION_ID -out DIR [-dir SESSION_DIR] [-repo DIR] [-logs=true] [-mcp=true] [-json]")
}

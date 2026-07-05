package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

type fakeDoctorRunner struct {
	responses map[string]fakeDoctorResponse
	calls     []string
}

type fakeDoctorResponse struct {
	out string
	err error
}

func (f *fakeDoctorRunner) CombinedOutput(_ context.Context, dir, name string, args ...string) (string, error) {
	key := doctorRunnerKey(dir, name, args...)
	f.calls = append(f.calls, key)
	if resp, ok := f.responses[key]; ok {
		return resp.out, resp.err
	}
	return "", nil
}

func doctorRunnerKey(dir, name string, args ...string) string {
	return dir + "|" + name + " " + strings.Join(args, " ")
}

func TestCollectDoctorReportIncludesProjectHealth(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	billyHome := filepath.Join(root, "home")
	t.Setenv("BILLYHARNESS_HOME", billyHome)
	t.Setenv("FAST_AGENT_MODEL", "deepseek-v4-pro")
	t.Setenv("DEEPSEEK_REASONING_EFFORT", "xhigh")
	t.Setenv("DEEPSEEK_API_KEY", "present")
	writeTestFile(t, repo, "cmd/fast-agent-harness/doctor.go", "package main\n")
	writeTestFile(t, repo, "bin/fast-agent-harness", "binary\n")
	writeTestFile(t, billyHome, "gateway-sessions/session/events.jsonl", "{}\n")
	writeTestFile(t, billyHome, "tool-output/ref.txt", "output\n")
	writeTestFile(t, billyHome, "auth/credentials.json", "{}\n")
	writeTestFile(t, billyHome, "auth/codex.json", "{}\n")

	runner := &fakeDoctorRunner{responses: map[string]fakeDoctorResponse{
		doctorRunnerKey(repo, "git", "ls-files", "--", "*.go"): {
			out: "cmd/fast-agent-harness/doctor.go\n",
		},
		doctorRunnerKey(repo, "git", "status", "--short"): {
			out: "",
		},
		doctorRunnerKey(repo, goCommand(), "test", "-run", "^$", "./cmd/fast-agent-harness"): {
			out: "ok\n",
		},
		doctorRunnerKey("", "systemctl", "is-active", "billyharness-gateway.service"): {
			out: "active\n",
		},
		doctorRunnerKey("", "systemctl", "is-active", "billyharness-telegram.service"): {
			out: "active\n",
		},
	}}

	cfg := config.Default()
	report := collectDoctorReport(context.Background(), cfg, doctorOptions{
		RepoDir:       repo,
		CheckBuild:    true,
		CheckServices: true,
		CheckGateway:  false,
		Timeout:       time.Second,
	}, runner)

	if report.RepoDir != repo {
		t.Fatalf("RepoDir = %q, want %q", report.RepoDir, repo)
	}
	if report.Mode != "local" {
		t.Fatalf("Mode = %q, want local", report.Mode)
	}
	if report.Version == "" || report.BuildCommit == "" || report.BuildTime == "" {
		t.Fatalf("build metadata missing: version=%q commit=%q time=%q", report.Version, report.BuildCommit, report.BuildTime)
	}
	if report.BillyHome != filepath.Join(root, "home") {
		t.Fatalf("BillyHome = %q", report.BillyHome)
	}
	if report.SettingsPath != filepath.Join(root, "home", "settings.json") {
		t.Fatalf("SettingsPath = %q", report.SettingsPath)
	}
	if report.MCPConfigPath != filepath.Join(root, "home", "mcp.config.toml") {
		t.Fatalf("MCPConfigPath = %q", report.MCPConfigPath)
	}
	if report.GatewaySessionDir != filepath.Join(root, "home", "gateway-sessions") {
		t.Fatalf("GatewaySessionDir = %q", report.GatewaySessionDir)
	}
	if report.Config.Provider != "deepseek" || report.Config.Model != "deepseek-v4-pro" || report.Config.ReasoningEffort != "xhigh" {
		t.Fatalf("Config = %#v", report.Config)
	}
	if report.Config.APIKeyEnv != "DEEPSEEK_API_KEY" || report.Config.CodexAuthFile == "" || report.CodexAuthPath != report.Config.CodexAuthFile {
		t.Fatalf("provider/auth diagnostics = %#v codex_path=%q", report.Config.ProviderAuthSnapshot, report.CodexAuthPath)
	}
	if report.Config.ProviderCapability.Provider != "deepseek" ||
		report.Config.ProviderCapability.Model != "deepseek-v4-pro" ||
		report.Config.ProviderCapability.ValidationError != "" {
		t.Fatalf("provider capability diagnostics = %#v", report.Config.ProviderCapability)
	}
	if report.Config.MaxToolRounds == 0 || report.Config.MCPAllowedServers == "" || report.Config.WebSummaryMode == "" {
		t.Fatalf("runtime/tool diagnostics = %#v", report.Config.RuntimeToolSnapshot)
	}
	if report.Runtime.Provider != "deepseek" || report.Runtime.Model != "deepseek-v4-pro" || report.Runtime.GatewayURL == "" {
		t.Fatalf("Runtime provider/model/gateway = %#v", report.Runtime)
	}
	if !report.Runtime.Auth.APIKeyEnvSet || !report.Runtime.Auth.CredentialFileExists || !report.Runtime.Auth.CodexAuthFileExists {
		t.Fatalf("Runtime auth presence = %#v", report.Runtime.Auth)
	}
	if report.Runtime.Auth.CostMode != "metered" ||
		report.Runtime.Auth.DeepSeek.AuthType != "api-key" ||
		report.Runtime.Auth.DeepSeek.Credential != "redacted" ||
		report.Runtime.Auth.Codex.AuthType != "codex-oauth" ||
		report.Runtime.Auth.Codex.Credential != "missing" {
		t.Fatalf("Runtime auth classification = %#v", report.Runtime.Auth)
	}
	if !report.Runtime.ServiceBinary.Exists || report.Runtime.ServiceBinary.SizeBytes == 0 || report.Runtime.ServiceBinary.AgeSeconds < 0 {
		t.Fatalf("Runtime service binary = %#v", report.Runtime.ServiceBinary)
	}
	if !report.Runtime.GatewaySessionStore.Exists || report.Runtime.GatewaySessionStore.SizeBytes == 0 {
		t.Fatalf("Runtime session store = %#v", report.Runtime.GatewaySessionStore)
	}
	if !report.Runtime.ToolOutputStore.Exists || report.Runtime.ToolOutputStore.SizeBytes == 0 {
		t.Fatalf("Runtime tool output store = %#v", report.Runtime.ToolOutputStore)
	}
	if report.Runtime.StrictHygiene.Status != "ok" || report.Runtime.StrictHygiene.TrackedGoFiles != 1 {
		t.Fatalf("Runtime strict hygiene = %#v", report.Runtime.StrictHygiene)
	}
	assertDoctorCheck(t, report, "git status", "ok")
	assertDoctorCheck(t, report, "build check", "ok")
	assertDoctorCheck(t, report, "config provider/model", "ok")
	assertDoctorCheck(t, report, "provider capability", "ok")
	assertDoctorCheck(t, report, "gateway bind address", "ok")
	assertDoctorCheck(t, report, "auth configured", "ok")
	assertDoctorCheck(t, report, "tool catalog", "ok")
	assertDoctorCheck(t, report, "session store access", "ok")
	assertDoctorCheck(t, report, "service billyharness-gateway.service", "ok")
	assertDoctorCheck(t, report, "service billyharness-telegram.service", "ok")
	assertDoctorCheck(t, report, "gateway /health", "skip")
	assertDoctorCheck(t, report, "gateway /ready", "skip")
	for _, check := range report.Checks {
		if check.Name == "build check" && !strings.HasPrefix(check.Detail, goCommand()+" test -run '^$'") {
			t.Fatalf("build check detail = %q", check.Detail)
		}
	}

	var buf bytes.Buffer
	printDoctorReport(&buf, report)
	out := buf.String()
	for _, want := range []string{"billyharness doctor", "build: commit=", "mode: local", "model=deepseek-v4-pro", "settings:", "capability:", "validation=ok", "runtime:", "strict_hygiene=ok", "tool_output=", "auth:", "cost_mode=metered", "auth status:", "credential=redacted", "checks:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("formatted report missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorProviderCapabilityCheckFailsRuntimeValidation(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	cfg := config.Default()
	cfg.Provider = "deepseek"
	cfg.Model = "unknown-non-custom-model"

	check := doctorProviderCapabilityCheck(cfg)
	if check.Status != "fail" ||
		!strings.Contains(check.Detail, "model capabilities are unknown") ||
		!strings.Contains(check.Detail, "provider=deepseek") {
		t.Fatalf("provider capability check = %#v", check)
	}
}

func TestDoctorMCPAllowlistCheckFailsMissingDisabledAndUnsupported(t *testing.T) {
	cfg := config.Config{
		MCPEnabled:        true,
		MCPAllowedServers: []string{"github", "remote", "disabled"},
		MCPServers: []config.MCPServer{
			{Name: "remote", Enabled: true, UnsupportedReason: "streamable HTTP MCP is not implemented"},
			{Name: "disabled", Enabled: false, Command: "stdio-helper"},
		},
	}

	check := doctorMCPAllowlistCheck(cfg)
	if check.Status != "fail" ||
		!strings.Contains(check.Detail, "missing=github") ||
		!strings.Contains(check.Detail, "disabled=disabled") ||
		!strings.Contains(check.Detail, "unsupported=remote") {
		t.Fatalf("mcp allowlist check = %#v", check)
	}
}

func TestDoctorMCPAllowlistCheckPassesAvailableAllowedServers(t *testing.T) {
	cfg := config.Config{
		MCPEnabled:        true,
		MCPAllowedServers: []string{"github", "context7"},
		MCPServers: []config.MCPServer{
			{Name: "github", Enabled: true, Command: "github-mcp"},
			{Name: "context7", Enabled: true, Command: "context7-mcp"},
		},
	}

	check := doctorMCPAllowlistCheck(cfg)
	if check.Status != "ok" || !strings.Contains(check.Detail, "2 allowed") {
		t.Fatalf("mcp allowlist check = %#v", check)
	}
}

func TestCollectDoctorReportFromResolvedPrintsConfigProvenance(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	t.Setenv("FAST_AGENT_ENV_FILE", "")
	t.Setenv("FAST_AGENT_MODEL", "gpt-5.5")
	t.Setenv("FAST_AGENT_WEB_SEARCH_BACKEND", "exa")
	t.Setenv("FAST_AGENT_WEB_EXTRACT_BACKEND", "tavily")

	resolved, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	report := collectDoctorReportFromResolved(context.Background(), resolved, doctorOptions{
		RepoDir:       repo,
		CheckBuild:    false,
		CheckServices: false,
		CheckGateway:  false,
		Timeout:       time.Second,
	}, &fakeDoctorRunner{})
	if report.Config.ModelSource == nil || report.Config.ModelSource.Source != config.SourceEnvironment {
		t.Fatalf("doctor model source = %#v", report.Config.ModelSource)
	}
	if report.Config.WebSearchBackendSource == nil || report.Config.WebSearchBackendSource.SourceKey != "FAST_AGENT_WEB_SEARCH_BACKEND" {
		t.Fatalf("doctor web backend source = %#v", report.Config.WebSearchBackendSource)
	}

	var buf bytes.Buffer
	printDoctorReport(&buf, report)
	out := buf.String()
	for _, want := range []string{
		"config provenance:",
		"model=environment:FAST_AGENT_MODEL",
		"web_backend=environment:FAST_AGENT_WEB_SEARCH_BACKEND/environment:FAST_AGENT_WEB_EXTRACT_BACKEND",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorReportTracksFailuresForStrictMode(t *testing.T) {
	report := doctorReport{Checks: []doctorCheck{
		{Name: "git status", Status: "ok"},
		{Name: "build check", Status: "fail", Detail: "compile error"},
	}}
	if !doctorHasFailures(report) {
		t.Fatal("doctorHasFailures = false, want true")
	}
	report = doctorReport{Runtime: doctorRuntimeStatus{StrictHygiene: doctorHygieneStatus{Status: "fail"}}}
	if !doctorHasFailures(report) {
		t.Fatal("doctorHasFailures ignored strict hygiene failure")
	}
}

func TestDoctorServiceStatusSkipsMissingSystemctl(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	runner := &fakeDoctorRunner{responses: map[string]fakeDoctorResponse{
		doctorRunnerKey("", "systemctl", "is-active", "billyharness-gateway.service"): {
			err: execNotFound("systemctl"),
		},
		doctorRunnerKey("", "systemctl", "is-active", "billyharness-telegram.service"): {
			err: execNotFound("systemctl"),
		},
	}}
	checks := doctorServiceStatuses(context.Background(), doctorOptions{CheckServices: true, Timeout: time.Second}, runner)
	for _, check := range checks {
		if !strings.HasPrefix(check.Name, "service ") {
			continue
		}
		if check.Status != "skip" || !strings.Contains(check.Detail, "systemctl unavailable") {
			t.Fatalf("check = %#v", check)
		}
	}
}

func TestDoctorServiceStatusDetectsDuplicateProcessesAndStalePIDFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BILLYHARNESS_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "gateway.pid"), []byte("999999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeDoctorRunner{responses: map[string]fakeDoctorResponse{
		doctorRunnerKey("", "systemctl", "is-active", "billyharness-gateway.service"): {
			out: "active\n",
		},
		doctorRunnerKey("", "systemctl", "is-active", "billyharness-telegram.service"): {
			out: "active\n",
		},
		doctorRunnerKey("", "pgrep", "-af", "fast-agent-harness"): {
			out: strings.Join([]string{
				"111 /root/billyharness/bin/fast-agent-harness gateway",
				"222 /root/billyharness/bin/fast-agent-harness (deleted) gateway -addr 127.0.0.1:8765",
				"333 /root/billyharness/bin/fast-agent-harness telegram",
			}, "\n"),
		},
	}}
	checks := doctorServiceStatuses(context.Background(), doctorOptions{CheckServices: true, Timeout: time.Second}, runner)
	assertDoctorCheckInList(t, checks, "process gateway duplicates", "fail")
	assertDoctorCheckInList(t, checks, "process telegram duplicates", "ok")
	gatewayPID := findDoctorCheck(t, checks, "pid file gateway.pid")
	if gatewayPID.Status != "warn" || !strings.Contains(gatewayPID.Detail, "stale pid 999999999") {
		t.Fatalf("gateway pid check = %#v", gatewayPID)
	}
	telegramPID := findDoctorCheck(t, checks, "pid file telegram.pid")
	if telegramPID.Status != "ok" || telegramPID.Detail != "absent" {
		t.Fatalf("telegram pid check = %#v", telegramPID)
	}
}

func TestDoctorProductionServiceChecksIncludeUnitAndJournalSignals(t *testing.T) {
	t.Setenv("BILLYHARNESS_HOME", t.TempDir())
	runner := &fakeDoctorRunner{responses: map[string]fakeDoctorResponse{
		doctorRunnerKey("", "systemctl", "is-active", "billyharness-gateway.service"): {
			out: "active\n",
		},
		doctorRunnerKey("", "systemctl", "is-active", "billyharness-telegram.service"): {
			out: "active\n",
		},
		doctorRunnerKey("", "systemctl", "show", "--property=FragmentPath", "--property=WorkingDirectory", "--property=User", "--property=Restart", "--property=NRestarts", "billyharness-gateway.service"): {
			out: "FragmentPath=/etc/systemd/system/billyharness-gateway.service\nWorkingDirectory=/root/billyharness\nUser=root\nRestart=always\nNRestarts=0\n",
		},
		doctorRunnerKey("", "systemctl", "show", "--property=FragmentPath", "--property=WorkingDirectory", "--property=User", "--property=Restart", "--property=NRestarts", "billyharness-telegram.service"): {
			out: "FragmentPath=/etc/systemd/system/billyharness-telegram.service\nWorkingDirectory=/root/billyharness\nUser=root\nRestart=always\nNRestarts=2\n",
		},
		doctorRunnerKey("", "journalctl", "--unit", "billyharness-gateway.service", "--since", "-1 hour", "--no-pager", "--lines", "200"): {
			out: "no issues\n",
		},
		doctorRunnerKey("", "journalctl", "--unit", "billyharness-telegram.service", "--since", "-1 hour", "--no-pager", "--lines", "200"): {
			out: "panic: reconnect loop\n",
		},
	}}
	checks := doctorServiceStatuses(context.Background(), doctorOptions{Mode: "production", CheckServices: true, Timeout: time.Second}, runner)
	assertDoctorCheckInList(t, checks, "service unit billyharness-gateway.service", "ok")
	assertDoctorCheckInList(t, checks, "service journal billyharness-gateway.service", "ok")
	assertDoctorCheckInList(t, checks, "service journal billyharness-telegram.service", "fail")
}

func TestDoctorGatewayStatusesProbeHealthAndReadiness(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"ok":true,"provider":"mock","model":"mock"}`))
		case "/ready":
			_, _ = w.Write([]byte(`{"ok":true,"checks":[{"name":"tool_catalog","status":"ok"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAST_AGENT_GATEWAY_URL", server.URL)

	checks := doctorGatewayStatuses(context.Background(), config.Default(), doctorOptions{CheckGateway: true, Timeout: time.Second})
	assertDoctorCheckInList(t, checks, "gateway /health", "ok")
	assertDoctorCheckInList(t, checks, "gateway /ready", "ok")
}

func assertDoctorCheck(t *testing.T, report doctorReport, name, status string) {
	t.Helper()
	assertDoctorCheckInList(t, report.Checks, name, status)
}

func assertDoctorCheckInList(t *testing.T, checks []doctorCheck, name, status string) {
	t.Helper()
	check := findDoctorCheck(t, checks, name)
	if check.Status != status {
		t.Fatalf("%s status = %q, want %q (detail %q)", name, check.Status, status, check.Detail)
	}
}

func findDoctorCheck(t *testing.T, checks []doctorCheck, name string) doctorCheck {
	t.Helper()
	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("missing doctor check %q in %#v", name, checks)
	return doctorCheck{}
}

func execNotFound(name string) error {
	return &exec.Error{Name: name, Err: exec.ErrNotFound}
}

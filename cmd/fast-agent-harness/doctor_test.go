package main

import (
	"bytes"
	"context"
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
	if report.Config.MaxToolRounds == 0 || report.Config.MCPAllowedServers == "" || report.Config.WebSummaryMode == "" {
		t.Fatalf("runtime/tool diagnostics = %#v", report.Config.RuntimeToolSnapshot)
	}
	if report.Runtime.Provider != "deepseek" || report.Runtime.Model != "deepseek-v4-pro" || report.Runtime.GatewayURL == "" {
		t.Fatalf("Runtime provider/model/gateway = %#v", report.Runtime)
	}
	if !report.Runtime.Auth.APIKeyEnvSet || !report.Runtime.Auth.CredentialFileExists || !report.Runtime.Auth.CodexAuthFileExists {
		t.Fatalf("Runtime auth presence = %#v", report.Runtime.Auth)
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
	assertDoctorCheck(t, report, "service billyharness-gateway.service", "ok")
	assertDoctorCheck(t, report, "service billyharness-telegram.service", "ok")
	assertDoctorCheck(t, report, "gateway /health", "skip")
	for _, check := range report.Checks {
		if check.Name == "build check" && !strings.HasPrefix(check.Detail, goCommand()+" test -run '^$'") {
			t.Fatalf("build check detail = %q", check.Detail)
		}
	}

	var buf bytes.Buffer
	printDoctorReport(&buf, report)
	out := buf.String()
	for _, want := range []string{"billyharness doctor", "model=deepseek-v4-pro", "settings:", "runtime:", "strict_hygiene=ok", "tool_output=", "auth:", "checks:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("formatted report missing %q:\n%s", want, out)
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
	runner := &fakeDoctorRunner{responses: map[string]fakeDoctorResponse{
		doctorRunnerKey("", "systemctl", "is-active", "billyharness-gateway.service"): {
			err: execNotFound("systemctl"),
		},
		doctorRunnerKey("", "systemctl", "is-active", "billyharness-telegram.service"): {
			err: execNotFound("systemctl"),
		},
	}}
	checks := doctorServiceStatuses(context.Background(), doctorOptions{CheckServices: true, Timeout: time.Second}, runner)
	if len(checks) != 2 {
		t.Fatalf("checks len = %d", len(checks))
	}
	for _, check := range checks {
		if check.Status != "skip" || !strings.Contains(check.Detail, "systemctl unavailable") {
			t.Fatalf("check = %#v", check)
		}
	}
}

func assertDoctorCheck(t *testing.T, report doctorReport, name, status string) {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			if check.Status != status {
				t.Fatalf("%s status = %q, want %q (detail %q)", name, check.Status, status, check.Detail)
			}
			return
		}
	}
	t.Fatalf("missing doctor check %q in %#v", name, report.Checks)
}

func execNotFound(name string) error {
	return &exec.Error{Name: name, Err: exec.ErrNotFound}
}

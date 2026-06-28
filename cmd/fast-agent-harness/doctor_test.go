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
	t.Setenv("BILLYHARNESS_HOME", filepath.Join(root, "home"))
	t.Setenv("FAST_AGENT_MODEL", "deepseek-v4-pro")
	t.Setenv("DEEPSEEK_REASONING_EFFORT", "xhigh")

	runner := &fakeDoctorRunner{responses: map[string]fakeDoctorResponse{
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
	assertDoctorCheck(t, report, "git status", "ok")
	assertDoctorCheck(t, report, "build check", "ok")
	assertDoctorCheck(t, report, "service billyharness-gateway.service", "ok")
	assertDoctorCheck(t, report, "service billyharness-telegram.service", "ok")
	assertDoctorCheck(t, report, "gateway /health", "skip")

	var buf bytes.Buffer
	printDoctorReport(&buf, report)
	out := buf.String()
	for _, want := range []string{"billyharness doctor", "model=deepseek-v4-pro", "settings:", "checks:"} {
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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
)

type doctorOptions struct {
	RepoDir       string
	JSON          bool
	Strict        bool
	CheckBuild    bool
	CheckServices bool
	CheckGateway  bool
	Timeout       time.Duration
}

type doctorReport struct {
	Version           string             `json:"version"`
	GeneratedAt       string             `json:"generated_at"`
	CWD               string             `json:"cwd"`
	RepoDir           string             `json:"repo_dir,omitempty"`
	BillyHome         string             `json:"billy_home"`
	SettingsPath      string             `json:"settings_path"`
	EnvPath           string             `json:"env_path"`
	MCPConfigPath     string             `json:"mcp_config_path"`
	CodexAuthPath     string             `json:"codex_auth_path"`
	GatewaySessionDir string             `json:"gateway_session_dir"`
	Config            doctorConfigStatus `json:"config"`
	Checks            []doctorCheck      `json:"checks"`
}

type doctorConfigStatus struct {
	Provider              string `json:"provider"`
	Model                 string `json:"model"`
	Profile               string `json:"profile"`
	Thinking              string `json:"thinking"`
	ReasoningEffort       string `json:"reasoning_effort"`
	ContextWindowTokens   int64  `json:"context_window_tokens"`
	ContextCompactTokens  int    `json:"context_compact_tokens"`
	WebSummaryMode        string `json:"web_summary_mode"`
	WebSummaryProvider    string `json:"web_summary_provider"`
	WebSummaryModel       string `json:"web_summary_model"`
	MaxToolRounds         int    `json:"max_tool_rounds"`
	MaxParallelTools      int    `json:"max_parallel_tools"`
	GatewayAddr           string `json:"gateway_addr"`
	AutoApproveDangerous  bool   `json:"auto_approve_dangerous"`
	MCPEnabled            bool   `json:"mcp_enabled"`
	MCPAllowedServers     string `json:"mcp_allowed_servers"`
	MaxToolOutputBytes    int    `json:"max_tool_output_bytes"`
	StoreReasoningContent bool   `json:"store_reasoning_content"`
}

type doctorCheck struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type doctorCommandRunner interface {
	CombinedOutput(ctx context.Context, dir, name string, args ...string) (string, error)
}

type osDoctorRunner struct{}

func (osDoctorRunner) CombinedOutput(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func doctorCmd(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "print machine-readable JSON")
	strict := fs.Bool("strict", false, "exit non-zero when a check fails")
	repoDir := fs.String("repo", "", "repository directory; defaults to current git root")
	checkBuild := fs.Bool("build", true, "compile-check the CLI package")
	checkServices := fs.Bool("services", true, "check billyharness systemd services")
	checkGateway := fs.Bool("gateway", true, "check gateway /health")
	timeoutSec := fs.Int("timeout-sec", 10, "per-command timeout seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts := doctorOptions{
		RepoDir:       strings.TrimSpace(*repoDir),
		JSON:          *jsonOut,
		Strict:        *strict,
		CheckBuild:    *checkBuild,
		CheckServices: *checkServices,
		CheckGateway:  *checkGateway,
		Timeout:       time.Duration(*timeoutSec) * time.Second,
	}
	cfg := config.Default()
	report := collectDoctorReport(context.Background(), cfg, opts, osDoctorRunner{})
	if opts.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printDoctorReport(os.Stdout, report)
	}
	if opts.Strict && doctorHasFailures(report) {
		return fmt.Errorf("doctor found failing checks")
	}
	return nil
}

func collectDoctorReport(ctx context.Context, cfg config.Config, opts doctorOptions, runner doctorCommandRunner) doctorReport {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	cwd, _ := os.Getwd()
	billyHome := config.BillyHomeDir()
	report := doctorReport{
		Version:           version,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		CWD:               cwd,
		BillyHome:         billyHome,
		SettingsPath:      filepath.Join(billyHome, "settings.json"),
		EnvPath:           filepath.Join(billyHome, ".env"),
		MCPConfigPath:     config.DefaultMCPConfigFile(),
		CodexAuthPath:     cfg.CodexAuthFile,
		GatewaySessionDir: gateway.DefaultSessionStoreDir(),
		Config: doctorConfigStatus{
			Provider:              cfg.Provider,
			Model:                 cfg.Model,
			Profile:               cfg.Profile,
			Thinking:              cfg.Thinking,
			ReasoningEffort:       cfg.ReasoningEffort,
			ContextWindowTokens:   cfg.ContextWindowTokens,
			ContextCompactTokens:  cfg.ContextCompactTokens,
			WebSummaryMode:        cfg.WebSummaryMode,
			WebSummaryProvider:    cfg.WebSummaryProvider,
			WebSummaryModel:       cfg.WebSummaryModel,
			MaxToolRounds:         cfg.MaxToolRounds,
			MaxParallelTools:      cfg.MaxParallelTools,
			GatewayAddr:           cfg.GatewayAddr,
			AutoApproveDangerous:  cfg.AutoApproveDangerous,
			MCPEnabled:            cfg.MCPEnabled,
			MCPAllowedServers:     strings.Join(cfg.MCPAllowedServers, ","),
			MaxToolOutputBytes:    cfg.MaxToolOutputBytes,
			StoreReasoningContent: cfg.StoreReasoningContent,
		},
	}

	repoDir := opts.RepoDir
	if repoDir == "" {
		repoDir, report.Checks = resolveDoctorRepo(ctx, cwd, opts, runner, report.Checks)
	}
	report.RepoDir = repoDir
	report.Checks = append(report.Checks, doctorGitStatus(ctx, repoDir, opts, runner))
	report.Checks = append(report.Checks, doctorBuildStatus(ctx, repoDir, opts, runner))
	report.Checks = append(report.Checks, doctorServiceStatuses(ctx, opts, runner)...)
	report.Checks = append(report.Checks, doctorGatewayStatus(ctx, cfg, opts))
	return report
}

func resolveDoctorRepo(ctx context.Context, cwd string, opts doctorOptions, runner doctorCommandRunner, checks []doctorCheck) (string, []doctorCheck) {
	start := time.Now()
	out, err := runDoctorCommand(ctx, runner, cwd, opts.Timeout, "git", "rev-parse", "--show-toplevel")
	check := doctorCheck{Name: "git root", DurationMS: time.Since(start).Milliseconds()}
	if err != nil {
		check.Status = "warn"
		check.Detail = commandErrorDetail(out, err)
		return cwd, append(checks, check)
	}
	root := strings.TrimSpace(out)
	if root == "" {
		check.Status = "warn"
		check.Detail = "empty git root; using cwd"
		return cwd, append(checks, check)
	}
	check.Status = "ok"
	check.Detail = root
	return root, append(checks, check)
}

func doctorGitStatus(ctx context.Context, repoDir string, opts doctorOptions, runner doctorCommandRunner) doctorCheck {
	if strings.TrimSpace(repoDir) == "" {
		return doctorCheck{Name: "git status", Status: "skip", Detail: "repository directory unknown"}
	}
	start := time.Now()
	out, err := runDoctorCommand(ctx, runner, repoDir, opts.Timeout, "git", "status", "--short")
	check := doctorCheck{Name: "git status", DurationMS: time.Since(start).Milliseconds()}
	if err != nil {
		check.Status = "warn"
		check.Detail = commandErrorDetail(out, err)
		return check
	}
	out = strings.TrimSpace(out)
	if out == "" {
		check.Status = "ok"
		check.Detail = "clean"
		return check
	}
	check.Status = "warn"
	check.Detail = "dirty: " + firstLines(out, 6)
	return check
}

func doctorBuildStatus(ctx context.Context, repoDir string, opts doctorOptions, runner doctorCommandRunner) doctorCheck {
	if !opts.CheckBuild {
		return doctorCheck{Name: "build check", Status: "skip", Detail: "disabled"}
	}
	if strings.TrimSpace(repoDir) == "" {
		return doctorCheck{Name: "build check", Status: "skip", Detail: "repository directory unknown"}
	}
	start := time.Now()
	out, err := runDoctorCommand(ctx, runner, repoDir, opts.Timeout, goCommand(), "test", "-run", "^$", "./cmd/fast-agent-harness")
	check := doctorCheck{Name: "build check", DurationMS: time.Since(start).Milliseconds()}
	if err != nil {
		check.Status = "fail"
		check.Detail = commandErrorDetail(out, err)
		return check
	}
	check.Status = "ok"
	check.Detail = "go test -run '^$' ./cmd/fast-agent-harness"
	return check
}

func doctorServiceStatuses(ctx context.Context, opts doctorOptions, runner doctorCommandRunner) []doctorCheck {
	services := []string{"billyharness-gateway.service", "billyharness-telegram.service"}
	out := make([]doctorCheck, 0, len(services))
	if !opts.CheckServices {
		for _, service := range services {
			out = append(out, doctorCheck{Name: "service " + service, Status: "skip", Detail: "disabled"})
		}
		return out
	}
	for _, service := range services {
		start := time.Now()
		cmdOut, err := runDoctorCommand(ctx, runner, "", opts.Timeout, "systemctl", "is-active", service)
		check := doctorCheck{Name: "service " + service, DurationMS: time.Since(start).Milliseconds()}
		state := strings.TrimSpace(cmdOut)
		switch {
		case err == nil && state == "active":
			check.Status = "ok"
			check.Detail = "active"
		case isCommandMissing(err):
			check.Status = "skip"
			check.Detail = "systemctl unavailable"
		default:
			check.Status = "fail"
			check.Detail = commandErrorDetail(cmdOut, err)
			if state != "" && !strings.Contains(check.Detail, state) {
				check.Detail = state + ": " + check.Detail
			}
		}
		out = append(out, check)
	}
	return out
}

func doctorGatewayStatus(ctx context.Context, cfg config.Config, opts doctorOptions) doctorCheck {
	if !opts.CheckGateway {
		return doctorCheck{Name: "gateway /health", Status: "skip", Detail: "disabled"}
	}
	start := time.Now()
	for _, candidate := range gatewayURLCandidates(cfg) {
		if gateway.WaitForReady(ctx, candidate, 0) {
			return doctorCheck{Name: "gateway /health", Status: "ok", Detail: candidate, DurationMS: time.Since(start).Milliseconds()}
		}
	}
	return doctorCheck{Name: "gateway /health", Status: "fail", Detail: "no healthy local gateway found", DurationMS: time.Since(start).Milliseconds()}
}

func runDoctorCommand(ctx context.Context, runner doctorCommandRunner, dir string, timeout time.Duration, name string, args ...string) (string, error) {
	if runner == nil {
		runner = osDoctorRunner{}
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := runner.CombinedOutput(cmdCtx, dir, name, args...)
	if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
		return out, context.DeadlineExceeded
	}
	return out, err
}

func printDoctorReport(w io.Writer, report doctorReport) {
	fmt.Fprintln(w, "billyharness doctor")
	fmt.Fprintf(w, "version: %s\n", report.Version)
	fmt.Fprintf(w, "cwd: %s\n", report.CWD)
	if report.RepoDir != "" {
		fmt.Fprintf(w, "repo: %s\n", report.RepoDir)
	}
	fmt.Fprintf(w, "home: %s\n", report.BillyHome)
	fmt.Fprintf(w, "settings: %s\n", report.SettingsPath)
	fmt.Fprintf(w, "env: %s\n", report.EnvPath)
	fmt.Fprintf(w, "mcp config: %s\n", report.MCPConfigPath)
	fmt.Fprintf(w, "sessions: %s\n", report.GatewaySessionDir)
	fmt.Fprintf(w, "config: provider=%s model=%s profile=%s reasoning=%s/%s context=%d compact_at=%d websum=%s/%s/%s gateway=%s\n",
		report.Config.Provider,
		report.Config.Model,
		report.Config.Profile,
		report.Config.Thinking,
		report.Config.ReasoningEffort,
		report.Config.ContextWindowTokens,
		report.Config.ContextCompactTokens,
		report.Config.WebSummaryMode,
		report.Config.WebSummaryProvider,
		report.Config.WebSummaryModel,
		report.Config.GatewayAddr,
	)
	fmt.Fprintln(w, "checks:")
	for _, check := range report.Checks {
		detail := strings.TrimSpace(check.Detail)
		if detail != "" {
			fmt.Fprintf(w, "  %-42s %-5s %s\n", check.Name, check.Status, detail)
		} else {
			fmt.Fprintf(w, "  %-42s %-5s\n", check.Name, check.Status)
		}
	}
}

func doctorHasFailures(report doctorReport) bool {
	for _, check := range report.Checks {
		if check.Status == "fail" {
			return true
		}
	}
	return false
}

func commandErrorDetail(out string, err error) string {
	out = strings.TrimSpace(out)
	if out != "" {
		out = firstLines(out, 8)
	}
	if err == nil {
		return out
	}
	if out == "" {
		return err.Error()
	}
	return out + " (" + err.Error() + ")"
}

func firstLines(value string, max int) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if max <= 0 || len(lines) <= max {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:max], "\n") + "\n..."
}

func isCommandMissing(err error) bool {
	if err == nil {
		return false
	}
	var pathErr *exec.Error
	return errors.As(err, &pathErr) && pathErr.Err == exec.ErrNotFound
}

func goCommand() string {
	if value := strings.TrimSpace(os.Getenv("GO")); value != "" {
		return value
	}
	if value, err := exec.LookPath("go"); err == nil && value != "" {
		return value
	}
	if _, err := os.Stat("/root/.local/go/bin/go"); err == nil {
		return "/root/.local/go/bin/go"
	}
	return "go"
}

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
	Version           string              `json:"version"`
	GeneratedAt       string              `json:"generated_at"`
	CWD               string              `json:"cwd"`
	RepoDir           string              `json:"repo_dir,omitempty"`
	BillyHome         string              `json:"billy_home"`
	SettingsPath      string              `json:"settings_path"`
	EnvPath           string              `json:"env_path"`
	MCPConfigPath     string              `json:"mcp_config_path"`
	CodexAuthPath     string              `json:"codex_auth_path"`
	GatewaySessionDir string              `json:"gateway_session_dir"`
	Config            doctorConfigStatus  `json:"config"`
	Runtime           doctorRuntimeStatus `json:"runtime"`
	Checks            []doctorCheck       `json:"checks"`
}

type doctorConfigStatus struct {
	config.ProviderAuthSnapshot
	config.RuntimeToolSnapshot
}

type doctorRuntimeStatus struct {
	Provider            string              `json:"provider"`
	Model               string              `json:"model"`
	GatewayURL          string              `json:"gateway_url,omitempty"`
	Auth                doctorAuthPresence  `json:"auth"`
	ServiceBinary       doctorFileStatus    `json:"service_binary"`
	GatewaySessionStore doctorPathUsage     `json:"gateway_session_store"`
	ToolOutputStore     doctorPathUsage     `json:"tool_output_store"`
	StrictHygiene       doctorHygieneStatus `json:"strict_hygiene"`
}

type doctorAuthPresence struct {
	Provider             string `json:"provider"`
	APIKeyEnv            string `json:"api_key_env,omitempty"`
	APIKeyEnvSet         bool   `json:"api_key_env_set"`
	CredentialFile       string `json:"credential_file,omitempty"`
	CredentialFileExists bool   `json:"credential_file_exists"`
	CodexAuthFile        string `json:"codex_auth_file,omitempty"`
	CodexAuthFileExists  bool   `json:"codex_auth_file_exists"`
}

type doctorFileStatus struct {
	Path       string `json:"path,omitempty"`
	Exists     bool   `json:"exists"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	ModTime    string `json:"mod_time,omitempty"`
	AgeSeconds int64  `json:"age_seconds,omitempty"`
	Error      string `json:"error,omitempty"`
}

type doctorPathUsage struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Error     string `json:"error,omitempty"`
}

type doctorHygieneStatus struct {
	Status            string `json:"status"`
	TrackedGoFiles    int    `json:"tracked_go_files,omitempty"`
	LargeFiles        int    `json:"large_files,omitempty"`
	MissingFiles      int    `json:"missing_files,omitempty"`
	AllowedLargeFiles int    `json:"allowed_large_files,omitempty"`
	Detail            string `json:"detail,omitempty"`
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
		CodexAuthPath:     cfg.ProviderAuthSnapshot().CodexAuthFile,
		GatewaySessionDir: gateway.DefaultSessionStoreDir(),
		Config: doctorConfigStatus{
			ProviderAuthSnapshot: cfg.ProviderAuthSnapshot(),
			RuntimeToolSnapshot:  cfg.RuntimeToolSnapshot(),
		},
	}

	repoDir := opts.RepoDir
	if repoDir == "" {
		repoDir, report.Checks = resolveDoctorRepo(ctx, cwd, opts, runner, report.Checks)
	}
	report.RepoDir = repoDir
	report.Runtime = collectDoctorRuntime(ctx, cfg, repoDir, opts, runner)
	report.Checks = append(report.Checks, doctorGitStatus(ctx, repoDir, opts, runner))
	report.Checks = append(report.Checks, doctorBuildStatus(ctx, repoDir, opts, runner))
	report.Checks = append(report.Checks, doctorServiceStatuses(ctx, opts, runner)...)
	report.Checks = append(report.Checks, doctorGatewayStatus(ctx, cfg, opts))
	return report
}

func collectDoctorRuntime(ctx context.Context, cfg config.Config, repoDir string, opts doctorOptions, runner doctorCommandRunner) doctorRuntimeStatus {
	providerAuth := cfg.ProviderAuthSnapshot()
	gatewayURL := ""
	if candidates := gatewayURLCandidates(cfg); len(candidates) > 0 {
		gatewayURL = candidates[0]
	}
	return doctorRuntimeStatus{
		Provider:            providerAuth.Provider,
		Model:               providerAuth.Model,
		GatewayURL:          gatewayURL,
		Auth:                doctorAuthPresenceStatus(providerAuth),
		ServiceBinary:       doctorFileStatusFor(filepath.Join(repoDir, "bin", "fast-agent-harness"), time.Now()),
		GatewaySessionStore: doctorPathUsageFor(gateway.DefaultSessionStoreDir()),
		ToolOutputStore:     doctorPathUsageFor(filepath.Join(config.BillyHomeDir(), "tool-output")),
		StrictHygiene:       doctorStrictHygieneStatus(ctx, repoDir, opts, runner),
	}
}

func doctorAuthPresenceStatus(auth config.ProviderAuthSnapshot) doctorAuthPresence {
	apiKeySet := false
	if strings.TrimSpace(auth.APIKeyEnv) != "" {
		_, apiKeySet = os.LookupEnv(auth.APIKeyEnv)
	}
	return doctorAuthPresence{
		Provider:             auth.Provider,
		APIKeyEnv:            auth.APIKeyEnv,
		APIKeyEnvSet:         apiKeySet,
		CredentialFile:       auth.CredentialFile,
		CredentialFileExists: regularFileExists(auth.CredentialFile),
		CodexAuthFile:        auth.CodexAuthFile,
		CodexAuthFileExists:  regularFileExists(auth.CodexAuthFile),
	}
}

func regularFileExists(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func doctorFileStatusFor(path string, now time.Time) doctorFileStatus {
	path = strings.TrimSpace(path)
	status := doctorFileStatus{Path: path}
	if path == "" {
		status.Error = "empty path"
		return status
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return status
		}
		status.Error = err.Error()
		return status
	}
	if info.IsDir() {
		status.Exists = true
		status.Error = "path is a directory"
		return status
	}
	status.Exists = true
	status.SizeBytes = info.Size()
	status.ModTime = info.ModTime().UTC().Format(time.RFC3339)
	if !now.IsZero() {
		age := now.Sub(info.ModTime())
		if age < 0 {
			age = 0
		}
		status.AgeSeconds = int64(age.Seconds())
	}
	return status
}

func doctorPathUsageFor(path string) doctorPathUsage {
	size, exists, err := pathSize(path)
	usage := doctorPathUsage{
		Path:      path,
		Exists:    exists,
		SizeBytes: size,
	}
	if err != nil {
		usage.Error = err.Error()
	}
	return usage
}

func doctorStrictHygieneStatus(ctx context.Context, repoDir string, opts doctorOptions, runner doctorCommandRunner) doctorHygieneStatus {
	if strings.TrimSpace(repoDir) == "" {
		return doctorHygieneStatus{Status: "skip", Detail: "repository directory unknown"}
	}
	report, err := collectHygieneReport(ctx, hygieneOptions{RepoDir: repoDir, Timeout: opts.Timeout}, runner)
	if err != nil {
		return doctorHygieneStatus{Status: "fail", Detail: err.Error()}
	}
	status := doctorHygieneStatus{
		Status:            "ok",
		TrackedGoFiles:    report.Source.TrackedGoFiles,
		LargeFiles:        len(report.Source.LargeFiles),
		MissingFiles:      len(report.Source.MissingFiles),
		AllowedLargeFiles: len(report.Source.AllowedLargeFiles),
		Detail:            "large source files: none",
	}
	if err := hygieneStrictError(report.Source); err != nil {
		status.Status = "fail"
		status.Detail = err.Error()
	}
	return status
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
	goBin := goCommand()
	out, err := runDoctorCommand(ctx, runner, repoDir, opts.Timeout, goBin, "test", "-run", "^$", "./cmd/fast-agent-harness")
	check := doctorCheck{Name: "build check", DurationMS: time.Since(start).Milliseconds()}
	if err != nil {
		check.Status = "fail"
		check.Detail = commandErrorDetail(out, err)
		return check
	}
	check.Status = "ok"
	check.Detail = goBin + " test -run '^$' ./cmd/fast-agent-harness"
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
	fmt.Fprintf(w, "config: provider=%s model=%s profile=%s reasoning=%s/%s spark_disabled=%v context=%d compact_at=%d compact=%s/%s/%s websum=%s/%s/%s webcache=%v/%s/%d gateway=%s\n",
		report.Config.Provider,
		report.Config.Model,
		report.Config.Profile,
		report.Config.Thinking,
		report.Config.ReasoningEffort,
		report.Config.DisableSpark,
		report.Config.ContextWindowTokens,
		report.Config.ContextCompactTokens,
		report.Config.ContextCompactStrategy,
		report.Config.ContextCompactSummaryProvider,
		report.Config.ContextCompactSummaryModel,
		report.Config.WebSummaryMode,
		report.Config.WebSummaryProvider,
		report.Config.WebSummaryModel,
		report.Config.WebCacheEnabled,
		time.Duration(report.Config.WebCacheTTLMS)*time.Millisecond,
		report.Config.WebCacheMaxBytes,
		report.Config.GatewayAddr,
	)
	fmt.Fprintf(w, "runtime: provider=%s model=%s gateway=%s strict_hygiene=%s service_binary=%s age=%s sessions=%s tool_output=%s\n",
		report.Runtime.Provider,
		report.Runtime.Model,
		report.Runtime.GatewayURL,
		report.Runtime.StrictHygiene.Status,
		doctorFileSummary(report.Runtime.ServiceBinary),
		doctorAgeSummary(report.Runtime.ServiceBinary.AgeSeconds),
		doctorPathUsageSummary(report.Runtime.GatewaySessionStore),
		doctorPathUsageSummary(report.Runtime.ToolOutputStore),
	)
	fmt.Fprintf(w, "auth: provider=%s api_key_env=%s credential_file=%s codex_auth=%s\n",
		report.Runtime.Auth.Provider,
		presenceSummary(report.Runtime.Auth.APIKeyEnv, report.Runtime.Auth.APIKeyEnvSet),
		presenceSummary(report.Runtime.Auth.CredentialFile, report.Runtime.Auth.CredentialFileExists),
		presenceSummary(report.Runtime.Auth.CodexAuthFile, report.Runtime.Auth.CodexAuthFileExists),
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

func doctorFileSummary(status doctorFileStatus) string {
	if strings.TrimSpace(status.Error) != "" {
		return status.Path + " error:" + status.Error
	}
	if !status.Exists {
		return status.Path + " missing"
	}
	return fmt.Sprintf("%s %s", status.Path, humanBytes(status.SizeBytes))
}

func doctorPathUsageSummary(usage doctorPathUsage) string {
	if strings.TrimSpace(usage.Error) != "" {
		return usage.Path + " error:" + usage.Error
	}
	if !usage.Exists {
		return usage.Path + " missing"
	}
	return usage.Path + " " + humanBytes(usage.SizeBytes)
}

func doctorAgeSummary(seconds int64) string {
	if seconds <= 0 {
		return "0s"
	}
	return (time.Duration(seconds) * time.Second).Round(time.Second).String()
}

func presenceSummary(label string, present bool) string {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "<unset>"
	}
	if present {
		return label + ":present"
	}
	return label + ":missing"
}

func doctorHasFailures(report doctorReport) bool {
	if report.Runtime.StrictHygiene.Status == "fail" {
		return true
	}
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

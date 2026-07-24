package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/attachments"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/credentials"
	"github.com/billyhargroveofficial/billyharness/internal/docsgen"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/serviceops"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
)

type doctorOptions struct {
	RepoDir       string
	Mode          string
	JSON          bool
	Deep          bool
	Strict        bool
	CheckBuild    bool
	CheckServices bool
	CheckGateway  bool
	CheckDocs     bool
	Timeout       time.Duration
}

type doctorReport struct {
	Version           string              `json:"version"`
	BuildCommit       string              `json:"build_commit"`
	BuildTime         string              `json:"build_time"`
	GeneratedAt       string              `json:"generated_at"`
	Mode              string              `json:"mode"`
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
	ProviderCapability config.ProviderCapabilitySnapshot `json:"provider_capability"`
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
	AttachmentsStore    doctorPathUsage     `json:"attachments_store"`
	StrictHygiene       doctorHygieneStatus `json:"strict_hygiene"`
}

type doctorAuthPresence struct {
	Provider             string                     `json:"provider"`
	Model                string                     `json:"model,omitempty"`
	CostMode             string                     `json:"cost_mode,omitempty"`
	APIKeyEnv            string                     `json:"api_key_env,omitempty"`
	APIKeyEnvSet         bool                       `json:"api_key_env_set"`
	CredentialFile       string                     `json:"credential_file,omitempty"`
	CredentialFileExists bool                       `json:"credential_file_exists"`
	CodexAuthFile        string                     `json:"codex_auth_file,omitempty"`
	CodexAuthFileExists  bool                       `json:"codex_auth_file_exists"`
	DeepSeek             credentials.ProviderStatus `json:"deepseek"`
	Codex                credentials.ProviderStatus `json:"codex"`
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
	deep := fs.Bool("deep", false, "run full operator checks; currently the default")
	strict := fs.Bool("strict", false, "exit non-zero when a check fails")
	repoDir := fs.String("repo", "", "repository directory; defaults to current git root")
	checkBuild := fs.Bool("build", true, "compile-check the CLI package")
	checkServices := fs.Bool("services", true, "check billyharness systemd services")
	checkGateway := fs.Bool("gateway", true, "check gateway /health and /ready")
	checkDocs := fs.Bool("docs", false, "check generated docs fingerprints against the live binary")
	mode := fs.String("mode", "auto", "doctor mode: auto, local, or production")
	timeoutSec := fs.Int("timeout-sec", 10, "per-command timeout seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	modeValue := strings.ToLower(strings.TrimSpace(*mode))
	if modeValue == "" {
		modeValue = "auto"
	}
	switch modeValue {
	case "auto", "local", "production":
	default:
		return fmt.Errorf("invalid doctor mode %q; use auto, local, or production", *mode)
	}
	opts := doctorOptions{
		RepoDir:       strings.TrimSpace(*repoDir),
		Mode:          modeValue,
		JSON:          *jsonOut,
		Deep:          *deep,
		Strict:        *strict,
		CheckBuild:    *checkBuild,
		CheckServices: *checkServices,
		CheckGateway:  *checkGateway,
		CheckDocs:     *checkDocs || *deep,
		Timeout:       time.Duration(*timeoutSec) * time.Second,
	}
	resolved, err := config.ResolveStrict()
	if err != nil {
		return err
	}
	report := collectDoctorReportFromResolved(context.Background(), resolved, opts, osDoctorRunner{})
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

func collectDoctorReportFromResolved(ctx context.Context, resolved config.ResolvedConfig, opts doctorOptions, runner doctorCommandRunner) doctorReport {
	report := collectDoctorReport(ctx, resolved.Config, opts, runner)
	diagnostics := resolved.DiagnosticSnapshot()
	report.Config.ProviderAuthSnapshot = diagnostics.ProviderAuth
	report.Config.ProviderCapability = diagnostics.ProviderCapability
	report.Config.RuntimeToolSnapshot = diagnostics.RuntimeTool
	return report
}

func collectDoctorReport(ctx context.Context, cfg config.Config, opts doctorOptions, runner doctorCommandRunner) doctorReport {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	cwd, _ := os.Getwd()
	billyHome := config.BillyHomeDir()
	build := currentBuildMetadata()
	report := doctorReport{
		Version:           build.Version,
		BuildCommit:       build.Commit,
		BuildTime:         build.BuiltAt,
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
			ProviderCapability:   cfg.ProviderCapabilitySnapshot(),
			RuntimeToolSnapshot:  cfg.RuntimeToolSnapshot(),
		},
	}

	repoDir := opts.RepoDir
	if repoDir == "" {
		repoDir, report.Checks = resolveDoctorRepo(ctx, cwd, opts, runner, report.Checks)
	}
	report.RepoDir = repoDir
	opts.Mode = normalizeDoctorMode(opts.Mode, repoDir)
	report.Mode = opts.Mode
	report.Runtime = collectDoctorRuntime(ctx, cfg, repoDir, opts, runner)
	doctorCtx := &doctorContext{
		Context: ctx,
		Config:  cfg,
		Runtime: report.Runtime,
		RepoDir: repoDir,
		Options: opts,
		Runner:  runner,
	}
	for _, spec := range doctorCheckSpecs() {
		if !doctorCheckSpecEnabled(spec, opts) {
			continue
		}
		report.Checks = append(report.Checks, spec.Run(doctorCtx)...)
	}
	return report
}

func doctorDocsStatuses(repoDir string, opts doctorOptions) []doctorCheck {
	if !opts.CheckDocs {
		return nil
	}
	targets := docsgen.Targets()
	out := make([]doctorCheck, 0, len(targets))
	if strings.TrimSpace(repoDir) == "" {
		for _, target := range targets {
			out = append(out, doctorCheck{Name: "docs:" + target.Name, Status: "n/a", Detail: "repository directory unknown"})
		}
		return out
	}
	dir := filepath.Join(repoDir, "docs", "generated")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		detail := "docs/generated absent"
		if err != nil && !os.IsNotExist(err) {
			detail = "docs/generated unavailable: " + err.Error()
		}
		for _, target := range targets {
			out = append(out, doctorCheck{Name: "docs:" + target.Name, Status: "n/a", Detail: detail})
		}
		return out
	}
	for _, status := range docsgen.VerifyAgainst(dir) {
		check := doctorCheck{Name: "docs:" + status.Name}
		switch status.Status {
		case docsgen.TargetStatusOK:
			check.Status = "ok"
			check.Detail = status.Filename + " hash=" + status.ActualHash
		case docsgen.TargetStatusStale:
			check.Status = "fail"
			check.Detail = status.Filename + " stale; run go run ./cmd/fast-agent-harness docsgen"
		case docsgen.TargetStatusMissing:
			check.Status = "fail"
			check.Detail = status.Filename + " missing; run go run ./cmd/fast-agent-harness docsgen"
		default:
			check.Status = "fail"
			check.Detail = status.Filename + " unreadable: " + status.Detail
		}
		out = append(out, check)
	}
	return out
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
		AttachmentsStore:    doctorPathUsageFor(attachments.DefaultStoreRoot()),
		StrictHygiene:       doctorStrictHygieneStatus(ctx, repoDir, opts, runner),
	}
}

func normalizeDoctorMode(mode string, repoDir string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "local", "production":
		return strings.ToLower(strings.TrimSpace(mode))
	}
	clean := filepath.Clean(strings.TrimSpace(repoDir))
	if clean == "/root/billyharness" {
		return "production"
	}
	return "local"
}

func doctorConfigChecks(cfg config.Config, auth doctorAuthPresence) []doctorCheck {
	checks := []doctorCheck{
		doctorEffectiveConfigCheck(cfg),
		doctorProviderCapabilityCheck(cfg),
		doctorGatewayBindCheck(cfg),
		doctorActiveAuthCheck(auth),
		doctorMCPAllowlistCheck(cfg),
	}
	return checks
}

func doctorEffectiveConfigCheck(cfg config.Config) doctorCheck {
	snapshot := cfg.ProviderAuthSnapshot()
	provider := strings.TrimSpace(snapshot.Provider)
	model := strings.TrimSpace(snapshot.Model)
	if provider == "" || model == "" {
		return doctorCheck{Name: "config provider/model", Status: "fail", Detail: "provider/model not fully set"}
	}
	return doctorCheck{Name: "config provider/model", Status: "ok", Detail: "provider=" + provider + " model=" + model}
}

func doctorProviderCapabilityCheck(cfg config.Config) doctorCheck {
	caps := cfg.ProviderCapabilitySnapshot()
	detail := fmt.Sprintf("provider=%s model=%s known=%t streaming=%t tools=%t parallel=%t",
		caps.Provider,
		caps.Model,
		caps.Known,
		caps.Streaming,
		caps.ToolCalls,
		caps.ParallelToolCalls,
	)
	if strings.TrimSpace(caps.ValidationError) != "" {
		return doctorCheck{Name: "provider capability", Status: "fail", Detail: detail + " error=" + caps.ValidationError}
	}
	return doctorCheck{Name: "provider capability", Status: "ok", Detail: detail}
}

func doctorGatewayBindCheck(cfg config.Config) doctorCheck {
	addr := strings.TrimSpace(cfg.GatewayAddr)
	if addr == "" {
		return doctorCheck{Name: "gateway bind address", Status: "fail", Detail: "empty"}
	}
	detail := addr
	if gateway.RequiresAuthForAddr(addr) {
		detail += " non-loopback-or-wildcard"
	} else {
		detail += " loopback"
	}
	return doctorCheck{Name: "gateway bind address", Status: "ok", Detail: detail}
}

func doctorActiveAuthCheck(auth doctorAuthPresence) doctorCheck {
	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	if provider == "" {
		return doctorCheck{Name: "auth configured", Status: "fail", Detail: "active provider unknown"}
	}
	if provider == "mock" || strings.EqualFold(auth.CostMode, "none") {
		return doctorCheck{Name: "auth configured", Status: "skip", Detail: "mock provider"}
	}
	if strings.Contains(provider, "codex") || provider == "openai" || provider == "openai-codex" {
		if auth.Codex.Configured {
			return doctorCheck{Name: "auth configured", Status: "ok", Detail: "codex oauth configured"}
		}
		return doctorCheck{Name: "auth configured", Status: "fail", Detail: "codex oauth missing"}
	}
	if provider == "deepseek" {
		if auth.DeepSeek.Configured {
			return doctorCheck{Name: "auth configured", Status: "ok", Detail: "deepseek api key configured"}
		}
		return doctorCheck{Name: "auth configured", Status: "fail", Detail: "deepseek api key missing"}
	}
	if auth.APIKeyEnvSet || auth.CredentialFileExists || auth.CodexAuthFileExists {
		return doctorCheck{Name: "auth configured", Status: "ok", Detail: "credential material present for " + provider}
	}
	return doctorCheck{Name: "auth configured", Status: "warn", Detail: "unknown provider credential state for " + provider}
}

func doctorMCPAllowlistCheck(cfg config.Config) doctorCheck {
	mcp := cfg.MCPSettings()
	if !mcp.Enabled {
		return doctorCheck{Name: "mcp allowlist", Status: "skip", Detail: "mcp disabled"}
	}
	allowed := doctorAllowedMCPNames(mcp.AllowedServers)
	if len(allowed) == 0 {
		if len(mcp.Servers) == 0 {
			return doctorCheck{Name: "mcp allowlist", Status: "warn", Detail: "mcp enabled with no configured servers"}
		}
		return doctorCheck{Name: "mcp allowlist", Status: "ok", Detail: fmt.Sprintf("%d configured server(s); no allowlist", len(mcp.Servers))}
	}
	byName := make(map[string]config.MCPServer, len(mcp.Servers))
	for _, server := range mcp.Servers {
		name := strings.ToLower(strings.TrimSpace(server.Name))
		if name != "" {
			byName[name] = server
		}
	}
	var missing, disabled, unsupported []string
	for _, name := range allowed {
		server, ok := byName[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		if !server.Enabled {
			disabled = append(disabled, name)
		}
		if reason := strings.TrimSpace(server.UnsupportedReason); reason != "" {
			unsupported = append(unsupported, name+": "+reason)
		}
	}
	if len(missing) > 0 || len(disabled) > 0 || len(unsupported) > 0 {
		var parts []string
		if len(missing) > 0 {
			parts = append(parts, "missing="+strings.Join(missing, ","))
		}
		if len(disabled) > 0 {
			parts = append(parts, "disabled="+strings.Join(disabled, ","))
		}
		if len(unsupported) > 0 {
			parts = append(parts, "unsupported="+strings.Join(unsupported, " | "))
		}
		return doctorCheck{Name: "mcp allowlist", Status: "fail", Detail: strings.Join(parts, " ")}
	}
	return doctorCheck{Name: "mcp allowlist", Status: "ok", Detail: fmt.Sprintf("%d allowed server(s) available", len(allowed))}
}

func doctorAllowedMCPNames(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range in {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func doctorToolCatalogStatus(cfg config.Config) doctorCheck {
	registry := tools.NewRegistry(cfg)
	count := len(registry.Specs())
	if count == 0 {
		return doctorCheck{Name: "tool catalog", Status: "fail", Detail: "no visible native tools"}
	}
	return doctorCheck{Name: "tool catalog", Status: "ok", Detail: fmt.Sprintf("%d visible native tools", count)}
}

func doctorSessionStoreAccessCheck(path string) doctorCheck {
	path = strings.TrimSpace(path)
	check := doctorCheck{Name: "session store access"}
	if path == "" {
		check.Status = "fail"
		check.Detail = "empty session store path"
		return check
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		parent := filepath.Dir(path)
		if detail, ok := doctorWritableDirectory(parent); ok {
			check.Status = "warn"
			check.Detail = "missing; parent writable: " + detail
		} else {
			check.Status = "fail"
			check.Detail = "missing; parent not writable: " + detail
		}
		return check
	}
	if err != nil {
		check.Status = "fail"
		check.Detail = err.Error()
		return check
	}
	if !info.IsDir() {
		check.Status = "fail"
		check.Detail = "path is not a directory"
		return check
	}
	if _, err := os.ReadDir(path); err != nil {
		check.Status = "fail"
		check.Detail = "not readable: " + err.Error()
		return check
	}
	if detail, ok := doctorWritableDirectory(path); ok {
		check.Status = "ok"
		check.Detail = "readable/writable: " + detail
	} else {
		check.Status = "fail"
		check.Detail = "not writable: " + detail
	}
	return check
}

func doctorAttachmentsStoreUsageCheck(usage doctorPathUsage) doctorCheck {
	check := doctorCheck{Name: "attachments store usage"}
	if strings.TrimSpace(usage.Error) != "" {
		check.Status = "warn"
		check.Detail = usage.Error
		return check
	}
	if !usage.Exists {
		check.Status = "ok"
		check.Detail = "missing; created on first attachment"
		return check
	}
	if usage.SizeBytes > defaultAttachmentsGCMaxBytes {
		check.Status = "warn"
		check.Detail = fmt.Sprintf("%s > %s; run attachments gc", humanBytes(usage.SizeBytes), humanBytes(defaultAttachmentsGCMaxBytes))
		return check
	}
	check.Status = "ok"
	check.Detail = fmt.Sprintf("%s <= %s", humanBytes(usage.SizeBytes), humanBytes(defaultAttachmentsGCMaxBytes))
	return check
}

func doctorWritableDirectory(path string) (string, bool) {
	file, err := os.CreateTemp(path, ".doctor-write-*")
	if err != nil {
		return err.Error(), false
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err.Error(), false
	}
	if err := os.Remove(name); err != nil {
		return err.Error(), false
	}
	return path, true
}

func doctorAuthPresenceStatus(auth config.ProviderAuthSnapshot) doctorAuthPresence {
	apiKeySet := false
	if strings.TrimSpace(auth.APIKeyEnv) != "" {
		_, apiKeySet = os.LookupEnv(auth.APIKeyEnv)
	}
	status := credentials.CurrentStatusForRuntime(config.AuthSettings{
		APIKeyEnv:      auth.APIKeyEnv,
		CredentialFile: auth.CredentialFile,
		CodexAuthFile:  auth.CodexAuthFile,
	}, auth.Provider, auth.Model)
	return doctorAuthPresence{
		Provider:             auth.Provider,
		Model:                auth.Model,
		CostMode:             status.CostMode,
		APIKeyEnv:            auth.APIKeyEnv,
		APIKeyEnvSet:         apiKeySet,
		CredentialFile:       auth.CredentialFile,
		CredentialFileExists: regularFileExists(auth.CredentialFile),
		CodexAuthFile:        auth.CodexAuthFile,
		CodexAuthFileExists:  regularFileExists(auth.CodexAuthFile),
		DeepSeek:             status.DeepSeek,
		Codex:                status.Codex,
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
	services := doctorManagedServices()
	out := make([]doctorCheck, 0, len(services)*3)
	if !opts.CheckServices {
		for _, service := range services {
			out = append(out, doctorCheck{Name: "service " + service.Service, Status: "skip", Detail: "disabled"})
		}
		return out
	}
	for _, service := range services {
		start := time.Now()
		cmdOut, err := runDoctorCommand(ctx, runner, "", opts.Timeout, "systemctl", "is-active", service.Service)
		check := doctorCheck{Name: "service " + service.Service, DurationMS: time.Since(start).Milliseconds()}
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
	out = append(out, doctorProcessDuplicateChecks(ctx, opts, runner, services)...)
	out = append(out, doctorPIDFileChecks(services)...)
	if opts.Mode == "production" {
		out = append(out, doctorServiceUnitMetadataChecks(ctx, opts, runner, services)...)
		out = append(out, doctorServiceJournalChecks(ctx, opts, runner, services)...)
	}
	return out
}

func doctorManagedServices() []serviceops.ManagedService {
	return serviceops.ManagedServices()
}

func doctorProcessDuplicateChecks(ctx context.Context, opts doctorOptions, runner doctorCommandRunner, services []serviceops.ManagedService) []doctorCheck {
	start := time.Now()
	cmdOut, err := runDoctorCommand(ctx, runner, "", opts.Timeout, "pgrep", "-af", "fast-agent-harness")
	durationMS := time.Since(start).Milliseconds()
	out := make([]doctorCheck, 0, len(services))
	if isCommandMissing(err) {
		for _, service := range services {
			out = append(out, doctorCheck{Name: "process " + service.Subcommand + " duplicates", Status: "skip", Detail: "pgrep unavailable", DurationMS: durationMS})
		}
		return out
	}
	for _, service := range services {
		check := doctorCheck{Name: "process " + service.Subcommand + " duplicates", DurationMS: durationMS}
		matches := doctorMatchingProcessLines(cmdOut, service.Subcommand)
		switch {
		case len(matches) > 1:
			check.Status = "fail"
			check.Detail = fmt.Sprintf("%d live %s processes: %s", len(matches), service.Subcommand, strings.Join(firstStringItems(matches, 4), "; "))
		case len(matches) == 1:
			check.Status = "ok"
			check.Detail = "1 live " + service.Subcommand + " process: " + matches[0]
		default:
			check.Status = "ok"
			if err != nil {
				check.Detail = "no live " + service.Subcommand + " process found"
			} else {
				check.Detail = "0 live " + service.Subcommand + " processes"
			}
		}
		out = append(out, check)
	}
	return out
}

func doctorPIDFileChecks(services []serviceops.ManagedService) []doctorCheck {
	out := make([]doctorCheck, 0, len(services))
	for _, service := range services {
		path := filepath.Join(config.BillyHomeDir(), service.PIDFile)
		check := doctorCheck{Name: "pid file " + service.PIDFile}
		bytes, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			check.Status = "ok"
			check.Detail = "absent"
			out = append(out, check)
			continue
		}
		if err != nil {
			check.Status = "warn"
			check.Detail = path + ": " + err.Error()
			out = append(out, check)
			continue
		}
		raw := strings.TrimSpace(string(bytes))
		pid, err := strconv.Atoi(raw)
		if err != nil || pid <= 0 {
			check.Status = "warn"
			check.Detail = fmt.Sprintf("malformed pid %q in %s", raw, path)
			out = append(out, check)
			continue
		}
		if !doctorProcessExists(pid) {
			check.Status = "warn"
			check.Detail = fmt.Sprintf("stale pid %d in %s; process is not running", pid, path)
			out = append(out, check)
			continue
		}
		check.Status = "ok"
		check.Detail = fmt.Sprintf("pid %d alive", pid)
		if cmdline := doctorProcessCmdline(pid); cmdline != "" {
			check.Detail += ": " + cmdline
		}
		out = append(out, check)
	}
	return out
}

func doctorServiceUnitMetadataChecks(ctx context.Context, opts doctorOptions, runner doctorCommandRunner, services []serviceops.ManagedService) []doctorCheck {
	out := make([]doctorCheck, 0, len(services))
	for _, service := range services {
		start := time.Now()
		cmdOut, err := runDoctorCommand(ctx, runner, "", opts.Timeout, "systemctl", "show",
			"--property=FragmentPath",
			"--property=WorkingDirectory",
			"--property=User",
			"--property=Restart",
			"--property=NRestarts",
			service.Service,
		)
		check := doctorCheck{Name: "service unit " + service.Service, DurationMS: time.Since(start).Milliseconds()}
		if isCommandMissing(err) {
			check.Status = "skip"
			check.Detail = "systemctl unavailable"
			out = append(out, check)
			continue
		}
		if err != nil {
			check.Status = "warn"
			check.Detail = commandErrorDetail(cmdOut, err)
			out = append(out, check)
			continue
		}
		props := parseSystemctlProperties(cmdOut)
		if len(props) == 0 {
			check.Status = "warn"
			check.Detail = "empty unit metadata"
			out = append(out, check)
			continue
		}
		check.Status = "ok"
		check.Detail = formatSystemctlUnitSummary(props)
		out = append(out, check)
	}
	return out
}

func doctorServiceJournalChecks(ctx context.Context, opts doctorOptions, runner doctorCommandRunner, services []serviceops.ManagedService) []doctorCheck {
	out := make([]doctorCheck, 0, len(services))
	for _, service := range services {
		start := time.Now()
		cmdOut, err := runDoctorCommand(ctx, runner, "", opts.Timeout, "journalctl",
			"--unit", service.Service,
			"--since", "-1 hour",
			"--no-pager",
			"--lines", "200",
		)
		check := doctorCheck{Name: "service journal " + service.Service, DurationMS: time.Since(start).Milliseconds()}
		if isCommandMissing(err) {
			check.Status = "skip"
			check.Detail = "journalctl unavailable"
			out = append(out, check)
			continue
		}
		if err != nil {
			check.Status = "warn"
			check.Detail = commandErrorDetail(cmdOut, err)
			out = append(out, check)
			continue
		}
		count := countCrashLoopSignals(cmdOut)
		if count > 0 {
			check.Status = "fail"
			check.Detail = fmt.Sprintf("%d recent crash/error signal(s) in journal", count)
		} else {
			check.Status = "ok"
			check.Detail = "no recent crash/error signals"
		}
		out = append(out, check)
	}
	return out
}

func parseSystemctlProperties(out string) map[string]string {
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			props[key] = value
		}
	}
	return props
}

func formatSystemctlUnitSummary(props map[string]string) string {
	parts := []string{}
	for _, key := range []string{"FragmentPath", "WorkingDirectory", "User", "Restart", "NRestarts"} {
		if value := strings.TrimSpace(props[key]); value != "" {
			parts = append(parts, strings.ToLower(key)+"="+value)
		}
	}
	if len(parts) == 0 {
		return "no selected unit metadata"
	}
	return strings.Join(parts, " ")
}

func countCrashLoopSignals(out string) int {
	count := 0
	for _, line := range strings.Split(out, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "panic:") ||
			strings.Contains(lower, "fatal") ||
			strings.Contains(lower, "segmentation fault") ||
			strings.Contains(lower, "main process exited") ||
			strings.Contains(lower, "restart counter") ||
			strings.Contains(lower, "failed with result") {
			count++
		}
	}
	return count
}

func doctorMatchingProcessLines(out string, subcommand string) []string {
	var matches []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !doctorProcessLineMatchesSubcommand(line, subcommand) {
			continue
		}
		matches = append(matches, line)
	}
	return matches
}

func doctorProcessLineMatchesSubcommand(line string, subcommand string) bool {
	fields := strings.Fields(line)
	for i := 1; i < len(fields); i++ {
		if filepath.Base(strings.Trim(fields[i], `"'`)) != "fast-agent-harness" {
			continue
		}
		next := i + 1
		if next < len(fields) && fields[next] == "(deleted)" {
			next++
		}
		if next < len(fields) && fields[next] == subcommand {
			return true
		}
	}
	return false
}

func doctorProcessExists(pid int) bool {
	return doctorProcessExistsOS(pid)
}

func doctorProcessCmdline(pid int) string {
	bytes, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(string(bytes), "\x00"), "\x00")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func doctorGatewayStatuses(ctx context.Context, cfg config.Config, opts doctorOptions) []doctorCheck {
	if !opts.CheckGateway {
		return []doctorCheck{
			{Name: "gateway /health", Status: "skip", Detail: "disabled"},
			{Name: "gateway /ready", Status: "skip", Detail: "disabled"},
		}
	}
	return []doctorCheck{
		doctorGatewayEndpointStatus(ctx, cfg, opts, "/health", "gateway /health"),
		doctorGatewayEndpointStatus(ctx, cfg, opts, "/ready", "gateway /ready"),
	}
}

func doctorGatewayEndpointStatus(ctx context.Context, cfg config.Config, opts doctorOptions, path string, name string) doctorCheck {
	start := time.Now()
	lastDetail := ""
	for _, candidate := range gatewayURLCandidates(cfg) {
		status, detail, reached := doctorProbeGatewayEndpoint(ctx, candidate, path, opts.Timeout)
		if reached {
			return doctorCheck{Name: name, Status: status, Detail: detail, DurationMS: time.Since(start).Milliseconds()}
		}
		lastDetail = detail
	}
	if lastDetail == "" {
		lastDetail = "no local gateway candidates"
	}
	return doctorCheck{Name: name, Status: "fail", Detail: "no reachable local gateway found: " + lastDetail, DurationMS: time.Since(start).Milliseconds()}
}

func doctorProbeGatewayEndpoint(ctx context.Context, baseURL string, path string, timeout time.Duration) (string, string, bool) {
	baseURL = gatewayapi.NormalizeBaseURL(baseURL)
	if baseURL == "" {
		return "fail", "empty gateway URL", false
	}
	if timeout <= 0 || timeout > 2*time.Second {
		timeout = 2 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return "fail", err.Error(), false
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return "fail", baseURL + path + ": " + err.Error(), false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	detail := gatewayEndpointDetail(baseURL, path, resp.StatusCode, body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "fail", detail, true
	}
	if path == "/ready" {
		var ready gatewayapi.ReadinessResponse
		if err := json.Unmarshal(body, &ready); err == nil && !ready.OK {
			return "fail", detail, true
		}
	}
	return "ok", detail, true
}

func gatewayEndpointDetail(baseURL string, path string, statusCode int, body []byte) string {
	prefix := fmt.Sprintf("%s%s status=%d", baseURL, path, statusCode)
	switch path {
	case "/ready":
		var ready gatewayapi.ReadinessResponse
		if err := json.Unmarshal(body, &ready); err == nil {
			warn, fail := readinessCheckCounts(ready.Checks)
			return fmt.Sprintf("%s ok=%v checks=%d warn=%d fail=%d", prefix, ready.OK, len(ready.Checks), warn, fail)
		}
	case "/health":
		var health gatewayapi.HealthResponse
		if err := json.Unmarshal(body, &health); err == nil {
			return fmt.Sprintf("%s ok=%v provider=%s model=%s", prefix, health.OK, health.Provider, health.Model)
		}
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return prefix
	}
	return prefix + " body=" + firstLines(text, 2)
}

func readinessCheckCounts(checks []gatewayapi.ReadinessCheck) (warn int, fail int) {
	for _, check := range checks {
		switch strings.ToLower(strings.TrimSpace(check.Status)) {
		case "warn":
			warn++
		case "fail":
			fail++
		}
	}
	return warn, fail
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
	fmt.Fprintf(w, "build: commit=%s built_at=%s\n", report.BuildCommit, report.BuildTime)
	fmt.Fprintf(w, "mode: %s\n", report.Mode)
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
	fmt.Fprintf(w, "config provenance: provider=%s model=%s context_window=%s compact=%s helper_model=%s web_backend=%s/%s\n",
		diagnosticSourceSummary(report.Config.ProviderSource),
		diagnosticSourceSummary(report.Config.ModelSource),
		diagnosticSourceSummary(report.Config.ContextWindowTokensSource),
		diagnosticSourceSummary(report.Config.ContextCompactTokensSource),
		diagnosticSourceSummary(report.Config.WebSummaryModelSource),
		diagnosticSourceSummary(report.Config.WebSearchBackendSource),
		diagnosticSourceSummary(report.Config.WebExtractBackendSource),
	)
	fmt.Fprintf(w, "capability: provider=%s model=%s known=%v context=%d max_output=%d tools=%v parallel=%v streaming=%v reasoning=%v cost=%s validation=%s\n",
		report.Config.ProviderCapability.Provider,
		report.Config.ProviderCapability.Model,
		report.Config.ProviderCapability.Known,
		report.Config.ProviderCapability.ContextWindowTokens,
		report.Config.ProviderCapability.MaxOutputTokens,
		report.Config.ProviderCapability.ToolCalls,
		report.Config.ProviderCapability.ParallelToolCalls,
		report.Config.ProviderCapability.Streaming,
		report.Config.ProviderCapability.Reasoning,
		report.Config.ProviderCapability.CostMode,
		doctorCapabilityValidationSummary(report.Config.ProviderCapability),
	)
	fmt.Fprintf(w, "runtime: provider=%s model=%s gateway=%s strict_hygiene=%s service_binary=%s age=%s sessions=%s tool_output=%s attachments=%s\n",
		report.Runtime.Provider,
		report.Runtime.Model,
		report.Runtime.GatewayURL,
		report.Runtime.StrictHygiene.Status,
		doctorFileSummary(report.Runtime.ServiceBinary),
		doctorAgeSummary(report.Runtime.ServiceBinary.AgeSeconds),
		doctorPathUsageSummary(report.Runtime.GatewaySessionStore),
		doctorPathUsageSummary(report.Runtime.ToolOutputStore),
		doctorPathUsageSummary(report.Runtime.AttachmentsStore),
	)
	fmt.Fprintf(w, "auth: provider=%s model=%s cost_mode=%s api_key_env=%s credential_file=%s codex_auth=%s\n",
		report.Runtime.Auth.Provider,
		report.Runtime.Auth.Model,
		report.Runtime.Auth.CostMode,
		presenceSummary(report.Runtime.Auth.APIKeyEnv, report.Runtime.Auth.APIKeyEnvSet),
		presenceSummary(report.Runtime.Auth.CredentialFile, report.Runtime.Auth.CredentialFileExists),
		presenceSummary(report.Runtime.Auth.CodexAuthFile, report.Runtime.Auth.CodexAuthFileExists),
	)
	fmt.Fprintf(w, "auth status:\n%s\n", indentLines(credentials.FormatStatusText(credentials.Status{
		DeepSeek:       report.Runtime.Auth.DeepSeek,
		Codex:          report.Runtime.Auth.Codex,
		ActiveProvider: report.Runtime.Auth.Provider,
		ActiveModel:    report.Runtime.Auth.Model,
		CostMode:       report.Runtime.Auth.CostMode,
	}), "  "))
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

func indentLines(text, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
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

func doctorCapabilityValidationSummary(caps config.ProviderCapabilitySnapshot) string {
	if strings.TrimSpace(caps.ValidationError) == "" {
		return "ok"
	}
	return caps.ValidationError
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

func firstStringItems(values []string, max int) []string {
	if max <= 0 || len(values) <= max {
		return values
	}
	out := append([]string{}, values[:max]...)
	out = append(out, "...")
	return out
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

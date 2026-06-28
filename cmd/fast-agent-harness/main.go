package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/bench"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/mcpserver"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/telegrambot"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
	"github.com/billyhargroveofficial/billyharness/internal/tui"
)

var version = "0.1.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return serve(nil)
	}
	switch os.Args[1] {
	case "run":
		return runOnce(os.Args[2:])
	case "chat":
		return chat(os.Args[2:])
	case "tui":
		return tuiCmd(os.Args[2:])
	case "telegram":
		return telegramCmd(os.Args[2:])
	case "serve", "gateway":
		return serve(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return nil
	case "mcp":
		return mcp(os.Args[2:])
	case "config":
		return configCommand(os.Args[2:], os.Stdout)
	case "bench":
		return benchCmd(os.Args[2:])
	case "sessions", "session":
		return sessionsCmd(os.Args[2:])
	case "tools":
		return printTools()
	case "doctor", "health":
		return doctorCmd(os.Args[2:])
	default:
		usage()
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}

func runOnce(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	mock := fs.Bool("mock", false, "use mock provider")
	model := fs.String("model", "", "model override")
	profile := fs.String("profile", "", "system profile override")
	gatewayURL := fs.String("gateway", "", "gateway base URL, for example http://127.0.0.1:8765")
	noReasoning := fs.Bool("hide-reasoning", true, "do not print reasoning deltas")
	if err := fs.Parse(args); err != nil {
		return err
	}
	prompt := strings.Join(fs.Args(), " ")
	if prompt == "" {
		return fmt.Errorf("prompt required")
	}
	if *gatewayURL != "" {
		req := gateway.RunRequest{Prompt: prompt, Model: *model, Profile: config.NormalizeProfileName(*profile)}
		if *mock {
			req.Provider = "mock"
			req.Model = "mock"
		}
		return gatewayRun(context.Background(), *gatewayURL, "/v1/run", req, terminalEmitter(*noReasoning))
	}
	cfg := config.Default()
	if *mock {
		cfg.Provider = "mock"
		cfg.Model = "mock"
	}
	if *model != "" {
		cfg.Model = *model
	}
	if *profile != "" {
		cfg.Profile = config.NormalizeProfileName(*profile)
	}
	cfg.ApplyModelProviderDefaults()
	prov, err := provider.New(cfg)
	if err != nil {
		return err
	}
	registry, err := tools.NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer registry.Close()
	a := agent.New(cfg, prov, registry)
	return a.Run(context.Background(), prompt, terminalEmitter(*noReasoning))
}

func chat(args []string) error {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	mock := fs.Bool("mock", false, "use mock provider")
	model := fs.String("model", "", "model override")
	profile := fs.String("profile", "", "system profile override")
	gatewayURL := fs.String("gateway", "", "gateway base URL, for example http://127.0.0.1:8765")
	noReasoning := fs.Bool("hide-reasoning", true, "do not print reasoning deltas")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *gatewayURL != "" {
		return chatGateway(*gatewayURL, *noReasoning, *model, *profile, *mock)
	}
	cfg := config.Default()
	if *mock {
		cfg.Provider = "mock"
		cfg.Model = "mock"
	}
	if *model != "" {
		cfg.Model = *model
	}
	if *profile != "" {
		cfg.Profile = config.NormalizeProfileName(*profile)
	}
	cfg.ApplyModelProviderDefaults()
	prov, err := provider.New(cfg)
	if err != nil {
		return err
	}
	registry, err := tools.NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer registry.Close()
	a := agent.New(cfg, prov, registry)
	messages := agent.InitialMessages(cfg)
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Fprintln(os.Stderr, "fast-agent-harness chat. Type /exit or press Ctrl-D to quit.")
	for {
		fmt.Fprint(os.Stderr, "> ")
		if !scanner.Scan() {
			fmt.Fprintln(os.Stderr)
			return scanner.Err()
		}
		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" {
			continue
		}
		if prompt == "/exit" || prompt == "/quit" {
			return nil
		}
		messages = append(messages, protocol.Message{Role: protocol.RoleUser, Content: prompt})
		messages, err = a.RunMessages(context.Background(), messages, terminalEmitter(*noReasoning))
		if err != nil {
			return err
		}
	}
}

func tuiCmd(args []string) error {
	cfg := config.Default()
	fs := flag.NewFlagSet("tui", flag.ExitOnError)
	gatewayURL := fs.String("gateway", "", "gateway base URL override; auto-discovered when omitted")
	model := fs.String("model", "", "initial model: deepseek-v4-flash or deepseek-v4-pro")
	dangerous := fs.Bool("dangerous", true, "enable write and shell tools for local TUI runs")
	maxRounds := fs.Int("max-rounds", 0, "max model/tool rounds per request; 0 uses config default")
	plain := fs.Bool("plain", false, "compatibility mode for SSH/dumb terminals: no alt-screen, mouse, or bracketed paste")
	if err := fs.Parse(args); err != nil {
		return err
	}
	gatewayNotice := ""
	if strings.TrimSpace(*gatewayURL) == "" {
		if discovered, ok := discoverGatewayURL(context.Background(), cfg); ok {
			*gatewayURL = discovered
		} else {
			target := normalizeGatewayURL(cfg.GatewayAddr)
			if candidates := gatewayURLCandidates(cfg); len(candidates) > 0 {
				target = candidates[0]
			}
			gatewayNotice = gateway.UnavailableHint(target) + "; local mode active"
		}
	} else {
		*gatewayURL = normalizeGatewayURL(*gatewayURL)
	}
	return tui.Run(tui.Options{
		GatewayURL:    *gatewayURL,
		GatewayNotice: gatewayNotice,
		Model:         *model,
		Dangerous:     *dangerous,
		MaxRounds:     *maxRounds,
		Plain:         *plain,
		Version:       version,
	})
}

func telegramCmd(args []string) error {
	cfg := config.Default()
	fs := flag.NewFlagSet("telegram", flag.ExitOnError)
	gatewayURL := fs.String("gateway", "", "gateway base URL override; auto-discovered when omitted")
	token := fs.String("token", "", "Telegram bot token; defaults to TELEGRAM_BOT_TOKEN from env or .env")
	botAPIBase := fs.String("bot-api-base", lookupEnvAny("BILLYHARNESS_TELEGRAM_BOT_API_BASE_URL", "TELEGRAM_BOT_API_BASE_URL"), "Telegram Bot API base URL")
	model := fs.String("model", cfg.Model, "initial model for new Telegram chats")
	profile := fs.String("profile", cfg.Profile, "system profile for new Telegram chats")
	reasoning := fs.String("reasoning", cfg.ReasoningEffort, "initial reasoning effort")
	statePath := fs.String("state", telegrambot.DefaultStatePath(), "Telegram gateway state JSON path")
	allowedRaw := fs.String("allow-chat", lookupEnvAny("BILLYHARNESS_TELEGRAM_ALLOWED_CHAT_IDS", "TELEGRAM_ALLOWED_CHAT_IDS"), "comma-separated allowed Telegram chat IDs")
	allowedUsersRaw := fs.String("allow-user", lookupEnvAny("BILLYHARNESS_TELEGRAM_ALLOWED_USER_IDS", "TELEGRAM_ALLOWED_USER_IDS"), "comma-separated allowed Telegram user IDs")
	requireAllowlist := fs.Bool("require-allowlist", envBoolAnyDefault(false, "BILLYHARNESS_TELEGRAM_REQUIRE_ALLOWLIST", "TELEGRAM_REQUIRE_ALLOWLIST"), "reject chats not listed in -allow-chat")
	allowAllChats := fs.Bool("allow-all-chats", envBoolAnyDefault(false, "BILLYHARNESS_TELEGRAM_ALLOW_ALL_CHATS", "TELEGRAM_ALLOW_ALL_CHATS"), "allow every Telegram chat; unsafe for live bots")
	sendEnabled := fs.Bool("send-enabled", envBoolAnyDefault(true, "BILLYHARNESS_TELEGRAM_SEND_ENABLED", "TELEGRAM_SEND_ENABLED"), "actually send Telegram messages")
	dryRun := fs.Bool("dry-run", envBoolAnyDefault(false, "BILLYHARNESS_TELEGRAM_DRY_RUN", "TELEGRAM_DRY_RUN"), "log Telegram sends without sending")
	pollTimeout := fs.Int("poll-timeout-sec", envIntAnyDefault(30, "BILLYHARNESS_TELEGRAM_POLL_TIMEOUT_SEC", "TELEGRAM_POLL_TIMEOUT_SEC"), "Telegram long poll timeout")
	editIntervalMS := fs.Int("edit-interval-ms", envIntAnyDefault(700, "BILLYHARNESS_TELEGRAM_EDIT_INTERVAL_MS", "TELEGRAM_EDIT_INTERVAL_MS"), "minimum interval between live edits per message")
	maxRounds := fs.Int("max-rounds", cfg.MaxToolRounds, "max model/tool rounds per Telegram request")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*token) == "" {
		*token = lookupEnvAny("BILLYHARNESS_TELEGRAM_BOT_TOKEN", "TELEGRAM_BOT_TOKEN")
	}
	if strings.TrimSpace(*token) == "" {
		return fmt.Errorf("Telegram bot token required; set TELEGRAM_BOT_TOKEN in %s/.env or pass -token", config.BillyHomeDir())
	}
	if strings.TrimSpace(*gatewayURL) == "" {
		if discovered, ok := discoverGatewayURL(context.Background(), cfg); ok {
			*gatewayURL = discovered
		} else {
			*gatewayURL = normalizeGatewayURL(cfg.GatewayAddr)
		}
	} else {
		*gatewayURL = normalizeGatewayURL(*gatewayURL)
	}
	allowed, err := parseChatIDs(*allowedRaw)
	if err != nil {
		return err
	}
	allowedUsers, err := parseChatIDs(*allowedUsersRaw)
	if err != nil {
		return err
	}
	effectiveRequireAllowlist := *requireAllowlist
	if *allowAllChats {
		effectiveRequireAllowlist = false
	}
	if *sendEnabled && !*dryRun && len(allowed) == 0 && len(allowedUsers) == 0 && !*allowAllChats {
		return fmt.Errorf("Telegram live send requires -allow-chat, -allow-user, or -allow-all-chats")
	}
	opts := telegrambot.Options{
		BotToken:         *token,
		BotAPIBaseURL:    *botAPIBase,
		GatewayURL:       *gatewayURL,
		StatePath:        *statePath,
		Model:            modelAliasForTelegram(*model),
		Profile:          config.NormalizeProfileName(*profile),
		ReasoningEffort:  strings.ToLower(strings.TrimSpace(*reasoning)),
		MaxToolRounds:    *maxRounds,
		ContextWindow:    cfg.ContextWindowTokens,
		PollTimeoutSec:   *pollTimeout,
		EditInterval:     time.Duration(*editIntervalMS) * time.Millisecond,
		AllowedChatIDs:   allowed,
		AllowedUserIDs:   allowedUsers,
		AllowAllChats:    *allowAllChats,
		SendEnabled:      *sendEnabled,
		DryRunDefault:    *dryRun,
		RequireAllowlist: effectiveRequireAllowlist,
	}
	bot, err := telegrambot.New(opts, nil, nil)
	if err != nil {
		return err
	}
	ctx, stop := processContext()
	defer stop()
	fmt.Fprintln(os.Stderr, "billyharness telegram gateway polling; gateway="+*gatewayURL)
	if err := bot.Run(ctx); errors.Is(err, context.Canceled) {
		return nil
	} else {
		return err
	}
}

func discoverGatewayURL(ctx context.Context, cfg config.Config) (string, bool) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, baseURL := range gatewayURLCandidates(cfg) {
			if gateway.WaitForReady(ctx, baseURL, 0) {
				return baseURL, true
			}
		}
		if time.Now().After(deadline) {
			return "", false
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", false
		case <-timer.C:
		}
	}
}

func gatewayURLCandidates(cfg config.Config) []string {
	var raw []string
	if value := strings.TrimSpace(os.Getenv("FAST_AGENT_GATEWAY_URL")); value != "" {
		raw = append(raw, value)
	}
	if value := strings.TrimSpace(os.Getenv("BILLYHARNESS_GATEWAY_URL")); value != "" {
		raw = append(raw, value)
	}
	if cfg.GatewayAddr != "" {
		raw = append(raw, cfg.GatewayAddr)
	}
	raw = append(raw, "127.0.0.1:8765", "localhost:8765")

	seen := map[string]bool{}
	var out []string
	for _, item := range raw {
		url := normalizeGatewayURL(item)
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		out = append(out, url)
	}
	return out
}

func normalizeGatewayURL(value string) string {
	return gateway.NormalizeBaseURL(value)
}

func lookupEnvAny(keys ...string) string {
	for _, key := range keys {
		if value, ok := config.LookupEnvOrDotenv(key); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func envBoolAnyDefault(fallback bool, keys ...string) bool {
	for _, key := range keys {
		if value := lookupEnvAny(key); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err == nil {
				return parsed
			}
		}
	}
	return fallback
}

func envIntAnyDefault(fallback int, keys ...string) int {
	for _, key := range keys {
		if value := lookupEnvAny(key); value != "" {
			parsed, err := strconv.Atoi(value)
			if err == nil {
				return parsed
			}
		}
	}
	return fallback
}

func parseChatIDs(raw string) (map[int64]bool, error) {
	out := map[int64]bool{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	}) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid Telegram chat id %q", part)
		}
		out[id] = true
	}
	return out, nil
}

func modelAliasForTelegram(value string) string {
	return modelinfo.NormalizeAlias(value)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	mock := fs.Bool("mock", false, "use mock provider")
	model := fs.String("model", "", "model override")
	addr := fs.String("addr", "", "listen address")
	authToken := fs.String("auth-token", "", "gateway bearer token for non-loopback clients; defaults to BILLYHARNESS_GATEWAY_AUTH_TOKEN")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg := config.Default()
	if *mock {
		cfg.Provider = "mock"
		cfg.Model = "mock"
	}
	if *model != "" {
		cfg.Model = *model
	}
	cfg.ApplyModelProviderDefaults()
	if *addr == "" {
		*addr = cfg.GatewayAddr
	}
	if strings.TrimSpace(*authToken) == "" {
		*authToken = gateway.AuthTokenFromEnv()
	}
	*authToken = strings.TrimSpace(*authToken)
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", *addr, err)
	}
	defer listener.Close()
	authRequired := gateway.RequiresAuthForAddr(listener.Addr().String())
	if authRequired && *authToken == "" {
		return fmt.Errorf("gateway auth token required for non-loopback listen address %q; set %s or use -addr 127.0.0.1:8765 for local-only access", *addr, gateway.GatewayAuthTokenEnv)
	}
	ctx, stop := processContext()
	defer stop()
	registry, err := tools.NewRegistryWithMCP(ctx, cfg)
	if err != nil {
		return err
	}
	defer registry.Close()
	server := gateway.NewServerWithOptions(cfg, provider.Mock{}, registry, gateway.ServerOptions{
		AuthToken:       *authToken,
		SessionStoreDir: gateway.DefaultSessionStoreDir(),
	})
	listenURL := normalizeGatewayURL(listener.Addr().String())
	status := "fast-agent-harness gateway listening on " + listenURL
	if authRequired {
		status += "; bearer auth required for non-loopback clients"
	}
	fmt.Fprintln(os.Stderr, status)
	if err := server.Serve(ctx, listener); errors.Is(err, context.Canceled) {
		return nil
	} else {
		return err
	}
}

func processContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func mcp(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg := config.Default()
	registry := tools.NewRegistry(cfg)
	server := mcpserver.New(registry)
	return server.Serve(context.Background(), os.Stdin, os.Stdout)
}

func configCommand(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		configUsage(stdout)
		return nil
	}
	switch args[0] {
	case "inspect", "status":
		return configInspectCommand(args[1:], stdout)
	default:
		configUsage(stdout)
		return fmt.Errorf("unknown config command %q", args[0])
	}
}

func configInspectCommand(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("config inspect", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "print resolved config as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolved, err := config.Resolve()
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Config   map[string]any         `json:"config"`
			Values   []config.ResolvedValue `json:"values"`
			Warnings []string               `json:"warnings,omitempty"`
		}{
			Config:   resolved.SanitizedConfig(),
			Values:   resolved.SanitizedValues(),
			Warnings: resolved.Warnings,
		})
	}
	fmt.Fprintf(stdout, "billyharness config inspect\n")
	fmt.Fprintf(stdout, "provider=%s model=%s profile=%s reasoning=%s/%s gateway=%s\n",
		resolved.Config.Provider,
		resolved.Config.Model,
		resolved.Config.Profile,
		resolved.Config.Thinking,
		resolved.Config.ReasoningEffort,
		resolved.Config.GatewayAddr,
	)
	fmt.Fprintf(stdout, "%-34s  %-28s  %-26s  %s\n", "key", "value", "source", "source key/path")
	fmt.Fprintf(stdout, "%-34s  %-28s  %-26s  %s\n", strings.Repeat("-", 34), strings.Repeat("-", 28), strings.Repeat("-", 26), strings.Repeat("-", 24))
	for _, value := range resolved.SanitizedValues() {
		location := value.SourceKey
		if value.SourcePath != "" {
			location = location + " @ " + value.SourcePath
		}
		if value.Warning != "" {
			location = strings.TrimSpace(location + " warning=" + value.Warning)
		}
		if value.Error != "" {
			location = strings.TrimSpace(location + " error=" + value.Error)
		}
		fmt.Fprintf(stdout, "%-34s  %-28s  %-26s  %s\n",
			value.Key,
			truncateConfigInspectValue(fmt.Sprint(value.Value), 28),
			value.Source,
			location,
		)
	}
	for _, warning := range resolved.Warnings {
		fmt.Fprintf(stdout, "warning: %s\n", warning)
	}
	return nil
}

func configUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: fast-agent-harness config inspect [-json]")
}

func truncateConfigInspectValue(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxLen {
		return value
	}
	if maxLen <= 3 {
		return value[:maxLen]
	}
	return value[:maxLen-3] + "..."
}

func benchCmd(args []string) error {
	if len(args) == 0 {
		benchUsage()
		return nil
	}
	switch args[0] {
	case "run":
		return benchRunCmd(args[1:])
	case "terminal-bench", "tb":
		return benchTerminalBenchCmd(args[1:])
	default:
		benchUsage()
		return fmt.Errorf("unknown bench command %q", args[0])
	}
}

func benchRunCmd(args []string) error {
	fs := flag.NewFlagSet("bench run", flag.ExitOnError)
	tasksPath := fs.String("tasks", "", "JSONL task file")
	outDir := fs.String("out", "bench-runs", "output directory for JSONL traces")
	limit := fs.Int("limit", 0, "max tasks to run")
	mock := fs.Bool("mock", false, "use mock provider")
	model := fs.String("model", "", "model override")
	timeoutSec := fs.Int("timeout-sec", 0, "per-task timeout override")
	maxRounds := fs.Int("max-rounds", 100, "max model/tool rounds per task")
	allowDangerous := fs.Bool("dangerous", false, "enable write and shell tools for benchmark tasks")
	scriptedRounds := fs.Int("scripted-rounds", 0, "mock-only scripted tool rounds for loop/compaction stress")
	contextCompactTokens := fs.Int("context-compact-tokens", 0, "override context compaction trigger tokens")
	contextCompactKeep := fs.Int("context-compact-keep", 0, "override context compaction keep count")
	contextCompactMaxChars := fs.Int("context-compact-max-chars", 0, "override context compaction summary max chars")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tasksPath == "" {
		return fmt.Errorf("-tasks required")
	}
	cfg := config.Default()
	cfg.MaxToolRounds = *maxRounds
	cfg.StoreReasoningContent = true
	if *allowDangerous {
		cfg.AutoApproveDangerous = true
	}
	rc := bench.RunConfig{
		TasksPath:              *tasksPath,
		OutDir:                 *outDir,
		Limit:                  *limit,
		Mock:                   *mock,
		Model:                  *model,
		ScriptedToolRounds:     *scriptedRounds,
		ContextCompactTokens:   *contextCompactTokens,
		ContextCompactKeep:     *contextCompactKeep,
		ContextCompactMaxChars: *contextCompactMaxChars,
	}
	if *timeoutSec > 0 {
		rc.Timeout = time.Duration(*timeoutSec) * time.Second
	}
	cfg.ApplyModelProviderDefaults()
	summary, err := bench.Run(context.Background(), cfg, rc)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(summary)
}

func benchTerminalBenchCmd(args []string) error {
	if len(args) == 0 {
		benchTerminalBenchUsage()
		return nil
	}
	switch args[0] {
	case "export":
		return benchTerminalBenchExportCmd(args[1:])
	case "import":
		return benchTerminalBenchImportCmd(args[1:])
	default:
		benchTerminalBenchUsage()
		return fmt.Errorf("unknown bench terminal-bench command %q", args[0])
	}
}

func benchTerminalBenchExportCmd(args []string) error {
	fs := flag.NewFlagSet("bench terminal-bench export", flag.ExitOnError)
	tasksPath := fs.String("tasks", "", "billyharness JSONL task file")
	outDir := fs.String("out", "benchmarks/terminal-bench-export", "Terminal-Bench dataset output directory")
	force := fs.Bool("force", false, "replace an existing Terminal-Bench dataset output directory")
	authorName := fs.String("author-name", "billyharness", "Terminal-Bench author_name")
	authorEmail := fs.String("author-email", "unknown", "Terminal-Bench author_email")
	difficulty := fs.String("difficulty", "unknown", "Terminal-Bench difficulty: easy, medium, hard, or unknown")
	category := fs.String("category", "software_engineering", "Terminal-Bench category")
	maxTestTimeoutSec := fs.Int("max-test-timeout-sec", 60, "Terminal-Bench max_test_timeout_sec")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tasksPath == "" {
		return fmt.Errorf("-tasks required")
	}
	summary, err := bench.ExportTerminalBenchDataset(bench.TerminalBenchExportOptions{
		TasksPath:             *tasksPath,
		OutDir:                *outDir,
		Force:                 *force,
		AuthorName:            *authorName,
		AuthorEmail:           *authorEmail,
		Difficulty:            *difficulty,
		Category:              *category,
		MaxTestTimeoutSeconds: *maxTestTimeoutSec,
	})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(summary)
}

func benchTerminalBenchImportCmd(args []string) error {
	fs := flag.NewFlagSet("bench terminal-bench import", flag.ExitOnError)
	datasetDir := fs.String("dataset", "", "Terminal-Bench dataset directory")
	outPath := fs.String("out", "", "billyharness JSONL task output; stdout when omitted")
	suite := fs.String("suite", bench.TerminalBenchSuite, "suite for generic Terminal-Bench imports")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *datasetDir == "" {
		return fmt.Errorf("-dataset required")
	}
	tasks, err := bench.ImportTerminalBenchDataset(bench.TerminalBenchImportOptions{
		DatasetDir: *datasetDir,
		Suite:      *suite,
	})
	if err != nil {
		return err
	}
	if *outPath == "" {
		return bench.EncodeTasksJSONL(os.Stdout, tasks)
	}
	return bench.WriteTasksJSONL(*outPath, tasks)
}

func benchUsage() {
	fmt.Println("usage:")
	fmt.Println("  fast-agent-harness bench run -tasks tasks.jsonl -out runs")
	fmt.Println("  fast-agent-harness bench terminal-bench export -tasks tasks.jsonl -out tb-dataset")
	fmt.Println("  fast-agent-harness bench terminal-bench import -dataset tb-dataset [-out tasks.jsonl]")
}

func benchTerminalBenchUsage() {
	fmt.Println("usage:")
	fmt.Println("  fast-agent-harness bench terminal-bench export -tasks tasks.jsonl -out tb-dataset")
	fmt.Println("  fast-agent-harness bench terminal-bench import -dataset tb-dataset [-out tasks.jsonl]")
}

func chatGateway(baseURL string, noReasoning bool, model, profile string, mock bool) error {
	baseURL = normalizeGatewayURL(baseURL)
	profile = config.NormalizeProfileName(profile)
	sessionID, err := gatewayCreateSession(context.Background(), baseURL, profile)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Fprintln(os.Stderr, "fast-agent-harness gateway chat. Session "+sessionID+". Type /exit or press Ctrl-D to quit.")
	for {
		fmt.Fprint(os.Stderr, "> ")
		if !scanner.Scan() {
			fmt.Fprintln(os.Stderr)
			return scanner.Err()
		}
		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" {
			continue
		}
		if prompt == "/exit" || prompt == "/quit" {
			return nil
		}
		path := "/v1/sessions/" + sessionID + "/run"
		req := gateway.RunRequest{Prompt: prompt, Model: model, Profile: profile}
		if mock {
			req.Provider = "mock"
			req.Model = "mock"
		}
		if err := gatewayRun(context.Background(), baseURL, path, req, terminalEmitter(noReasoning)); err != nil {
			return err
		}
	}
}

func gatewayCreateSession(ctx context.Context, baseURL, profile string) (string, error) {
	baseURL = normalizeGatewayURL(baseURL)
	body, err := json.Marshal(gateway.CreateSessionRequest{Profile: profile})
	if err != nil {
		return "", err
	}
	resp, err := gateway.DoWithReadyRetry(ctx, http.DefaultClient, baseURL, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/sessions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		gateway.SetAuthHeaderFromEnv(req)
		return req, nil
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("gateway returned empty session id")
	}
	return out.ID, nil
}

func gatewayRun(ctx context.Context, baseURL, path string, runReq gateway.RunRequest, emit func(protocol.Event)) error {
	baseURL = normalizeGatewayURL(baseURL)
	body, _ := json.Marshal(runReq)
	resp, err := gateway.DoWithReadyRetry(ctx, http.DefaultClient, baseURL, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		gateway.SetAuthHeaderFromEnv(req)
		return req, nil
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var event protocol.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return err
		}
		emit(event)
		if event.Type == protocol.EventRunFailed {
			return fmt.Errorf("%v", event.Data)
		}
	}
	return scanner.Err()
}

func terminalEmitter(noReasoning bool) func(protocol.Event) {
	return func(event protocol.Event) {
		if event.Type == protocol.EventAssistantReasoning && noReasoning {
			return
		}
		if event.Type == protocol.EventAssistantDelta {
			fmt.Print(event.Data)
			return
		}
		if event.Type == protocol.EventRunCompleted {
			fmt.Println()
			return
		}
		bytes, _ := json.Marshal(event)
		if strings.HasPrefix(string(event.Type), "tool.") {
			fmt.Fprintln(os.Stderr, string(bytes))
		}
	}
}

func printTools() error {
	cfg := config.Default()
	registry, err := tools.NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer registry.Close()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(registry.Specs())
}

func usage() {
	fmt.Println("fast-agent-harness-go")
	fmt.Println("default:")
	fmt.Println("  fast-agent-harness                 start gateway using billyharness config")
	fmt.Println("commands:")
	fmt.Println("  run [-mock] <prompt>")
	fmt.Println("  run [-gateway http://127.0.0.1:8765] <prompt>")
	fmt.Println("  tui [-gateway http://127.0.0.1:8765] [-model deepseek-v4-flash]")
	fmt.Println("  telegram [-gateway http://127.0.0.1:8765] [-model deepseek-v4-flash]")
	fmt.Println("  chat [-mock]")
	fmt.Println("  chat [-gateway http://127.0.0.1:8765]")
	fmt.Println("  serve|gateway [-mock] [-addr 127.0.0.1:8765]")
	fmt.Println("  mcp")
	fmt.Println("  config inspect [-json]")
	fmt.Println("  sessions list [-dir DIR] [-json]")
	fmt.Println("  sessions inspect [-dir DIR] [-json] SESSION_ID")
	fmt.Println("  sessions index rebuild|show|delete [-dir DIR] [-json]")
	fmt.Println("  bench run -tasks tasks.jsonl -out runs [-model deepseek-v4-flash] [-max-rounds 100]")
	fmt.Println("  bench terminal-bench export -tasks tasks.jsonl -out tb-dataset")
	fmt.Println("  bench terminal-bench import -dataset tb-dataset [-out tasks.jsonl]")
	fmt.Println("  tools")
	fmt.Println("  doctor|health [-json] [-strict] [-build=true] [-services=true] [-gateway=true]")
}

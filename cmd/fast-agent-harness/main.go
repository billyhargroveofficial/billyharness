package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/bench"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/mcpserver"
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
	case "bench":
		return benchCmd(os.Args[2:])
	case "tools":
		return printTools()
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
	fs := flag.NewFlagSet("tui", flag.ExitOnError)
	gatewayURL := fs.String("gateway", "", "gateway base URL override; auto-discovered when omitted")
	model := fs.String("model", "", "initial model: deepseek-v4-flash or deepseek-v4-pro")
	dangerous := fs.Bool("dangerous", true, "enable write and shell tools for local TUI runs")
	maxRounds := fs.Int("max-rounds", 0, "max model/tool rounds per request; 0 uses config default")
	plain := fs.Bool("plain", false, "compatibility mode for SSH/dumb terminals: no alt-screen, mouse, or bracketed paste")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*gatewayURL) == "" {
		if discovered, ok := discoverGatewayURL(context.Background(), config.Default()); ok {
			*gatewayURL = discovered
		}
	}
	return tui.Run(tui.Options{
		GatewayURL: *gatewayURL,
		Model:      *model,
		Dangerous:  *dangerous,
		MaxRounds:  *maxRounds,
		Plain:      *plain,
		Version:    version,
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
	effectiveRequireAllowlist := *requireAllowlist
	if *allowAllChats {
		effectiveRequireAllowlist = false
	}
	if *sendEnabled && !*dryRun && len(allowed) == 0 && !*allowAllChats {
		return fmt.Errorf("Telegram live send requires -allow-chat or -allow-all-chats")
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
		PollTimeoutSec:   *pollTimeout,
		EditInterval:     time.Duration(*editIntervalMS) * time.Millisecond,
		AllowedChatIDs:   allowed,
		AllowAllChats:    *allowAllChats,
		SendEnabled:      *sendEnabled,
		DryRunDefault:    *dryRun,
		RequireAllowlist: effectiveRequireAllowlist,
	}
	bot, err := telegrambot.New(opts, nil, nil)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "billyharness telegram gateway polling; gateway="+*gatewayURL)
	return bot.Run(context.Background())
}

func discoverGatewayURL(ctx context.Context, cfg config.Config) (string, bool) {
	client := http.Client{Timeout: 180 * time.Millisecond}
	for _, baseURL := range gatewayURLCandidates(cfg) {
		reqCtx, cancel := context.WithTimeout(ctx, 220*time.Millisecond)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"/health", nil)
		if err != nil {
			cancel()
			continue
		}
		resp, err := client.Do(req)
		cancel()
		if err != nil {
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return baseURL, true
		}
	}
	return "", false
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
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	if strings.HasPrefix(value, ":") {
		return "http://127.0.0.1" + value
	}
	host, port, err := net.SplitHostPort(value)
	if err == nil {
		host = strings.Trim(host, "[]")
		switch host {
		case "", "0.0.0.0", "::":
			host = "127.0.0.1"
		}
		return "http://" + net.JoinHostPort(host, port)
	}
	return "http://" + value
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
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "flash":
		return "deepseek-v4-flash"
	case "pro":
		return "deepseek-v4-pro"
	case "gpt":
		return "gpt-5.5"
	default:
		return value
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	mock := fs.Bool("mock", false, "use mock provider")
	model := fs.String("model", "", "model override")
	addr := fs.String("addr", "", "listen address")
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
	registry, err := tools.NewRegistryWithMCP(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer registry.Close()
	server := gateway.NewServer(cfg, provider.Mock{}, registry)
	fmt.Fprintln(os.Stderr, "fast-agent-harness gateway listening on http://"+*addr)
	return server.ListenAndServe(context.Background(), *addr)
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

func benchCmd(args []string) error {
	if len(args) == 0 || args[0] != "run" {
		fmt.Println("usage: fast-agent-harness bench run -tasks tasks.jsonl -out runs")
		return nil
	}
	fs := flag.NewFlagSet("bench run", flag.ExitOnError)
	tasksPath := fs.String("tasks", "", "JSONL task file")
	outDir := fs.String("out", "bench-runs", "output directory for JSONL traces")
	limit := fs.Int("limit", 0, "max tasks to run")
	mock := fs.Bool("mock", false, "use mock provider")
	model := fs.String("model", "", "model override")
	timeoutSec := fs.Int("timeout-sec", 0, "per-task timeout override")
	maxRounds := fs.Int("max-rounds", 100, "max model/tool rounds per task")
	allowDangerous := fs.Bool("dangerous", false, "enable write and shell tools for benchmark tasks")
	if err := fs.Parse(args[1:]); err != nil {
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
		TasksPath: *tasksPath,
		OutDir:    *outDir,
		Limit:     *limit,
		Mock:      *mock,
		Model:     *model,
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

func chatGateway(baseURL string, noReasoning bool, model, profile string, mock bool) error {
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
	body, err := json.Marshal(gateway.CreateSessionRequest{Profile: profile})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/sessions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
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
	body, _ := json.Marshal(runReq)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
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
	fmt.Println("  bench run -tasks tasks.jsonl -out runs [-model deepseek-v4-flash] [-max-rounds 100]")
	fmt.Println("  tools")
}

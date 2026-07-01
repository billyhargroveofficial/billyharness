package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/mcpserver"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/telegrambot"
	"github.com/billyhargroveofficial/billyharness/internal/tui"
)

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
	registry, err := newToolRegistry(ctx, cfg)
	if err != nil {
		return err
	}
	defer registry.Close()
	server := gateway.NewServerWithOptionsFromSettings(gateway.ServerSettingsFromConfig(cfg), provider.Mock{}, registry, gateway.ServerOptions{
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
	registry := newToolRegistryNoMCP(cfg)
	server := mcpserver.New(registry)
	return server.Serve(context.Background(), os.Stdin, os.Stdout)
}

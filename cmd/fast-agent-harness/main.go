package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/bench"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/mcpserver"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
	"github.com/billyhargroveofficial/billyharness/internal/tools"
	"github.com/billyhargroveofficial/billyharness/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		usage()
		return nil
	}
	switch os.Args[1] {
	case "run":
		return runOnce(os.Args[2:])
	case "chat":
		return chat(os.Args[2:])
	case "tui":
		return tuiCmd(os.Args[2:])
	case "serve":
		return serve(os.Args[2:])
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
		return gatewayRun(context.Background(), *gatewayURL, "/v1/run", prompt, terminalEmitter(*noReasoning))
	}
	cfg := config.Default()
	if *mock {
		cfg.Provider = "mock"
		cfg.Model = "mock"
	}
	if *model != "" {
		cfg.Model = *model
	}
	prov, err := provider.New(cfg)
	if err != nil {
		return err
	}
	registry := tools.NewRegistry(cfg)
	a := agent.New(cfg, prov, registry)
	return a.Run(context.Background(), prompt, terminalEmitter(*noReasoning))
}

func chat(args []string) error {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	mock := fs.Bool("mock", false, "use mock provider")
	model := fs.String("model", "", "model override")
	gatewayURL := fs.String("gateway", "", "gateway base URL, for example http://127.0.0.1:8765")
	noReasoning := fs.Bool("hide-reasoning", true, "do not print reasoning deltas")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *gatewayURL != "" {
		return chatGateway(*gatewayURL, *noReasoning)
	}
	cfg := config.Default()
	if *mock {
		cfg.Provider = "mock"
		cfg.Model = "mock"
	}
	if *model != "" {
		cfg.Model = *model
	}
	prov, err := provider.New(cfg)
	if err != nil {
		return err
	}
	registry := tools.NewRegistry(cfg)
	a := agent.New(cfg, prov, registry)
	messages := agent.InitialMessages()
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
	gatewayURL := fs.String("gateway", "", "gateway base URL, for example http://127.0.0.1:8765")
	model := fs.String("model", "", "initial model: deepseek-v4-flash or deepseek-v4-pro")
	dangerous := fs.Bool("dangerous", false, "enable write and shell tools for local TUI runs")
	maxRounds := fs.Int("max-rounds", 100, "max model/tool rounds per request")
	plain := fs.Bool("plain", false, "compatibility mode for SSH/dumb terminals: no alt-screen, mouse, or bracketed paste")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return tui.Run(tui.Options{
		GatewayURL: *gatewayURL,
		Model:      *model,
		Dangerous:  *dangerous,
		MaxRounds:  *maxRounds,
		Plain:      *plain,
	})
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
	if *addr == "" {
		*addr = cfg.GatewayAddr
	}
	prov, err := provider.New(cfg)
	if err != nil {
		return err
	}
	registry := tools.NewRegistry(cfg)
	server := gateway.NewServer(cfg, prov, registry)
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
	summary, err := bench.Run(context.Background(), cfg, rc)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(summary)
}

func chatGateway(baseURL string, noReasoning bool) error {
	sessionID, err := gatewayCreateSession(context.Background(), baseURL)
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
		if err := gatewayRun(context.Background(), baseURL, path, prompt, terminalEmitter(noReasoning)); err != nil {
			return err
		}
	}
}

func gatewayCreateSession(ctx context.Context, baseURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/sessions", nil)
	if err != nil {
		return "", err
	}
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

func gatewayRun(ctx context.Context, baseURL, path, prompt string, emit func(protocol.Event)) error {
	body, _ := json.Marshal(map[string]string{"prompt": prompt})
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
	registry := tools.NewRegistry(cfg)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(registry.Specs())
}

func usage() {
	fmt.Println("fast-agent-harness-go")
	fmt.Println("commands:")
	fmt.Println("  run [-mock] <prompt>")
	fmt.Println("  run [-gateway http://127.0.0.1:8765] <prompt>")
	fmt.Println("  tui [-gateway http://127.0.0.1:8765] [-model deepseek-v4-flash] [-dangerous]")
	fmt.Println("  chat [-mock]")
	fmt.Println("  chat [-gateway http://127.0.0.1:8765]")
	fmt.Println("  serve [-mock] [-addr 127.0.0.1:8765]")
	fmt.Println("  mcp")
	fmt.Println("  bench run -tasks tasks.jsonl -out runs [-model deepseek-v4-flash] [-max-rounds 100]")
	fmt.Println("  tools")
}

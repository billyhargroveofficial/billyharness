package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/agent"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/provider"
)

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
	prov, err := provider.NewFromBinding(cfg.ProviderBinding())
	if err != nil {
		return err
	}
	registry, err := newToolRegistry(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer registry.Close()
	a := agent.NewFromSettings(agent.SettingsFromConfig(cfg), prov, registry)
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
	prov, err := provider.NewFromBinding(cfg.ProviderBinding())
	if err != nil {
		return err
	}
	registry, err := newToolRegistry(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer registry.Close()
	a := agent.NewFromSettings(agent.SettingsFromConfig(cfg), prov, registry)
	messages := agent.InitialMessagesFromSettings(cfg.InstructionSettings())
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

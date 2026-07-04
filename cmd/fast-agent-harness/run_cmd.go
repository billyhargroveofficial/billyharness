package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gateway"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/runtimehost"
)

func runOnce(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	mock := fs.Bool("mock", false, "use mock provider")
	model := fs.String("model", "", "model override")
	profile := fs.String("profile", "", "system profile override")
	accessMode := fs.String("access-mode", "", "run access mode: build, guarded, or plan")
	gatewayURL := fs.String("gateway", "", "gateway base URL, for example http://127.0.0.1:8765")
	noReasoning := fs.Bool("hide-reasoning", true, "do not print reasoning deltas")
	if err := fs.Parse(args); err != nil {
		return err
	}
	prompt := strings.Join(fs.Args(), " ")
	if prompt == "" {
		return fmt.Errorf("prompt required")
	}
	mode, err := parseAccessModeFlag(*accessMode)
	if err != nil {
		return err
	}
	if *gatewayURL != "" {
		req := gateway.RunRequest{Prompt: prompt, Model: *model, Profile: config.NormalizeProfileName(*profile)}
		if mode != "" {
			req.AccessMode = mode
		}
		if *mock {
			req.Provider = "mock"
			req.Model = "mock"
		}
		return gatewayRun(context.Background(), *gatewayURL, "/v1/run", req, terminalEmitter(*noReasoning))
	}
	var overrides []config.ResolveOverride
	if *mock {
		overrides = append(overrides,
			config.ResolveOverride{Key: "provider", Value: "mock", Source: config.SourceCLI, SourceKey: "-mock"},
			config.ResolveOverride{Key: "model", Value: "mock", Source: config.SourceCLI, SourceKey: "-mock"},
		)
	}
	overrides = appendStringOverride(overrides, "model", *model, "-model")
	if strings.TrimSpace(*profile) != "" {
		overrides = appendStringOverride(overrides, "profile", config.NormalizeProfileName(*profile), "-profile")
	}
	if mode != "" {
		overrides = append(overrides, config.ResolveOverride{Key: "access_mode", Value: mode, Source: config.SourceCLI, SourceKey: "-access-mode"})
	}
	cfg, err := resolveRuntimeConfig(overrides...)
	if err != nil {
		return err
	}
	host, err := runtimehost.New(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer host.Close()
	a := host.Agent()
	return a.Run(context.Background(), prompt, terminalEmitter(*noReasoning))
}

func chat(args []string) error {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	mock := fs.Bool("mock", false, "use mock provider")
	model := fs.String("model", "", "model override")
	profile := fs.String("profile", "", "system profile override")
	accessMode := fs.String("access-mode", "", "run access mode: build, guarded, or plan")
	gatewayURL := fs.String("gateway", "", "gateway base URL, for example http://127.0.0.1:8765")
	noReasoning := fs.Bool("hide-reasoning", true, "do not print reasoning deltas")
	if err := fs.Parse(args); err != nil {
		return err
	}
	mode, err := parseAccessModeFlag(*accessMode)
	if err != nil {
		return err
	}
	if *gatewayURL != "" {
		return chatGateway(*gatewayURL, *noReasoning, *model, *profile, mode, *mock)
	}
	var overrides []config.ResolveOverride
	if *mock {
		overrides = append(overrides,
			config.ResolveOverride{Key: "provider", Value: "mock", Source: config.SourceCLI, SourceKey: "-mock"},
			config.ResolveOverride{Key: "model", Value: "mock", Source: config.SourceCLI, SourceKey: "-mock"},
		)
	}
	overrides = appendStringOverride(overrides, "model", *model, "-model")
	if strings.TrimSpace(*profile) != "" {
		overrides = appendStringOverride(overrides, "profile", config.NormalizeProfileName(*profile), "-profile")
	}
	if mode != "" {
		overrides = append(overrides, config.ResolveOverride{Key: "access_mode", Value: mode, Source: config.SourceCLI, SourceKey: "-access-mode"})
	}
	cfg, err := resolveRuntimeConfig(overrides...)
	if err != nil {
		return err
	}
	host, err := runtimehost.New(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer host.Close()
	a := host.Agent()
	messages := runtimehost.InitialMessages(host.Settings.Instructions)
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

func parseAccessModeFlag(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	mode, ok := config.ParseAccessMode(value)
	if !ok {
		return "", fmt.Errorf("unsupported access mode %q; use build, guarded, or plan", value)
	}
	return mode, nil
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

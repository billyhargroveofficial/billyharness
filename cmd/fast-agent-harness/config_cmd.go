package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/config"
)

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
			Config      map[string]any            `json:"config"`
			Values      []config.ResolvedValue    `json:"values"`
			Diagnostics config.DiagnosticSnapshot `json:"diagnostics"`
			Warnings    []string                  `json:"warnings,omitempty"`
		}{
			Config:      resolved.SanitizedConfig(),
			Values:      resolved.SanitizedValues(),
			Diagnostics: resolved.Config.DiagnosticSnapshot(),
			Warnings:    resolved.Warnings,
		})
	}
	providerAuth := resolved.Config.ProviderAuthSnapshot()
	fmt.Fprintf(stdout, "billyharness config inspect\n")
	fmt.Fprintf(stdout, "provider=%s model=%s profile=%s reasoning=%s/%s gateway=%s\n",
		providerAuth.Provider,
		providerAuth.Model,
		providerAuth.Profile,
		providerAuth.Thinking,
		providerAuth.ReasoningEffort,
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

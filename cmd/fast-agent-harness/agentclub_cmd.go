package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	"github.com/billyhargroveofficial/billyharness/internal/secrets"
)

func agentclubCmd(args []string) error {
	return agentclubCommand(args, os.Stdout)
}

func agentclubCommand(args []string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	if len(args) == 0 {
		agentclubUsage(out)
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "capabilities", "capability", "caps":
		return agentclubCapabilitiesCommand(args[1:], out)
	case "config":
		return agentclubConfigCommand(args[1:], out)
	case "bindings", "binding":
		return agentclubLocalBindingsCommand(args[1:], out)
	case "triggers", "trigger":
		return agentclubLocalTriggersCommand(args[1:], out)
	case "enable":
		return agentclubSetLocalItemCommand(args[1:], out, true)
	case "disable":
		return agentclubSetLocalItemCommand(args[1:], out, false)
	case "proposals", "proposal", "list":
		return agentclubProposalsCommand(args[1:], out)
	case "approve":
		return agentclubDecisionCommand(args[1:], out, agentclub.ProposalDecisionApprove)
	case "reject":
		return agentclubDecisionCommand(args[1:], out, agentclub.ProposalDecisionReject)
	case "help", "-h", "--help":
		agentclubUsage(out)
		return nil
	default:
		agentclubUsage(out)
		return fmt.Errorf("unknown agentclub command %q", args[0])
	}
}

func agentclubConfigCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return agentclubConfigStatusCommand(nil, out)
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "init":
		return agentclubConfigInitCommand(args[1:], out)
	case "validate":
		return agentclubConfigValidateCommand(args[1:], out)
	case "status":
		return agentclubConfigStatusCommand(args[1:], out)
	case "path", "paths":
		return agentclubConfigPathCommand(args[1:], out)
	case "help", "-h", "--help":
		agentclubConfigUsage(out)
		return nil
	default:
		agentclubConfigUsage(out)
		return fmt.Errorf("unknown agentclub config command %q", args[0])
	}
}

func agentclubConfigInitCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agentclub config init", flag.ExitOnError)
	path := fs.String("path", "", "config file path; defaults to $BILLYHARNESS_HOME/agentclub.config.json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: agentclub config init [-path PATH]")
	}
	target := strings.TrimSpace(*path)
	if target == "" {
		target = config.DefaultAgentClubConfigFile()
	}
	written, err := agentclub.EnsureConfigFile(target)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "agent-club config: %s\n", written)
	return nil
}

func agentclubConfigValidateCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agentclub config validate", flag.ExitOnError)
	path := fs.String("path", "", "config file path override")
	jsonOut := fs.Bool("json", false, "print redacted JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: agentclub config validate [-path PATH] [-json]")
	}
	loaded, files, err := loadAgentClubLocalConfig(*path)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeRedactedJSON(out, map[string]any{"files": files, "status": loaded.Status})
	}
	if len(files) == 0 {
		fmt.Fprintln(out, "agent-club config: no files")
		return nil
	}
	fmt.Fprint(out, formatAgentClubConfigStatus(files, loaded.Status))
	return nil
}

func agentclubConfigStatusCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agentclub config status", flag.ExitOnError)
	path := fs.String("path", "", "config file path override")
	jsonOut := fs.Bool("json", false, "print redacted JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: agentclub config status [-path PATH] [-json]")
	}
	loaded, files, err := loadAgentClubLocalConfig(*path)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeRedactedJSON(out, map[string]any{"files": files, "status": loaded.Status})
	}
	fmt.Fprint(out, formatAgentClubConfigStatus(files, loaded.Status))
	return nil
}

func agentclubConfigPathCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agentclub config path", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "print redacted JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: agentclub config path [-json]")
	}
	cfg, err := resolveRuntimeConfig()
	if err != nil {
		return err
	}
	files := agentClubConfigFilesFor(cfg)
	if *jsonOut {
		return writeRedactedJSON(out, map[string]any{
			"default_path": config.DefaultAgentClubConfigFile(),
			"files":        files,
		})
	}
	fmt.Fprintf(out, "default: %s\n", config.DefaultAgentClubConfigFile())
	if len(files) == 0 {
		fmt.Fprintln(out, "configured: none")
		return nil
	}
	for _, file := range files {
		fmt.Fprintf(out, "configured: %s\n", file)
	}
	return nil
}

func agentclubLocalBindingsCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agentclub bindings", flag.ExitOnError)
	path := fs.String("path", "", "config file path override")
	jsonOut := fs.Bool("json", false, "print redacted JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: agentclub bindings [-path PATH] [-json]")
	}
	loaded, _, err := loadAgentClubLocalConfig(*path)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeRedactedJSON(out, loaded.Config.TrustedBindings)
	}
	fmt.Fprint(out, formatAgentClubBindings(loaded.Config.TrustedBindings))
	return nil
}

func agentclubLocalTriggersCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agentclub triggers", flag.ExitOnError)
	path := fs.String("path", "", "config file path override")
	jsonOut := fs.Bool("json", false, "print redacted JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: agentclub triggers [-path PATH] [-json]")
	}
	loaded, _, err := loadAgentClubLocalConfig(*path)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeRedactedJSON(out, loaded.Config.Triggers)
	}
	fmt.Fprint(out, formatAgentClubTriggers(loaded.Config.Triggers))
	return nil
}

func agentclubSetLocalItemCommand(args []string, out io.Writer, enabled bool) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agentclub %s <binding|trigger> ID [-path PATH]", enableVerb(enabled))
	}
	kind := strings.ToLower(strings.TrimSpace(args[0]))
	path, positional, err := parseAgentClubSetArgs(args[1:])
	if err != nil {
		return err
	}
	if len(positional) != 1 || (kind != "binding" && kind != "trigger") {
		return fmt.Errorf("usage: agentclub %s <binding|trigger> ID [-path PATH]", enableVerb(enabled))
	}
	targetPath, err := agentClubWritableConfigPath(path)
	if err != nil {
		return err
	}
	cfg, err := agentclub.ReadConfigFile(targetPath)
	if err != nil {
		return err
	}
	id := positional[0]
	switch kind {
	case "binding":
		err = agentclub.SetTrustedBindingEnabled(&cfg, id, enabled)
	case "trigger":
		err = agentclub.SetTriggerEnabled(&cfg, id, enabled)
	}
	if err != nil {
		return err
	}
	if _, _, err := agentclub.BuildRegistryFromConfig(cfg, agentClubSecretLookup); err != nil {
		return err
	}
	if err := agentclub.WriteConfigFile(targetPath, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s %s %s in %s\n", enableVerb(enabled), kind, secrets.Redact(id), targetPath)
	return nil
}

func parseAgentClubSetArgs(args []string) (string, []string, error) {
	var path string
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "-path" || arg == "--path":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				return "", nil, fmt.Errorf("-path requires a value")
			}
			path = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-path="):
			path = strings.TrimSpace(strings.TrimPrefix(arg, "-path="))
		case strings.HasPrefix(arg, "--path="):
			path = strings.TrimSpace(strings.TrimPrefix(arg, "--path="))
		case strings.HasPrefix(arg, "-"):
			return "", nil, fmt.Errorf("unknown flag %s", arg)
		case arg != "":
			positional = append(positional, arg)
		}
	}
	return path, positional, nil
}

func agentclubCapabilitiesCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agentclub capabilities", flag.ExitOnError)
	gatewayURL := fs.String("gateway", "", "gateway base URL")
	clientType := fs.String("client-type", "", "optional owner client type for scoped discovery")
	clientID := fs.String("client-id", "", "optional owner client id for scoped discovery")
	jsonOut := fs.Bool("json", false, "print redacted JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: agentclub capabilities [-gateway URL] [-client-type TYPE -client-id ID] [-json]")
	}
	ctx, err := agentclubOwnerContext(context.Background(), *clientType, *clientID)
	if err != nil {
		return err
	}
	client, err := agentclubGatewayClient(*gatewayURL)
	if err != nil {
		return err
	}
	resp, err := client.AgentClubCapabilities(ctx)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeRedactedJSON(out, resp)
	}
	fmt.Fprint(out, gatewayclient.FormatAgentClubCapabilities(resp))
	return nil
}

func agentclubProposalsCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agentclub proposals", flag.ExitOnError)
	gatewayURL := fs.String("gateway", "", "gateway base URL")
	sessionID := fs.String("session", "", "explicit gateway session id")
	jsonOut := fs.Bool("json", false, "print redacted JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(*sessionID) == "" {
		return fmt.Errorf("usage: agentclub proposals -session SESSION_ID [-gateway URL] [-json]")
	}
	client, err := agentclubGatewayClient(*gatewayURL)
	if err != nil {
		return err
	}
	resp, err := client.AgentClubProposals(context.Background(), *sessionID)
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeRedactedJSON(out, resp)
	}
	fmt.Fprint(out, gatewayclient.FormatAgentClubProposals(resp))
	return nil
}

func agentclubDecisionCommand(args []string, out io.Writer, action string) error {
	fs := flag.NewFlagSet("agentclub "+action, flag.ExitOnError)
	gatewayURL := fs.String("gateway", "", "gateway base URL")
	sessionID := fs.String("session", "", "explicit gateway session id")
	proposalID := fs.String("proposal", "", "explicit proposal id")
	expectedHash := fs.String("hash", "", "expected proposal hash")
	comment := fs.String("comment", "", "optional operator comment")
	jsonOut := fs.Bool("json", false, "print redacted JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(*sessionID) == "" || strings.TrimSpace(*proposalID) == "" || strings.TrimSpace(*expectedHash) == "" {
		return fmt.Errorf("usage: agentclub %s -session SESSION_ID -proposal PROPOSAL_ID -hash EXPECTED_PROPOSAL_HASH [-comment TEXT] [-gateway URL] [-json]", action)
	}
	client, err := agentclubGatewayClient(*gatewayURL)
	if err != nil {
		return err
	}
	resp, err := client.DecideAgentClubProposal(context.Background(), *sessionID, *proposalID, agentclub.ProposalDecisionRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		Action:               action,
		ExpectedProposalHash: strings.TrimSpace(*expectedHash),
		Comment:              strings.TrimSpace(*comment),
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeRedactedJSON(out, resp)
	}
	fmt.Fprintf(out, "%s proposal %s decision=%s state=%s hash=%s\n",
		action,
		secrets.Redact(resp.Proposal.ProposalID),
		secrets.Redact(resp.DecisionID),
		resp.Proposal.State,
		gatewayclient.ShortAgentClubHash(resp.Proposal.ProposalHash),
	)
	return nil
}

func agentclubGatewayClient(gatewayURL string) (*gatewayclient.Client, error) {
	baseURL := normalizeGatewayURL(gatewayURL)
	if baseURL == "" {
		cfg, err := resolveRuntimeConfig()
		if err != nil {
			return nil, err
		}
		discovered, ok := discoverGatewayURL(context.Background(), cfg)
		if !ok {
			return nil, fmt.Errorf("gateway unavailable: %s", gatewayapi.UnavailableHint(normalizeGatewayURL(cfg.GatewayAddr)))
		}
		baseURL = discovered
	}
	return gatewayclient.New(baseURL), nil
}

func agentclubOwnerContext(ctx context.Context, clientType, clientID string) (context.Context, error) {
	clientType = strings.TrimSpace(clientType)
	clientID = strings.TrimSpace(clientID)
	if (clientType == "") != (clientID == "") {
		return ctx, fmt.Errorf("client-type and client-id must be provided together")
	}
	if clientType == "" {
		return ctx, nil
	}
	return gatewayclient.WithSessionOwner(ctx, gatewayapi.SessionOwner{
		ClientType: clientType,
		ClientID:   clientID,
	}), nil
}

func writeRedactedJSON(out io.Writer, value any) error {
	body, err := secrets.RedactJSONIndent(value, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	_, err = out.Write(body)
	return err
}

func agentclubUsage(out io.Writer) {
	fmt.Fprintln(out, "Usage: fast-agent-harness agentclub <command> [args]")
	fmt.Fprintln(out, "  agentclub capabilities [-gateway URL] [-client-type TYPE -client-id ID] [-json]")
	fmt.Fprintln(out, "  agentclub config <init|validate|status|path> [args]")
	fmt.Fprintln(out, "  agentclub bindings [-path PATH] [-json]")
	fmt.Fprintln(out, "  agentclub triggers [-path PATH] [-json]")
	fmt.Fprintln(out, "  agentclub enable <binding|trigger> ID [-path PATH]")
	fmt.Fprintln(out, "  agentclub disable <binding|trigger> ID [-path PATH]")
	fmt.Fprintln(out, "  agentclub proposals -session SESSION_ID [-gateway URL] [-json]")
	fmt.Fprintln(out, "  agentclub approve -session SESSION_ID -proposal PROPOSAL_ID -hash EXPECTED_PROPOSAL_HASH [-comment TEXT] [-gateway URL] [-json]")
	fmt.Fprintln(out, "  agentclub reject -session SESSION_ID -proposal PROPOSAL_ID -hash EXPECTED_PROPOSAL_HASH [-comment TEXT] [-gateway URL] [-json]")
}

func agentclubConfigUsage(out io.Writer) {
	fmt.Fprintln(out, "Usage: fast-agent-harness agentclub config <command> [args]")
	fmt.Fprintln(out, "  agentclub config init [-path PATH]")
	fmt.Fprintln(out, "  agentclub config validate [-path PATH] [-json]")
	fmt.Fprintln(out, "  agentclub config status [-path PATH] [-json]")
	fmt.Fprintln(out, "  agentclub config path [-json]")
}

func loadAgentClubLocalConfig(path string) (agentclub.LoadedConfig, []string, error) {
	var files []string
	path = strings.TrimSpace(path)
	if path != "" {
		files = []string{path}
	} else {
		cfg, err := resolveRuntimeConfig()
		if err != nil {
			return agentclub.LoadedConfig{}, nil, err
		}
		files = agentClubConfigFilesFor(cfg)
	}
	loaded, err := agentclub.LoadConfigFiles(agentclub.LoadConfigOptions{
		Files:        files,
		SecretLookup: agentClubSecretLookup,
	})
	return loaded, files, err
}

func agentClubWritableConfigPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path != "" {
		return path, nil
	}
	cfg, err := resolveRuntimeConfig()
	if err != nil {
		return "", err
	}
	files := agentClubConfigFilesFor(cfg)
	if len(files) == 0 {
		return "", fmt.Errorf("agentclub config file not found; run agentclub config init")
	}
	return files[0], nil
}

func formatAgentClubConfigStatus(files []string, status agentclub.ConfigStatus) string {
	var b strings.Builder
	if len(files) == 0 {
		b.WriteString("agent-club config: no files\n")
		return b.String()
	}
	fmt.Fprintf(&b, "agent-club config: files=%d capabilities=%d bindings=%d enabled_bindings=%d triggers=%d enabled_triggers=%d missing_secret_envs=%d\n",
		len(files),
		status.CapabilityCount,
		status.BindingCount,
		status.EnabledBindingCount,
		status.TriggerCount,
		status.EnabledTriggerCount,
		status.MissingSecretEnvCount,
	)
	for _, file := range files {
		fmt.Fprintf(&b, "- %s\n", file)
	}
	return b.String()
}

func formatAgentClubBindings(bindings []agentclub.TrustedBindingConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "agent-club bindings: %d\n", len(bindings))
	if len(bindings) == 0 {
		b.WriteString("bindings: none\n")
		return b.String()
	}
	for _, binding := range bindings {
		fmt.Fprintf(&b, "- %s capability=%s owner=%s/%s enabled=%t",
			secrets.Redact(binding.ID),
			binding.Capability,
			secrets.Redact(binding.ClientType),
			secrets.Redact(binding.ClientID),
			binding.Enabled,
		)
		if len(binding.Sources) > 0 {
			fmt.Fprintf(&b, " sources=%s", secrets.Redact(strings.Join(binding.Sources, ",")))
		}
		if len(binding.EventTypes) > 0 {
			fmt.Fprintf(&b, " events=%s", secrets.Redact(strings.Join(binding.EventTypes, ",")))
		}
		if len(binding.MetadataKeys) > 0 {
			fmt.Fprintf(&b, " metadata=%s", secrets.Redact(strings.Join(binding.MetadataKeys, ",")))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func formatAgentClubTriggers(triggers []agentclub.TriggerBindingConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "agent-club triggers: %d\n", len(triggers))
	if len(triggers) == 0 {
		b.WriteString("triggers: none\n")
		return b.String()
	}
	for _, trigger := range triggers {
		fmt.Fprintf(&b, "- %s kind=%s capability=%s event=%s enabled=%t auth=%s",
			secrets.Redact(trigger.ID),
			trigger.Kind,
			trigger.Capability,
			trigger.EventType,
			trigger.Enabled,
			trigger.AuthMethod,
		)
		if trigger.TargetSessionID != "" {
			fmt.Fprintf(&b, " session=%s", secrets.Redact(trigger.TargetSessionID))
		}
		if trigger.HMACSecretEnv != "" {
			fmt.Fprintf(&b, " hmac_secret_env=%s", secrets.Redact(trigger.HMACSecretEnv))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func enableVerb(enabled bool) string {
	if enabled {
		return "enable"
	}
	return "disable"
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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
	case "scheduler":
		return agentclubSchedulerCommand(args[1:], out)
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
	case "apply":
		return agentclubApplyCommand(args[1:], out)
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
	if len(args) > 0 && strings.EqualFold(strings.TrimSpace(args[0]), "deliver") {
		return agentclubTriggerDeliverCommand(args[1:], out)
	}
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

func agentclubTriggerDeliverCommand(args []string, out io.Writer) error {
	opts, err := parseAgentClubTriggerDeliverArgs(args)
	if err != nil {
		return err
	}
	loaded, files, err := loadAgentClubLocalConfig(opts.path)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("agentclub config file not found; run agentclub config init")
	}
	binding, err := loaded.Registry.TriggerBinding(opts.triggerID)
	if err != nil {
		return err
	}
	payload, err := readAgentClubDeliveryPayload(opts.payload, binding.MaxBodyBytes)
	if err != nil {
		return err
	}
	body, headers, err := buildAgentClubDeliveryBody(binding, payload, opts.deliveryID, opts.scheduledAt, opts.dryRunRegistration)
	if err != nil {
		return err
	}
	client, err := agentclubGatewayClient(opts.gatewayURL)
	if err != nil {
		return err
	}
	resp, err := client.DeliverAgentClubTrigger(context.Background(), gatewayclient.AgentClubTriggerDelivery{
		BindingID: binding.ID,
		Body:      body,
		Headers:   headers,
	})
	if err != nil {
		return safeAgentClubTriggerDeliveryError(err)
	}
	if opts.jsonOut {
		return writeRedactedJSON(out, resp)
	}
	fmt.Fprint(out, formatAgentClubTriggerDelivery(files, loaded.Status, resp))
	return nil
}

type agentClubTriggerDeliverOptions struct {
	triggerID          string
	gatewayURL         string
	path               string
	payload            string
	deliveryID         string
	scheduledAt        string
	dryRunRegistration bool
	jsonOut            bool
}

func parseAgentClubTriggerDeliverArgs(args []string) (agentClubTriggerDeliverOptions, error) {
	opts := agentClubTriggerDeliverOptions{scheduledAt: "now"}
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "":
			continue
		case arg == "-gateway" || arg == "--gateway":
			value, next, err := agentclubNextFlagValue(args, i, arg)
			if err != nil {
				return opts, err
			}
			opts.gatewayURL = value
			i = next
		case strings.HasPrefix(arg, "-gateway="):
			opts.gatewayURL = strings.TrimSpace(strings.TrimPrefix(arg, "-gateway="))
		case strings.HasPrefix(arg, "--gateway="):
			opts.gatewayURL = strings.TrimSpace(strings.TrimPrefix(arg, "--gateway="))
		case arg == "-path" || arg == "--path":
			value, next, err := agentclubNextFlagValue(args, i, arg)
			if err != nil {
				return opts, err
			}
			opts.path = value
			i = next
		case strings.HasPrefix(arg, "-path="):
			opts.path = strings.TrimSpace(strings.TrimPrefix(arg, "-path="))
		case strings.HasPrefix(arg, "--path="):
			opts.path = strings.TrimSpace(strings.TrimPrefix(arg, "--path="))
		case arg == "-payload" || arg == "--payload":
			value, next, err := agentclubNextFlagValue(args, i, arg)
			if err != nil {
				return opts, err
			}
			opts.payload = value
			i = next
		case strings.HasPrefix(arg, "-payload="):
			opts.payload = strings.TrimSpace(strings.TrimPrefix(arg, "-payload="))
		case strings.HasPrefix(arg, "--payload="):
			opts.payload = strings.TrimSpace(strings.TrimPrefix(arg, "--payload="))
		case arg == "-delivery-id" || arg == "--delivery-id":
			value, next, err := agentclubNextFlagValue(args, i, arg)
			if err != nil {
				return opts, err
			}
			opts.deliveryID = value
			i = next
		case strings.HasPrefix(arg, "-delivery-id="):
			opts.deliveryID = strings.TrimSpace(strings.TrimPrefix(arg, "-delivery-id="))
		case strings.HasPrefix(arg, "--delivery-id="):
			opts.deliveryID = strings.TrimSpace(strings.TrimPrefix(arg, "--delivery-id="))
		case arg == "-scheduled-at" || arg == "--scheduled-at":
			value, next, err := agentclubNextFlagValue(args, i, arg)
			if err != nil {
				return opts, err
			}
			opts.scheduledAt = value
			i = next
		case strings.HasPrefix(arg, "-scheduled-at="):
			opts.scheduledAt = strings.TrimSpace(strings.TrimPrefix(arg, "-scheduled-at="))
		case strings.HasPrefix(arg, "--scheduled-at="):
			opts.scheduledAt = strings.TrimSpace(strings.TrimPrefix(arg, "--scheduled-at="))
		case arg == "-dry-run-registration" || arg == "--dry-run-registration":
			opts.dryRunRegistration = true
		case arg == "-json" || arg == "--json":
			opts.jsonOut = true
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown flag %s", arg)
		default:
			if opts.triggerID != "" {
				return opts, fmt.Errorf("usage: agentclub trigger deliver TRIGGER_ID [-gateway URL] [-path CONFIG] [-payload JSON_OR_PATH_OR_-] [-delivery-id ID] [-scheduled-at RFC3339|now] [-dry-run-registration] [-json]")
			}
			opts.triggerID = arg
		}
	}
	if strings.TrimSpace(opts.triggerID) == "" {
		return opts, fmt.Errorf("usage: agentclub trigger deliver TRIGGER_ID [-gateway URL] [-path CONFIG] [-payload JSON_OR_PATH_OR_-] [-delivery-id ID] [-scheduled-at RFC3339|now] [-dry-run-registration] [-json]")
	}
	return opts, nil
}

func agentclubNextFlagValue(args []string, index int, flagName string) (string, int, error) {
	next := index + 1
	if next >= len(args) || strings.TrimSpace(args[next]) == "" {
		return "", index, fmt.Errorf("%s requires a value", flagName)
	}
	return strings.TrimSpace(args[next]), next, nil
}

func readAgentClubDeliveryPayload(spec string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = agentclub.DefaultTriggerMaxBodyBytes
	}
	var body []byte
	var err error
	spec = strings.TrimSpace(spec)
	switch {
	case spec == "":
		body = []byte(`{}`)
	case spec == "-":
		body, err = readAgentClubBounded(os.Stdin, maxBytes)
	case strings.HasPrefix(spec, "@"):
		path := strings.TrimSpace(strings.TrimPrefix(spec, "@"))
		if path == "" {
			return nil, fmt.Errorf("payload file path required after @")
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return nil, openErr
		}
		defer file.Close()
		body, err = readAgentClubBounded(file, maxBytes)
	default:
		body = []byte(spec)
		if int64(len(body)) > maxBytes {
			return nil, fmt.Errorf("payload exceeds trigger max body cap")
		}
	}
	if err != nil {
		return nil, err
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		body = []byte(`{}`)
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("payload must be valid JSON")
	}
	return body, nil
}

func readAgentClubBounded(r io.Reader, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("payload exceeds trigger max body cap")
	}
	return body, nil
}

func buildAgentClubDeliveryBody(binding agentclub.TriggerBinding, payload []byte, deliveryID, scheduledAt string, dryRunRegistration bool) ([]byte, map[string]string, error) {
	switch binding.Kind {
	case agentclub.TriggerKindWebhook:
		if dryRunRegistration {
			return nil, nil, fmt.Errorf("-dry-run-registration is only valid for manual and schedule triggers")
		}
		deliveryID = strings.TrimSpace(deliveryID)
		if deliveryID == "" {
			return nil, nil, fmt.Errorf("-delivery-id is required for webhook triggers")
		}
		headers := map[string]string{binding.DeliveryIDHeader: deliveryID}
		if binding.AuthMethod == agentclub.TriggerAuthHMACSHA256 {
			if len(binding.HMACSecret) == 0 {
				return nil, nil, fmt.Errorf("hmac secret is not available for trigger %s", secrets.Redact(binding.ID))
			}
			signatureHeader, signature, timestampHeader, timestamp, err := agentclub.SignTriggerWebhookHMAC(binding, payload, time.Now().UTC())
			if err != nil {
				return nil, nil, err
			}
			if timestampHeader != "" {
				headers[timestampHeader] = timestamp
			}
			headers[signatureHeader] = signature
		}
		return payload, headers, nil
	case agentclub.TriggerKindManual, agentclub.TriggerKindSchedule:
		if strings.TrimSpace(deliveryID) != "" {
			return nil, nil, fmt.Errorf("-delivery-id is only valid for webhook triggers")
		}
		scheduled, err := parseAgentClubDeliveryScheduledAt(scheduledAt)
		if err != nil {
			return nil, nil, err
		}
		body, err := json.Marshal(agentclub.TriggerDeliveryRequest{
			SchemaVersion:      agentclub.SchemaVersion,
			ScheduledAtUTC:     scheduled,
			Payload:            append(json.RawMessage(nil), payload...),
			DryRunRegistration: dryRunRegistration,
		})
		if err != nil {
			return nil, nil, err
		}
		return body, nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported trigger kind %q", binding.Kind)
	}
}

func parseAgentClubDeliveryScheduledAt(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "now") {
		return time.Now().UTC().Format(time.RFC3339Nano), nil
	}
	ts, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", fmt.Errorf("-scheduled-at must be RFC3339 or now")
	}
	return ts.UTC().Format(time.RFC3339Nano), nil
}

func safeAgentClubTriggerDeliveryError(err error) error {
	var status *gatewayclient.StatusError
	if errors.As(err, &status) {
		return fmt.Errorf("agentclub trigger delivery failed: gateway HTTP %d", status.StatusCode)
	}
	return fmt.Errorf("agentclub trigger delivery failed: %s", secrets.Redact(err.Error()))
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

func agentclubApplyCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agentclub apply", flag.ExitOnError)
	gatewayURL := fs.String("gateway", "", "gateway base URL")
	sessionID := fs.String("session", "", "explicit gateway session id")
	proposalID := fs.String("proposal", "", "explicit proposal id")
	expectedHash := fs.String("hash", "", "expected proposal hash")
	idempotencyKey := fs.String("idempotency-key", "", "optional idempotency key; defaults to a deterministic CLI key")
	dryRun := fs.Bool("dry-run", false, "validate without writing an apply record")
	jsonOut := fs.Bool("json", false, "print redacted JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(*sessionID) == "" || strings.TrimSpace(*proposalID) == "" || strings.TrimSpace(*expectedHash) == "" {
		return fmt.Errorf("usage: agentclub apply -session SESSION_ID -proposal PROPOSAL_ID -hash EXPECTED_PROPOSAL_HASH [-idempotency-key KEY] [-dry-run] [-gateway URL] [-json]")
	}
	key := strings.TrimSpace(*idempotencyKey)
	if key == "" {
		key = derivedAgentClubApplyIdempotencyKey(*proposalID, *expectedHash)
	}
	client, err := agentclubGatewayClient(*gatewayURL)
	if err != nil {
		return err
	}
	resp, err := client.ApplyAgentClubProposal(context.Background(), *sessionID, *proposalID, agentclub.ProposalApplyRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		ExpectedProposalHash: strings.TrimSpace(*expectedHash),
		IdempotencyKey:       key,
		DryRun:               *dryRun,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeRedactedJSON(out, resp)
	}
	fmt.Fprint(out, gatewayclient.FormatAgentClubProposalApply(resp))
	return nil
}

func derivedAgentClubApplyIdempotencyKey(proposalID, proposalHash string) string {
	return "cli:" + strings.TrimSpace(proposalID) + ":" + gatewayclient.ShortAgentClubHash(proposalHash)
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
	fmt.Fprintln(out, "  agentclub trigger deliver TRIGGER_ID [-gateway URL] [-path CONFIG] [-payload JSON_OR_PATH_OR_-] [-delivery-id ID] [-scheduled-at RFC3339|now] [-dry-run-registration] [-json]")
	fmt.Fprintln(out, "  agentclub scheduler <run|status> [args]")
	fmt.Fprintln(out, "  agentclub enable <binding|trigger> ID [-path PATH]")
	fmt.Fprintln(out, "  agentclub disable <binding|trigger> ID [-path PATH]")
	fmt.Fprintln(out, "  agentclub proposals -session SESSION_ID [-gateway URL] [-json]")
	fmt.Fprintln(out, "  agentclub approve -session SESSION_ID -proposal PROPOSAL_ID -hash EXPECTED_PROPOSAL_HASH [-comment TEXT] [-gateway URL] [-json]")
	fmt.Fprintln(out, "  agentclub reject -session SESSION_ID -proposal PROPOSAL_ID -hash EXPECTED_PROPOSAL_HASH [-comment TEXT] [-gateway URL] [-json]")
	fmt.Fprintln(out, "  agentclub apply -session SESSION_ID -proposal PROPOSAL_ID -hash EXPECTED_PROPOSAL_HASH [-idempotency-key KEY] [-dry-run] [-gateway URL] [-json]")
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
	fmt.Fprintf(&b, "agent-club config: files=%d capabilities=%d bindings=%d enabled_bindings=%d triggers=%d enabled_triggers=%d auto_run=%d missing_secret_envs=%d\n",
		len(files),
		status.CapabilityCount,
		status.BindingCount,
		status.EnabledBindingCount,
		status.TriggerCount,
		status.EnabledTriggerCount,
		status.EnabledAutoRunCount,
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
		if trigger.RunPolicy != nil && trigger.RunPolicy.Enabled {
			fmt.Fprintf(&b, " run_policy=%s", formatAgentClubRunPolicySummary(trigger.RunPolicy))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func formatAgentClubRunPolicySummary(policy *agentclub.RunPolicyConfig) string {
	if policy == nil || !policy.Enabled {
		return "disabled"
	}
	parts := []string{"enabled"}
	if strings.TrimSpace(policy.Mode) != "" {
		parts = append(parts, "mode="+policy.Mode)
	}
	if policy.MaxRunsPerHour > 0 {
		parts = append(parts, fmt.Sprintf("max_runs_per_hour=%d", policy.MaxRunsPerHour))
	}
	if strings.TrimSpace(policy.Cooldown) != "" {
		parts = append(parts, "cooldown="+policy.Cooldown)
	}
	if policy.MaxToolRounds > 0 {
		parts = append(parts, fmt.Sprintf("max_tool_rounds=%d", policy.MaxToolRounds))
	}
	if strings.TrimSpace(policy.AccessMode) != "" {
		parts = append(parts, "access_mode="+policy.AccessMode)
	}
	return strings.Join(parts, ",")
}

func formatAgentClubTriggerDelivery(files []string, status agentclub.ConfigStatus, resp agentclub.TriggerDeliveryResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "agent-club trigger delivery: admitted=%t state=%s duplicate=%t run_dispatched=%t\n",
		resp.Admitted,
		secrets.Redact(resp.State),
		resp.Duplicate,
		resp.RunDispatched,
	)
	fmt.Fprintf(&b, "binding=%s kind=%s source=%s capability=%s event=%s\n",
		secrets.Redact(resp.BindingID),
		resp.TriggerKind,
		resp.Source,
		resp.Capability,
		resp.EventType,
	)
	if resp.TargetSessionID != "" || resp.InputID != "" {
		fmt.Fprintf(&b, "target_session=%s input=%s\n", secrets.Redact(resp.TargetSessionID), secrets.Redact(resp.InputID))
	}
	if resp.PayloadSHA256 != "" || resp.ExternalEventIDHash != "" {
		fmt.Fprintf(&b, "payload_sha256=%s external_event_hash=%s\n",
			gatewayclient.ShortAgentClubHash(resp.PayloadSHA256),
			gatewayclient.ShortAgentClubHash(resp.ExternalEventIDHash),
		)
	}
	fmt.Fprintf(&b, "config: files=%d capabilities=%d bindings=%d enabled_bindings=%d triggers=%d enabled_triggers=%d\n",
		len(files),
		status.CapabilityCount,
		status.BindingCount,
		status.EnabledBindingCount,
		status.TriggerCount,
		status.EnabledTriggerCount,
	)
	if resp.Admitted && resp.InputID != "" && !resp.RunDispatched {
		fmt.Fprintf(&b, "next: queued input %s; start a run through the existing session run route for session %s\n",
			secrets.Redact(resp.InputID),
			secrets.Redact(resp.TargetSessionID),
		)
	}
	return b.String()
}

func enableVerb(enabled bool) string {
	if enabled {
		return "enable"
	}
	return "disable"
}

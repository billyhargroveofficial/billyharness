package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
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
	case "capabilities", "capability", "caps", "bindings":
		return agentclubCapabilitiesCommand(args[1:], out)
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
	fmt.Fprintln(out, "  agentclub proposals -session SESSION_ID [-gateway URL] [-json]")
	fmt.Fprintln(out, "  agentclub approve -session SESSION_ID -proposal PROPOSAL_ID -hash EXPECTED_PROPOSAL_HASH [-comment TEXT] [-gateway URL] [-json]")
	fmt.Fprintln(out, "  agentclub reject -session SESSION_ID -proposal PROPOSAL_ID -hash EXPECTED_PROPOSAL_HASH [-comment TEXT] [-gateway URL] [-json]")
}

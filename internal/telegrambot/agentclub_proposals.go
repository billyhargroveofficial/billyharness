package telegrambot

import (
	"context"
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
)

const agentClubCallbackPrefix = "acp:"

type agentClubOperator interface {
	AgentClubCapabilities(context.Context) (agentclub.CapabilityListResponse, error)
	AgentClubProposals(context.Context, string) (agentclub.ProposalListResponse, error)
	DecideAgentClubProposal(context.Context, string, string, agentclub.ProposalDecisionRequest) (agentclub.ProposalDecisionResponse, error)
}

type agentClubCallback struct {
	Action             string
	ProposalID         string
	ExpectedHashPrefix string
}

func (b *Bot) handleAgentClubCommand(ctx context.Context, msg Message, scope ChatScope, arg string) {
	if strings.TrimSpace(arg) != "" {
		_ = b.sendPlain(ctx, msg, "Usage: /agentclub")
		return
	}
	operator, ok := b.harness.(agentClubOperator)
	if !ok {
		_ = b.sendPlain(ctx, msg, "Agent-club proposal APIs are not available in this harness.")
		return
	}
	state := b.chatStateWithLegacy(scope.Key(), scope.LegacyKey())
	if strings.TrimSpace(state.SessionID) == "" {
		_ = b.sendPlain(ctx, msg, "No active session. Send a message first or use /new.")
		return
	}
	capabilities, err := operator.AgentClubCapabilities(ctx)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Agent-club capabilities failed: "+err.Error())
		return
	}
	proposals, err := operator.AgentClubProposals(b.gatewayScopedContext(ctx, msg, state), state.SessionID)
	if err != nil {
		_ = b.sendPlain(ctx, msg, "Agent-club proposals failed: "+err.Error())
		return
	}
	body := formatAgentClubHTML(capabilities, proposals)
	keyboard := agentClubProposalKeyboard(proposals.Proposals)
	if len(keyboard.InlineKeyboard) > 0 {
		if err := b.sendHTMLWithReplyMarkup(ctx, msg, body, keyboard); err == nil {
			return
		}
	}
	_ = b.sendHTML(ctx, msg, body)
}

func (b *Bot) handleCallbackQuery(ctx context.Context, update Update) {
	defer b.ackOffset(update.UpdateID)
	query := update.CallbackQuery
	if query == nil {
		return
	}
	callback, err := parseAgentClubCallback(query.Data)
	if err != nil {
		b.answerCallback(ctx, query.ID, "Unknown action")
		return
	}
	if query.Message == nil {
		b.answerCallback(ctx, query.ID, "Missing message")
		return
	}
	msg := *query.Message
	msg.From = &query.From
	if !b.allowed(msg) {
		b.answerCallback(ctx, query.ID, "Not allowed")
		return
	}
	if err := b.authorizeOperatorCommand(msg); err != nil {
		b.answerCallback(ctx, query.ID, err.Error())
		return
	}
	operator, ok := b.harness.(agentClubOperator)
	if !ok {
		b.answerCallback(ctx, query.ID, "Proposal APIs unavailable")
		return
	}
	scope := messageChatScope(msg)
	state := b.chatStateWithLegacy(scope.Key(), scope.LegacyKey())
	if strings.TrimSpace(state.SessionID) == "" {
		b.answerCallback(ctx, query.ID, "No active session")
		return
	}
	scopedCtx := b.gatewayScopedContext(ctx, msg, state)
	proposals, err := operator.AgentClubProposals(scopedCtx, state.SessionID)
	if err != nil {
		b.answerCallback(ctx, query.ID, "Proposal refresh failed")
		_ = b.sendPlain(ctx, msg, "Agent-club proposal refresh failed: "+err.Error())
		return
	}
	proposal, ok := findAgentClubProposal(proposals.Proposals, callback.ProposalID)
	if !ok {
		b.answerCallback(ctx, query.ID, "Proposal not found")
		return
	}
	if proposal.State != agentclub.ProposalStatePending {
		b.answerCallback(ctx, query.ID, "Proposal is "+proposal.State)
		return
	}
	if !strings.HasPrefix(strings.ToLower(proposal.ProposalHash), strings.ToLower(callback.ExpectedHashPrefix)) {
		b.answerCallback(ctx, query.ID, "Stale proposal hash")
		return
	}
	decision, err := operator.DecideAgentClubProposal(scopedCtx, state.SessionID, proposal.ProposalID, agentclub.ProposalDecisionRequest{
		SchemaVersion:        agentclub.SchemaVersion,
		Action:               callback.Action,
		ExpectedProposalHash: proposal.ProposalHash,
	})
	if err != nil {
		b.answerCallback(ctx, query.ID, "Decision failed")
		_ = b.sendPlain(ctx, msg, "Agent-club decision failed: "+err.Error())
		return
	}
	b.answerCallback(ctx, query.ID, agentClubDecisionPastTense(callback.Action))
	_ = b.sendHTML(ctx, msg, formatAgentClubDecisionHTML(decision))
}

func formatAgentClubHTML(capabilities agentclub.CapabilityListResponse, proposals agentclub.ProposalListResponse) string {
	body := strings.TrimSpace(gatewayclient.FormatAgentClubCapabilities(capabilities)) +
		"\n\n" +
		strings.TrimSpace(gatewayclient.FormatAgentClubProposals(proposals))
	return trimTelegram("<b>Agent-club</b>\n<pre>" + esc(body) + "</pre>")
}

func formatAgentClubDecisionHTML(resp agentclub.ProposalDecisionResponse) string {
	body := fmt.Sprintf("%s proposal %s\nstate: %s\nhash: %s",
		agentClubDecisionPastTense(resp.Action),
		resp.Proposal.ProposalID,
		resp.Proposal.State,
		gatewayclient.ShortAgentClubHash(resp.Proposal.ProposalHash),
	)
	if resp.DecisionID != "" {
		body += "\ndecision: " + resp.DecisionID
	}
	return trimTelegram("<b>Agent-club decision</b>\n<pre>" + esc(body) + "</pre>")
}

func agentClubProposalKeyboard(proposals []agentclub.Proposal) InlineKeyboardMarkup {
	var keyboard InlineKeyboardMarkup
	for _, proposal := range proposals {
		if proposal.State != agentclub.ProposalStatePending {
			continue
		}
		shortID := short(proposal.ProposalID)
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []InlineKeyboardButton{
			{Text: "Approve " + shortID, CallbackData: encodeAgentClubCallback(agentclub.ProposalDecisionApprove, proposal)},
			{Text: "Reject " + shortID, CallbackData: encodeAgentClubCallback(agentclub.ProposalDecisionReject, proposal)},
		})
		if len(keyboard.InlineKeyboard) >= 5 {
			break
		}
	}
	return keyboard
}

func encodeAgentClubCallback(action string, proposal agentclub.Proposal) string {
	code := "r"
	if action == agentclub.ProposalDecisionApprove {
		code = "a"
	}
	return agentClubCallbackPrefix + code + ":" + proposal.ProposalID + ":" + gatewayclient.ShortAgentClubHash(proposal.ProposalHash)
}

func parseAgentClubCallback(data string) (agentClubCallback, error) {
	data = strings.TrimSpace(data)
	if !strings.HasPrefix(data, agentClubCallbackPrefix) {
		return agentClubCallback{}, fmt.Errorf("not agent-club callback")
	}
	parts := strings.Split(data[len(agentClubCallbackPrefix):], ":")
	if len(parts) != 3 {
		return agentClubCallback{}, fmt.Errorf("invalid agent-club callback")
	}
	callback := agentClubCallback{
		ProposalID:         strings.TrimSpace(parts[1]),
		ExpectedHashPrefix: strings.ToLower(strings.TrimSpace(parts[2])),
	}
	switch parts[0] {
	case "a":
		callback.Action = agentclub.ProposalDecisionApprove
	case "r":
		callback.Action = agentclub.ProposalDecisionReject
	default:
		return agentClubCallback{}, fmt.Errorf("unsupported agent-club action")
	}
	if callback.ProposalID == "" || callback.ExpectedHashPrefix == "" {
		return agentClubCallback{}, fmt.Errorf("invalid agent-club callback")
	}
	return callback, nil
}

func findAgentClubProposal(proposals []agentclub.Proposal, proposalID string) (agentclub.Proposal, bool) {
	proposalID = strings.TrimSpace(proposalID)
	for _, proposal := range proposals {
		if proposal.ProposalID == proposalID {
			return proposal, true
		}
	}
	return agentclub.Proposal{}, false
}

func agentClubDecisionPastTense(action string) string {
	switch action {
	case agentclub.ProposalDecisionApprove:
		return "Approved"
	case agentclub.ProposalDecisionReject:
		return "Rejected"
	default:
		return "Recorded"
	}
}

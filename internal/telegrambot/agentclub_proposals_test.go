package telegrambot

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/agentclub"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
)

type telegramAgentClubHarness struct {
	scriptedHarness

	mu            sync.Mutex
	capabilities  agentclub.CapabilityListResponse
	proposals     agentclub.ProposalListResponse
	decision      agentclub.ProposalDecisionRequest
	decisionOwner gatewayapi.SessionOwner
	decisionCalls int
}

func (h *telegramAgentClubHarness) AgentClubCapabilities(context.Context) (agentclub.CapabilityListResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.capabilities, nil
}

func (h *telegramAgentClubHarness) AgentClubProposals(context.Context, string) (agentclub.ProposalListResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.proposals, nil
}

func (h *telegramAgentClubHarness) DecideAgentClubProposal(ctx context.Context, _ string, _ string, decision agentclub.ProposalDecisionRequest) (agentclub.ProposalDecisionResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.decision = decision
	h.decisionCalls++
	if owner, ok := gatewayclient.SessionOwnerFromContext(ctx); ok {
		h.decisionOwner = owner
	}
	proposal := h.proposals.Proposals[0]
	proposal.State = agentclub.ProposalStateApproved
	return agentclub.ProposalDecisionResponse{
		SchemaVersion: agentclub.SchemaVersion,
		DecisionID:    "decision-1",
		Action:        decision.Action,
		Proposal:      proposal,
	}, nil
}

func TestTelegramAgentClubCommandRendersPendingProposalButtons(t *testing.T) {
	proposal := telegramAgentClubProposal()
	harness := &telegramAgentClubHarness{
		capabilities: agentclub.CapabilityListResponse{
			SchemaVersion: agentclub.SchemaVersion,
			Capabilities: []agentclub.CapabilityView{{
				Descriptor: agentclub.CapabilityDescriptor{
					ID:       "event.review",
					Title:    "Review queue",
					Kind:     agentclub.CapabilityKindReview,
					Risk:     agentclub.RiskReadOnly,
					Dispatch: agentclub.DispatchAdmitOnly,
				},
			}},
		},
		proposals: agentclub.ProposalListResponse{
			SchemaVersion: agentclub.SchemaVersion,
			Proposals:     []agentclub.Proposal{proposal},
		},
	}
	var sentText string
	var replyMarkup map[string]any
	client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			sentText, _ = payload["text"].(string)
			replyMarkup, _ = payload["reply_markup"].(map[string]any)
			writeTelegramResult(w, SentMessage{MessageID: 11, Chat: Chat{ID: 123}})
		},
	})
	bot, msg := newAgentClubTestBot(t, client, harness, 1001)

	bot.handleMessage(context.Background(), msgWithText(msg, "/agentclub"))
	if !strings.Contains(sentText, "Agent-club") || !strings.Contains(sentText, "proposal-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatalf("agentclub text = %q", sentText)
	}
	if replyMarkup["inline_keyboard"] == nil {
		t.Fatalf("missing inline keyboard: %#v", replyMarkup)
	}
	rawButtons := replyMarkup["inline_keyboard"].([]any)[0].([]any)
	first := rawButtons[0].(map[string]any)
	callbackData, _ := first["callback_data"].(string)
	if !strings.Contains(callbackData, proposal.ProposalID) || !strings.Contains(callbackData, proposal.ProposalHash[:12]) {
		t.Fatalf("callback data should include proposal id and expected hash prefix: %q", callbackData)
	}
	if len(callbackData) > 64 {
		t.Fatalf("callback data too long for Telegram: %d %q", len(callbackData), callbackData)
	}
}

func TestTelegramAgentClubCallbackApprovesWithOwnerScopeAndFullHash(t *testing.T) {
	proposal := telegramAgentClubProposal()
	harness := &telegramAgentClubHarness{proposals: agentclub.ProposalListResponse{
		SchemaVersion: agentclub.SchemaVersion,
		Proposals:     []agentclub.Proposal{proposal},
	}}
	var callbackAnswer string
	var sentText string
	client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
		"answerCallbackQuery": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			callbackAnswer, _ = payload["text"].(string)
			writeTelegramResult(w, true)
		},
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			sentText, _ = payload["text"].(string)
			writeTelegramResult(w, SentMessage{MessageID: 12, Chat: Chat{ID: 123}})
		},
	})
	bot, msg := newAgentClubTestBot(t, client, harness, 1001)
	bot.handlePolledUpdate(context.Background(), Update{
		UpdateID: 77,
		CallbackQuery: &CallbackQuery{
			ID:      "callback-1",
			From:    User{ID: 1001},
			Message: &msg,
			Data:    encodeAgentClubCallback(agentclub.ProposalDecisionApprove, proposal),
		},
	})

	if callbackAnswer != "Approved" {
		t.Fatalf("callback answer = %q", callbackAnswer)
	}
	if harness.decision.Action != agentclub.ProposalDecisionApprove || harness.decision.ExpectedProposalHash != proposal.ProposalHash {
		t.Fatalf("decision = %#v", harness.decision)
	}
	if harness.decisionOwner.ClientType != "telegram" || harness.decisionOwner.TelegramUserID != 1001 || harness.decisionOwner.TelegramChatID != 123 {
		t.Fatalf("decision owner = %#v", harness.decisionOwner)
	}
	if !strings.Contains(sentText, "Agent-club decision") || !strings.Contains(sentText, "Approved proposal") {
		t.Fatalf("decision send = %q", sentText)
	}
}

func TestTelegramAgentClubCallbackRequiresOperator(t *testing.T) {
	proposal := telegramAgentClubProposal()
	harness := &telegramAgentClubHarness{proposals: agentclub.ProposalListResponse{
		SchemaVersion: agentclub.SchemaVersion,
		Proposals:     []agentclub.Proposal{proposal},
	}}
	var callbackAnswer string
	client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
		"answerCallbackQuery": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			callbackAnswer, _ = payload["text"].(string)
			writeTelegramResult(w, true)
		},
	})
	bot, msg := newAgentClubTestBot(t, client, harness, 1001)
	bot.handlePolledUpdate(context.Background(), Update{
		UpdateID: 78,
		CallbackQuery: &CallbackQuery{
			ID:      "callback-2",
			From:    User{ID: 2002},
			Message: &msg,
			Data:    encodeAgentClubCallback(agentclub.ProposalDecisionApprove, proposal),
		},
	})

	if !strings.Contains(callbackAnswer, "allowed Telegram operator") {
		t.Fatalf("callback answer = %q", callbackAnswer)
	}
	if harness.decisionCalls != 0 {
		t.Fatalf("unauthorized callback recorded decisions: %d", harness.decisionCalls)
	}
}

func newAgentClubTestBot(t *testing.T, client *Client, harness *telegramAgentClubHarness, operatorUser int64) (*Bot, Message) {
	t.Helper()
	bot, err := New(Options{
		BotToken:               "bottoken",
		StatePath:              t.TempDir() + "/state.json",
		Model:                  "deepseek-v4-flash",
		Profile:                "billy",
		AllowedChatIDs:         map[int64]bool{123: true},
		AllowedUserIDs:         map[int64]bool{operatorUser: true},
		AllowedOperatorUserIDs: map[int64]bool{operatorUser: true},
		SendEnabled:            true,
		DryRunDefault:          false,
	}, client, harness)
	if err != nil {
		t.Fatal(err)
	}
	msg := Message{Chat: Chat{ID: 123}, From: &User{ID: operatorUser}, Text: "/agentclub"}
	scope := messageChatScope(msg)
	bot.setChatState(scope.Key(), ChatState{SessionID: "session-1", Profile: "billy", Model: "deepseek-v4-flash", UpdatedAt: time.Now().UTC()})
	return bot, msg
}

func msgWithText(msg Message, text string) Message {
	msg.Text = text
	return msg
}

func telegramAgentClubProposal() agentclub.Proposal {
	hash := strings.Repeat("a", 64)
	return agentclub.Proposal{
		SchemaVersion: agentclub.SchemaVersion,
		ProposalID:    "proposal-" + hash[:32],
		SessionID:     "session-1",
		Source:        "hh_applicant_tool",
		Capability:    "safe_output.reply",
		ActionKind:    "reply",
		Risk:          agentclub.RiskExternalMutation,
		State:         agentclub.ProposalStatePending,
		ProposalHash:  hash,
		PayloadSHA256: strings.Repeat("b", 64),
		Preview:       "candidate reply",
		CreatedAt:     time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC),
	}
}

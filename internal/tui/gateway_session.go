package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayapi"
	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/toolrender"
)

type sessionReadyMsg struct {
	id string
}

type replayEventsMsg struct {
	events         []protocol.Event
	messages       []protocol.Message
	err            error
	fallbackCreate bool
}

func (m Model) createSessionCmd() tea.Cmd {
	return func() tea.Msg {
		profile := m.currentProfile()
		body, err := json.Marshal(gatewayapi.CreateSessionRequest{
			Messages: m.messages,
			Profile:  profile,
			Owner: gatewayapi.SessionOwner{
				ClientType: "tui",
				TUIChatID:  m.localChatID,
				Profile:    profile,
				Model:      m.currentModel(),
			},
		})
		if err != nil {
			return errMsg{err: err}
		}
		resp, err := m.gatewayRequest(context.Background(), http.DefaultClient, http.MethodPost, "/v1/sessions", body)
		if err != nil {
			return errMsg{err: err}
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return errMsg{err: fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return errMsg{err: err}
		}
		if out.ID == "" {
			return errMsg{err: fmt.Errorf("gateway returned empty session id")}
		}
		return sessionReadyMsg{id: out.ID}
	}
}

func (m Model) replayGatewayEventsCmd(fallbackCreate bool) tea.Cmd {
	sessionID := strings.TrimSpace(m.sessionID)
	afterSeq := m.lastGatewayEventSeq
	return func() tea.Msg {
		if sessionID == "" {
			return replayEventsMsg{err: fmt.Errorf("gateway session id is empty"), fallbackCreate: fallbackCreate}
		}
		path := fmt.Sprintf("/v1/sessions/%s/events?after_seq=%d&follow=false", url.PathEscape(sessionID), afterSeq)
		resp, err := m.gatewayRequest(context.Background(), http.DefaultClient, http.MethodGet, path, nil)
		if err != nil {
			return replayEventsMsg{err: err, fallbackCreate: fallbackCreate}
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return replayEventsMsg{err: fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(limited))), fallbackCreate: fallbackCreate}
		}
		dec := json.NewDecoder(resp.Body)
		var events []protocol.Event
		for {
			var event protocol.Event
			if err := dec.Decode(&event); err != nil {
				if err == io.EOF {
					break
				}
				return replayEventsMsg{err: err, fallbackCreate: fallbackCreate}
			}
			events = append(events, event)
		}
		messages, err := m.fetchGatewayMessagesForSession(sessionID)
		if err != nil {
			return replayEventsMsg{events: events, err: err, fallbackCreate: fallbackCreate}
		}
		return replayEventsMsg{events: events, messages: messages, fallbackCreate: fallbackCreate}
	}
}

func (m Model) gatewayRunRequest(prompt string, metadata ...map[string]string) gatewayapi.RunRequest {
	thinking := m.currentThinking()
	req := gatewayapi.RunRequest{
		Prompt:          prompt,
		ClientID:        "tui",
		Provider:        m.currentProvider(),
		Model:           m.currentModel(),
		Profile:         m.currentProfile(),
		Thinking:        thinking.kind,
		ReasoningEffort: thinking.effort,
		MaxToolRounds:   m.maxRounds,
		AccessMode:      m.currentAccessMode(),
	}
	if len(metadata) > 0 {
		req.Metadata = copyPromptMetadata(metadata[0])
	}
	return req
}

func (m Model) turnDiffPreviewCmd(changeID string) tea.Cmd {
	return func() tea.Msg {
		text, err := m.loadTurnDiffPreview(changeID)
		return turnDiffPreviewMsg{text: text, err: err}
	}
}

func (m Model) loadTurnDiffPreview(changeID string) (string, error) {
	if strings.TrimSpace(m.gatewayURL) == "" {
		return "", fmt.Errorf("diff preview requires gateway mode")
	}
	sessionID := strings.TrimSpace(m.sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("gateway session is not ready")
	}
	body, err := json.Marshal(gatewayapi.SessionUndoRequest{
		ChangeID: strings.TrimSpace(changeID),
		Preview:  true,
	})
	if err != nil {
		return "", err
	}
	var out gatewayapi.SessionUndoResponse
	path := fmt.Sprintf("/v1/sessions/%s/undo", url.PathEscape(sessionID))
	if err := m.gatewayJSON(http.MethodPost, path, body, &out); err != nil {
		return "", err
	}
	return formatTurnDiffPreview(out), nil
}

func formatTurnDiffPreview(out gatewayapi.SessionUndoResponse) string {
	var lines []string
	if strings.TrimSpace(out.Change.ChangeID) != "" {
		lines = append(lines, toolrender.TurnChangeDetails(out.Change))
	} else if strings.TrimSpace(out.ChangeID) != "" {
		lines = append(lines, "change: "+strings.TrimSpace(out.ChangeID))
	}
	patch := strings.TrimRight(out.Patch, "\n")
	if patch != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "preview:", patch)
	}
	if out.PatchTruncated {
		lines = append(lines, "[preview truncated]")
	}
	if len(out.Conflicts) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "conflicts:")
		for _, conflict := range out.Conflicts {
			lines = append(lines, "- "+conflict)
		}
	}
	if len(lines) == 0 {
		return "No turn diff preview is available."
	}
	return strings.Join(lines, "\n")
}

func (m Model) runGateway(prompt string, metadata ...map[string]string) {
	runReq := m.gatewayRunRequest(prompt, metadata...)
	client := &gatewayclient.Client{BaseURL: m.gatewayURL, Client: http.DefaultClient}
	result, err := client.RunSessionResult(context.Background(), m.sessionID, runReq, func(event protocol.Event) {
		m.events <- streamEventMsg{event: event}
	})
	needsReplay := result.StreamGaps > 0
	if err != nil {
		var seqGap *gatewayclient.EventSeqGapError
		if !errors.As(err, &seqGap) {
			m.events <- runDoneMsg{err: err}
			return
		}
		needsReplay = true
	}
	if needsReplay {
		if replayErr := client.ReplaySessionEvents(context.Background(), m.sessionID, result.LastSeq, func(event protocol.Event) {
			m.events <- streamEventMsg{event: event}
		}); replayErr != nil {
			m.events <- runDoneMsg{err: replayErr}
			return
		}
	}
	messages, err := m.fetchGatewayMessages()
	if err != nil {
		m.events <- runDoneMsg{err: err}
		return
	}
	m.events <- runDoneMsg{messages: messages}
}

func (m Model) fetchGatewayMessages() ([]protocol.Message, error) {
	return m.fetchGatewayMessagesForSession(m.sessionID)
}

func (m Model) fetchGatewayMessagesForSession(sessionID string) ([]protocol.Message, error) {
	path := fmt.Sprintf("/v1/sessions/%s", url.PathEscape(sessionID))
	resp, err := m.gatewayRequest(context.Background(), http.DefaultClient, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	var out struct {
		Messages []protocol.Message `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Messages, nil
}

func (m Model) gatewayJSON(method, path string, body []byte, out any) error {
	client := http.Client{Timeout: 30 * time.Second}
	resp, err := m.gatewayRequest(context.Background(), &client, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (m Model) gatewayRequest(ctx context.Context, client *http.Client, method, path string, body []byte) (*http.Response, error) {
	return (&gatewayclient.Client{BaseURL: m.gatewayURL, Client: client}).Do(ctx, method, path, body)
}

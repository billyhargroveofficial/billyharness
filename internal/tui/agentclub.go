package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
)

type agentClubStatusMsg struct {
	text string
	err  error
}

func (m Model) agentClubStatusCmd() tea.Cmd {
	return func() tea.Msg {
		text, err := m.agentClubStatusText(context.Background())
		return agentClubStatusMsg{text: text, err: err}
	}
}

func (m Model) agentClubStatusText(ctx context.Context) (string, error) {
	if strings.TrimSpace(m.gatewayURL) == "" {
		return "", fmt.Errorf("agent-club view requires gateway mode")
	}
	sessionID := strings.TrimSpace(m.sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("gateway session is not ready")
	}
	client := gatewayclient.New(m.gatewayURL)
	capabilities, err := client.AgentClubCapabilities(ctx)
	if err != nil {
		return "", err
	}
	proposals, err := client.AgentClubProposals(m.gatewayScopedContext(ctx), sessionID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(gatewayclient.FormatAgentClubCapabilities(capabilities)) +
		"\n\n" +
		strings.TrimSpace(gatewayclient.FormatAgentClubProposals(proposals)), nil
}

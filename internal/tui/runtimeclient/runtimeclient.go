package runtimeclient

import (
	"context"

	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/mcpstatus"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/runtimehost"
)

type Settings = runtimehost.Settings

func InitialMessages(settings config.InstructionSettings) []protocol.Message {
	return runtimehost.InitialMessages(settings)
}

func RunLocal(ctx context.Context, settings Settings, messages []protocol.Message, prompt string, refs []protocol.AttachmentRef, metadata map[string]string, onEvent func(protocol.Event)) ([]protocol.Message, error) {
	host, err := runtimehost.NewFromSettings(ctx, settings)
	if err != nil {
		return nil, err
	}
	defer host.Close()
	a := host.Agent()
	msgs := append([]protocol.Message(nil), messages...)
	msgs = append(msgs, protocol.UserMessage(prompt, refs))
	return a.RunMessagesWithPromptOptions(ctx, msgs, runtimehost.PromptSubmitOptions("tui", metadata), onEvent)
}

func MCPStatus(ctx context.Context, settings Settings) (mcpstatus.Response, error) {
	return runtimehost.MCPStatus(ctx, settings)
}

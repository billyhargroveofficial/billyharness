package telegrambot

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestTelegramOperatorCommandPolicy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		message  Message
		wantText string
	}{
		{
			name:     "group member",
			message:  Message{Chat: Chat{ID: 123, Type: "supergroup"}, From: &User{ID: 2002}, Text: "/config"},
			wantText: "allowed Telegram operator",
		},
		{
			name:     "anonymous group sender",
			message:  Message{Chat: Chat{ID: 123, Type: "supergroup"}, Text: "/config"},
			wantText: "identified Telegram operator",
		},
		{
			name:     "bot sender",
			message:  Message{Chat: Chat{ID: 123, Type: "supergroup"}, From: &User{ID: 1001, IsBot: true}, Text: "/config"},
			wantText: "human Telegram operator",
		},
		{
			name:     "malformed private sender",
			message:  Message{Chat: Chat{ID: 1001, Type: "private"}, Text: "/auth"},
			wantText: "identified Telegram operator",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sentText string
			client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
				"sendMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
					sentText, _ = payload["text"].(string)
					writeTelegramResult(w, SentMessage{MessageID: 11, Chat: tc.message.Chat})
				},
			})
			bot, err := New(Options{
				BotToken:               "bottoken",
				StatePath:              t.TempDir() + "/state.json",
				AllowedChatIDs:         map[int64]bool{123: true, 1001: true},
				AllowedUserIDs:         map[int64]bool{1001: true},
				AllowedOperatorUserIDs: map[int64]bool{1001: true},
				SendEnabled:            true,
				DryRunDefault:          false,
			}, client, scriptedHarness{configStatus: "billyharness config\napi_key: [redacted]"})
			if err != nil {
				t.Fatal(err)
			}

			bot.handleMessage(context.Background(), tc.message)
			if !strings.Contains(sentText, tc.wantText) {
				t.Fatalf("policy response = %q, want %q", sentText, tc.wantText)
			}
			if strings.Contains(sentText, "billyharness config") {
				t.Fatalf("unauthorized operator command reached config handler: %q", sentText)
			}
		})
	}
}

func TestTelegramOperatorCommandAllowsConfiguredGroupOperator(t *testing.T) {
	var sentText string
	var parseMode string
	client := newTelegramAPIClient(t, "bottoken", map[string]telegramAPIHandler{
		"sendMessage": func(w http.ResponseWriter, _ *http.Request, payload map[string]any) {
			sentText, _ = payload["text"].(string)
			parseMode, _ = payload["parse_mode"].(string)
			writeTelegramResult(w, SentMessage{MessageID: 11, Chat: Chat{ID: 123}})
		},
	})
	bot, err := New(Options{
		BotToken:               "bottoken",
		StatePath:              t.TempDir() + "/state.json",
		AllowedChatIDs:         map[int64]bool{123: true},
		AllowedOperatorUserIDs: map[int64]bool{1001: true},
		SendEnabled:            true,
		DryRunDefault:          false,
	}, client, scriptedHarness{configStatus: "billyharness config\napi_key: [redacted]"})
	if err != nil {
		t.Fatal(err)
	}

	bot.handleMessage(context.Background(), Message{Chat: Chat{ID: 123, Type: "supergroup"}, From: &User{ID: 1001}, Text: "/config"})
	if parseMode != "HTML" || !strings.Contains(sentText, "<b>Config</b>") || !strings.Contains(sentText, "billyharness config") {
		t.Fatalf("operator config response parse=%q text=%q", parseMode, sentText)
	}
}

func TestTelegramDryRunGroupSecretCommandDoesNotPersist(t *testing.T) {
	harness := &telegramAuthHarness{}
	bot, err := New(Options{
		BotToken:               "bottoken",
		StatePath:              t.TempDir() + "/state.json",
		AllowedChatIDs:         map[int64]bool{123: true},
		AllowedOperatorUserIDs: map[int64]bool{1001: true},
		SendEnabled:            true,
		DryRunDefault:          true,
	}, nil, harness)
	if err != nil {
		t.Fatal(err)
	}

	const secret = "sk-test-telegram-secret"
	bot.handleMessage(context.Background(), Message{MessageID: 77, Chat: Chat{ID: 123, Type: "supergroup"}, From: &User{ID: 1001}, Text: "/auth deepseek " + secret})

	harness.mu.Lock()
	saved := harness.savedDeepSeekKey
	harness.mu.Unlock()
	if saved != "" {
		t.Fatalf("dry-run group secret command persisted key = %q", saved)
	}
}

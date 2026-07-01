package telegrambot

import (
	"context"
	"log"
	"strings"
	"time"
)

func (b *Bot) Run(ctx context.Context) error {
	if b.opts.SendEnabled && !b.opts.DryRunDefault {
		if err := b.client.DeleteWebhook(ctx); err != nil {
			return err
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		updates, err := b.client.GetUpdates(ctx, b.nextOffset(), b.opts.PollTimeoutSec)
		if err != nil {
			log.Printf("telegram getUpdates: %v", err)
			sleep(ctx, 2*time.Second)
			continue
		}
		for _, update := range updates {
			b.handlePolledUpdate(ctx, update)
		}
	}
}

func (b *Bot) handlePolledUpdate(ctx context.Context, update Update) {
	if update.Message == nil || strings.TrimSpace(update.Message.Text) == "" {
		b.ackIgnoredUpdate(update, "empty_message")
		return
	}
	msg := *update.Message
	text := strings.TrimSpace(msg.Text)
	if !b.allowed(msg) {
		b.handleMessage(ctx, msg)
		b.ackIgnoredUpdate(update, "not_allowlisted")
		return
	}
	if strings.HasPrefix(text, "/") {
		b.handleMessage(ctx, msg)
		b.ackIgnoredUpdate(update, "command_handled")
		return
	}
	answered, answerErr := b.answerPendingUserInput(ctx, msg, update.UpdateID)
	if answered {
		if answerErr == nil {
			b.ackOffset(update.UpdateID)
		}
		return
	}
	admission, err := b.admitTelegramPromptUpdate(ctx, update)
	if err != nil {
		log.Printf("telegram prompt admission failed update=%d chat=%d: %v", update.UpdateID, msg.Chat.ID, err)
		return
	}
	b.ackOffset(update.UpdateID)
	if admission.SkipRun {
		log.Printf("telegram duplicate update skipped chat=%d key=%s update=%d input=%s state=%s",
			msg.Chat.ID, messageChatKey(msg), update.UpdateID, admission.InputID, admission.Response.State)
		return
	}
	admission.InputSeq = b.interruptActiveRunForInput(msg, messageChatScope(msg), false)
	go b.handleMessageWithAdmission(ctx, msg, admission)
}

func (b *Bot) ackIgnoredUpdate(update Update, reason string) {
	if err := b.admit.RecordIgnored(update, reason); err != nil {
		log.Printf("telegram ignored-update admission failed update=%d reason=%s: %v", update.UpdateID, reason, err)
		return
	}
	b.ackOffset(update.UpdateID)
}

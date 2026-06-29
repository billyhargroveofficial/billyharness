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
			// Telegram admission is currently at-most-once: persist the offset
			// before handling so commands are not replayed after a crash.
			b.ackOffset(update.UpdateID)
			if update.Message == nil || strings.TrimSpace(update.Message.Text) == "" {
				continue
			}
			msg := *update.Message
			go b.handleMessage(ctx, msg)
		}
	}
}

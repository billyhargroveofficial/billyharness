package telegrambot

import (
	"context"
	"errors"
	"log"
	"time"
)

type progressTextFunc func(force bool, pulse int) string

const telegramProgressFailureBackoff = 10 * time.Second

func (b *Bot) startProgressEdits(ctx context.Context, chatID int64, messageID int, interval time.Duration, stop <-chan struct{}, text progressTextFunc) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := newTelegramTicker(interval)
		defer ticker.Stop()
		lastText := ""
		pulse := 0
		pausedUntil := time.Time{}
		flush := func(force bool) {
			if telegramNow().Before(pausedUntil) {
				return
			}
			body := text(force, pulse)
			if body != "" {
				pulse++
			}
			if body != "" && body != lastText {
				lastText = body
				if err := b.editProgress(ctx, chatID, messageID, body); err != nil {
					if progressEditDeadline(err) {
						pausedUntil = telegramNow().Add(telegramProgressFailureBackoff)
						return
					}
					log.Printf("telegram progress edit: %v", err)
				}
			}
		}
		flush(true)
		for {
			select {
			case <-stop:
				pausedUntil = time.Time{}
				flush(true)
				return
			case <-ticker.C():
				flush(false)
			}
		}
	}()
	return done
}

func progressEditDeadline(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}

func (b *Bot) startTypingIndicator(ctx context.Context, chatID int64, stop <-chan struct{}) <-chan struct{} {
	done := make(chan struct{})
	if !b.opts.SendEnabled || b.opts.DryRunDefault {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		send := func() {
			actionCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			defer cancel()
			if err := b.client.SendChatAction(actionCtx, chatID, "typing"); err != nil {
				if actionCtx.Err() != nil {
					return
				}
				log.Printf("telegram typing action: %v", err)
			}
		}
		send()
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				send()
			}
		}
	}()
	return done
}

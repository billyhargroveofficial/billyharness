package telegrambot

import (
	"context"
	"log"
	"strings"
	"time"
)

func (b *Bot) sendHTML(ctx context.Context, msg Message, text string) error {
	_, err := b.send(ctx, msg.Chat.ID, msg.ThreadID, text, "HTML", false)
	return err
}

func (b *Bot) sendPlain(ctx context.Context, msg Message, text string) error {
	_, err := b.send(ctx, msg.Chat.ID, msg.ThreadID, text, "", false)
	return err
}

func (b *Bot) send(ctx context.Context, chatID int64, threadID int, text, parseMode string, force bool) (SentMessage, error) {
	if !b.opts.SendEnabled || (b.opts.DryRunDefault && !force) {
		log.Printf("telegram dry-run send chat=%d thread=%d text=%q", chatID, threadID, preview(text, 300))
		return SentMessage{MessageID: int(time.Now().UnixNano() % 1_000_000), Chat: Chat{ID: chatID}}, nil
	}
	return b.client.SendMessage(ctx, chatID, text, parseMode, threadID)
}

func (b *Bot) edit(ctx context.Context, chatID int64, messageID int, text, parseMode string) error {
	if !b.opts.SendEnabled || b.opts.DryRunDefault {
		log.Printf("telegram dry-run edit chat=%d message=%d text=%q", chatID, messageID, preview(text, 300))
		return nil
	}
	editCtx, cancel := telegramEditContext(ctx)
	defer cancel()
	err := b.client.EditMessageText(editCtx, chatID, messageID, text, parseMode)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
		return nil
	}
	return err
}

func (b *Bot) editProgress(ctx context.Context, chatID int64, messageID int, text string) error {
	if !b.opts.SendEnabled || b.opts.DryRunDefault {
		log.Printf("telegram dry-run progress edit chat=%d message=%d text=%q", chatID, messageID, preview(text, 300))
		return nil
	}
	editCtx, cancel := telegramProgressEditContext(ctx)
	defer cancel()
	err := b.client.EditMessageText(editCtx, chatID, messageID, text, "")
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
		return nil
	}
	return err
}

func (b *Bot) sendRichMarkdown(ctx context.Context, chatID int64, threadID int, markdown string) (SentMessage, error) {
	if !b.opts.SendEnabled || b.opts.DryRunDefault {
		log.Printf("telegram dry-run send-rich chat=%d thread=%d text=%q", chatID, threadID, preview(markdown, 300))
		return SentMessage{MessageID: int(time.Now().UnixNano() % 1_000_000), Chat: Chat{ID: chatID}}, nil
	}
	return b.client.SendRichMessageMarkdown(ctx, chatID, markdown, threadID)
}

func (b *Bot) editRichMarkdown(ctx context.Context, chatID int64, messageID int, markdown string) error {
	if !b.opts.SendEnabled || b.opts.DryRunDefault {
		log.Printf("telegram dry-run edit-rich chat=%d message=%d text=%q", chatID, messageID, preview(markdown, 300))
		return nil
	}
	editCtx, cancel := telegramEditContext(ctx)
	defer cancel()
	err := b.client.EditMessageRichMarkdown(editCtx, chatID, messageID, markdown)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
		return nil
	}
	return err
}

func telegramEditContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, telegramEditTimeout)
}

func telegramProgressEditContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, telegramProgressEditTimeout)
}

func (b *Bot) delete(ctx context.Context, chatID int64, messageID int) {
	if !b.opts.SendEnabled || b.opts.DryRunDefault {
		log.Printf("telegram dry-run delete chat=%d message=%d", chatID, messageID)
		return
	}
	if err := b.client.DeleteMessage(ctx, chatID, messageID); err != nil {
		log.Printf("telegram delete: %v", err)
	}
}

func (b *Bot) finishRich(ctx context.Context, msg Message, placeholder SentMessage, renderer *Renderer, model, reasoning string) bool {
	chunks := DefaultRichStream().FinalChunks(renderer, model, reasoning)
	if len(chunks) == 0 {
		return false
	}
	placeholderRich := false
	var freshRichMessageIDs []int
	if err := b.editRichMarkdown(ctx, msg.Chat.ID, placeholder.MessageID, chunks[0]); err != nil {
		log.Printf("telegram final rich edit failed, trying fresh rich message: %v", err)
		created, sendErr := b.sendRichMarkdown(ctx, msg.Chat.ID, msg.ThreadID, chunks[0])
		if sendErr != nil {
			log.Printf("telegram final rich send failed, falling back to HTML: %v", sendErr)
			return false
		}
		freshRichMessageIDs = append(freshRichMessageIDs, created.MessageID)
	} else {
		placeholderRich = true
	}
	for _, chunk := range chunks[1:] {
		created, err := b.sendRichMarkdown(ctx, msg.Chat.ID, msg.ThreadID, chunk)
		if err != nil {
			log.Printf("telegram final rich chunk send failed: %v", err)
			for _, messageID := range freshRichMessageIDs {
				b.delete(ctx, msg.Chat.ID, messageID)
			}
			return false
		}
		freshRichMessageIDs = append(freshRichMessageIDs, created.MessageID)
	}
	if !placeholderRich {
		b.delete(ctx, msg.Chat.ID, placeholder.MessageID)
	}
	return true
}

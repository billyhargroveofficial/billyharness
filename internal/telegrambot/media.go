package telegrambot

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/attachments"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

type telegramMediaItem struct {
	Source       string
	FileID       string
	FileUniqueID string
	FileName     string
	MIMEType     string
	FileSize     int64
	Width        int
	Height       int
}

type telegramDurableInputError struct {
	Reason      string
	UserMessage string
	Err         error
}

func (e *telegramDurableInputError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Reason
}

func (e *telegramDurableInputError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func telegramMessagePrompt(msg Message) string {
	if text := strings.TrimSpace(msg.Text); text != "" {
		return text
	}
	return strings.TrimSpace(msg.Caption)
}

func telegramMessageHasMedia(msg Message) bool {
	return len(msg.Photo) > 0 || msg.Document != nil
}

func telegramMessageProcessable(msg Message) bool {
	return telegramMessagePrompt(msg) != "" || telegramMessageHasMedia(msg)
}

func (b *Bot) prepareTelegramAttachments(ctx context.Context, msg Message, state ChatState) ([]protocol.AttachmentRef, error) {
	items := telegramMediaItems(msg)
	if len(items) == 0 {
		return nil, nil
	}
	if err := modelinfo.ValidateCapabilityPolicy(modelinfo.CapabilityPolicyRequest{
		Model:              state.Model,
		RequireVisionInput: true,
	}); err != nil {
		return nil, telegramDurableInput("vision_unsupported", telegramVisionUnsupportedMessage(state.Model), err)
	}
	store := attachments.DefaultStore()
	refs := make([]protocol.AttachmentRef, 0, len(items))
	for _, item := range items {
		if err := validateTelegramMediaItem(item, store); err != nil {
			return nil, err
		}
		file, err := b.client.GetFile(ctx, item.FileID)
		if err != nil {
			return nil, fmt.Errorf("telegram getFile %s: %w", item.Source, err)
		}
		if file.FileSize > store.MaxImageBytes {
			return nil, telegramDurableInput("attachment_too_large",
				fmt.Sprintf("Telegram image is too large (%d bytes; max %d).", file.FileSize, store.MaxImageBytes),
				nil)
		}
		data, err := b.client.DownloadFile(ctx, file.FilePath, store.MaxImageBytes)
		if err != nil {
			return nil, fmt.Errorf("telegram download %s: %w", item.Source, err)
		}
		ref, err := store.StoreImageBytes(telegramAttachmentFileName(item, file), data, "")
		if err != nil {
			if telegramAttachmentErrorDurable(err) {
				return nil, telegramDurableInput("attachment_invalid", "Telegram media is not a supported image. Send a PNG, JPEG, or GIF image.", err)
			}
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func telegramMediaItems(msg Message) []telegramMediaItem {
	var items []telegramMediaItem
	if len(msg.Photo) > 0 {
		photo := bestTelegramPhoto(msg.Photo)
		if strings.TrimSpace(photo.FileID) != "" {
			name := "telegram-photo"
			if photo.FileUniqueID != "" {
				name += "-" + safeTelegramName(photo.FileUniqueID)
			}
			name += ".jpg"
			items = append(items, telegramMediaItem{
				Source:       "photo",
				FileID:       photo.FileID,
				FileUniqueID: photo.FileUniqueID,
				FileName:     name,
				MIMEType:     "image/jpeg",
				FileSize:     photo.FileSize,
				Width:        photo.Width,
				Height:       photo.Height,
			})
		}
	}
	if msg.Document != nil && strings.TrimSpace(msg.Document.FileID) != "" {
		doc := *msg.Document
		items = append(items, telegramMediaItem{
			Source:       "document",
			FileID:       doc.FileID,
			FileUniqueID: doc.FileUniqueID,
			FileName:     doc.FileName,
			MIMEType:     doc.MIMEType,
			FileSize:     doc.FileSize,
		})
	}
	return items
}

func bestTelegramPhoto(photos []PhotoSize) PhotoSize {
	var best PhotoSize
	bestScore := int64(-1)
	for _, photo := range photos {
		score := photo.FileSize
		if score <= 0 && photo.Width > 0 && photo.Height > 0 {
			score = int64(photo.Width) * int64(photo.Height)
		}
		if score >= bestScore {
			best = photo
			bestScore = score
		}
	}
	return best
}

func validateTelegramMediaItem(item telegramMediaItem, store attachments.Store) error {
	if strings.TrimSpace(item.FileID) == "" {
		return telegramDurableInput("attachment_missing_file_id", "Telegram media did not include a downloadable file id.", nil)
	}
	mimeType := strings.ToLower(strings.TrimSpace(item.MIMEType))
	if item.Source == "document" && mimeType != "" && !strings.HasPrefix(mimeType, "image/") {
		return telegramDurableInput("attachment_not_image", "Telegram document is not an image. Send a PNG, JPEG, or GIF image.", nil)
	}
	if item.FileSize > store.MaxImageBytes {
		return telegramDurableInput("attachment_too_large",
			fmt.Sprintf("Telegram image is too large (%d bytes; max %d).", item.FileSize, store.MaxImageBytes),
			nil)
	}
	return nil
}

func telegramAttachmentFileName(item telegramMediaItem, file TelegramFile) string {
	if name := strings.TrimSpace(item.FileName); name != "" {
		return name
	}
	if base := path.Base(strings.TrimSpace(file.FilePath)); base != "." && base != "/" && base != "" {
		return base
	}
	if item.FileUniqueID != "" {
		return safeTelegramName(item.FileUniqueID) + ".jpg"
	}
	return "telegram-" + item.Source + ".jpg"
}

func safeTelegramName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "image"
	}
	var out strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			out.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			out.WriteRune(r)
		case r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == '-', r == '_':
			out.WriteRune(r)
		default:
			out.WriteByte('_')
		}
	}
	if out.Len() == 0 {
		return "image"
	}
	return out.String()
}

func telegramVisionUnsupportedMessage(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		model = "the current model"
	}
	return "Image input is unsupported for " + model + ". Switch to a Codex/OpenAI model with /model gpt-5.4, or resend text only."
}

func telegramDurableInput(reason, userMessage string, err error) error {
	return &telegramDurableInputError{
		Reason:      strings.TrimSpace(reason),
		UserMessage: strings.TrimSpace(userMessage),
		Err:         err,
	}
}

func telegramAttachmentErrorDurable(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"attachment is empty",
		"unsupported image mime type",
		"decode image metadata",
		"image dimensions",
		"image width",
		"image height",
		"image pixels",
		"max is " + strconv.FormatInt(attachments.DefaultMaxImageBytes, 10),
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

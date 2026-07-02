package protocol

import "strings"

type MessagePartType string

const (
	MessagePartText       MessagePartType = "text"
	MessagePartAttachment MessagePartType = "attachment"
)

type AttachmentKind string

const (
	AttachmentKindImage AttachmentKind = "image"
)

type AttachmentDetail string

const (
	AttachmentDetailAuto AttachmentDetail = "auto"
	AttachmentDetailLow  AttachmentDetail = "low"
	AttachmentDetailHigh AttachmentDetail = "high"
)

type MessagePart struct {
	Type       MessagePartType `json:"type"`
	Text       string          `json:"text,omitempty"`
	Attachment *AttachmentRef  `json:"attachment,omitempty"`
}

type AttachmentRef struct {
	ID         string           `json:"id"`
	Kind       AttachmentKind   `json:"kind,omitempty"`
	StorageRef string           `json:"storage_ref,omitempty"`
	FileName   string           `json:"file_name,omitempty"`
	MIMEType   string           `json:"mime_type,omitempty"`
	SizeBytes  int64            `json:"size_bytes,omitempty"`
	Width      int              `json:"width,omitempty"`
	Height     int              `json:"height,omitempty"`
	SHA256     string           `json:"sha256,omitempty"`
	Detail     AttachmentDetail `json:"detail,omitempty"`
}

func TextPart(text string) MessagePart {
	return MessagePart{Type: MessagePartText, Text: text}
}

func AttachmentPart(ref AttachmentRef) MessagePart {
	return MessagePart{Type: MessagePartAttachment, Attachment: &ref}
}

func UserMessage(text string, attachments []AttachmentRef) Message {
	return Message{
		Role:    RoleUser,
		Content: text,
		Parts:   PartsFromTextAndAttachments(text, attachments),
	}
}

func PartsFromTextAndAttachments(text string, attachments []AttachmentRef) []MessagePart {
	if len(attachments) == 0 {
		return nil
	}
	parts := make([]MessagePart, 0, len(attachments)+1)
	if text != "" {
		parts = append(parts, TextPart(text))
	}
	for _, attachment := range attachments {
		parts = append(parts, AttachmentPart(attachment))
	}
	return parts
}

func (m Message) MessageText() string {
	return MessageText(m)
}

func MessageText(msg Message) string {
	if msg.Content != "" || len(msg.Parts) == 0 {
		return msg.Content
	}
	var parts []string
	for _, part := range msg.Parts {
		if part.Type == MessagePartText && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (m Message) MessagePartsOrText() []MessagePart {
	return MessagePartsOrText(m)
}

func MessagePartsOrText(msg Message) []MessagePart {
	if len(msg.Parts) > 0 {
		return CloneMessageParts(msg.Parts)
	}
	text := MessageText(msg)
	if text == "" {
		return nil
	}
	return []MessagePart{TextPart(text)}
}

func (m Message) AttachmentCount() int {
	return AttachmentCount(m)
}

func (m Message) ImageAttachmentCount() int {
	return ImageAttachmentCount(m)
}

func (m Message) HasImageAttachment() bool {
	return ImageAttachmentCount(m) > 0
}

func AttachmentCount(msg Message) int {
	count := 0
	for _, part := range msg.Parts {
		if part.Type == MessagePartAttachment && part.Attachment != nil && part.Attachment.ID != "" {
			count++
		}
	}
	return count
}

func ImageAttachmentCount(msg Message) int {
	count := 0
	for _, part := range msg.Parts {
		if part.Type != MessagePartAttachment || part.Attachment == nil || part.Attachment.ID == "" {
			continue
		}
		if isImageAttachment(*part.Attachment) {
			count++
		}
	}
	return count
}

func isImageAttachment(ref AttachmentRef) bool {
	return ref.Kind == AttachmentKindImage || strings.HasPrefix(strings.ToLower(ref.MIMEType), "image/")
}

func MessageAttachmentCount(messages []Message) int {
	count := 0
	for _, msg := range messages {
		count += AttachmentCount(msg)
	}
	return count
}

func MessageImageAttachmentCount(messages []Message) int {
	count := 0
	for _, msg := range messages {
		count += ImageAttachmentCount(msg)
	}
	return count
}

func MessageImageSubmissionCount(messages []Message) int {
	count := 0
	for _, msg := range messages {
		if msg.Role == RoleUser && msg.HasImageAttachment() {
			count++
		}
	}
	return count
}

func CloneMessageParts(parts []MessagePart) []MessagePart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]MessagePart, len(parts))
	for i, part := range parts {
		out[i] = part
		if part.Attachment != nil {
			attachment := *part.Attachment
			out[i].Attachment = &attachment
		}
	}
	return out
}

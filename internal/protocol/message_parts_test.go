package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageJSONBackwardCompatibility(t *testing.T) {
	raw := []byte(`{"role":"user","content":"hello"}`)
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Role != RoleUser || msg.Content != "hello" || msg.MessageText() != "hello" {
		t.Fatalf("legacy message = %#v", msg)
	}
	parts := msg.MessagePartsOrText()
	if len(parts) != 1 || parts[0].Type != MessagePartText || parts[0].Text != "hello" {
		t.Fatalf("legacy parts = %#v", parts)
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "parts") {
		t.Fatalf("legacy message unexpectedly gained parts: %s", encoded)
	}
}

func TestMultipartMessageJSONRoundTrip(t *testing.T) {
	msg := Message{
		Role:    RoleUser,
		Content: "caption",
		Parts: []MessagePart{
			TextPart("caption"),
			AttachmentPart(AttachmentRef{
				ID:         "att_abc123",
				Kind:       AttachmentKindImage,
				StorageRef: "attachments/att_abc123.png",
				FileName:   "screen.png",
				MIMEType:   "image/png",
				SizeBytes:  1234,
				Width:      640,
				Height:     480,
				SHA256:     strings.Repeat("a", 64),
				Detail:     AttachmentDetailHigh,
			}),
		},
	}
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "base64") || strings.Contains(string(body), "data:image") {
		t.Fatalf("multipart message leaked image bytes: %s", body)
	}
	var got Message
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.MessageText() != "caption" || got.AttachmentCount() != 1 {
		t.Fatalf("round trip message = %#v", got)
	}
	parts := got.MessagePartsOrText()
	if len(parts) != 2 || parts[1].Attachment == nil || parts[1].Attachment.Width != 640 {
		t.Fatalf("round trip parts = %#v", parts)
	}
}

func TestUserMessageHelperPreservesTextOnlyShape(t *testing.T) {
	textOnly := UserMessage("hello", nil)
	if textOnly.Content != "hello" || len(textOnly.Parts) != 0 {
		t.Fatalf("text-only user message = %#v", textOnly)
	}
	withAttachment := UserMessage("look", []AttachmentRef{{ID: "att_test", Kind: AttachmentKindImage}})
	if withAttachment.Content != "look" || len(withAttachment.Parts) != 2 ||
		withAttachment.Parts[0].Text != "look" ||
		withAttachment.Parts[1].Attachment == nil ||
		withAttachment.Parts[1].Attachment.ID != "att_test" {
		t.Fatalf("attachment user message = %#v", withAttachment)
	}
}

func TestImageSubmissionCountsUserTurnsWithImages(t *testing.T) {
	messages := []Message{
		UserMessage("first", []AttachmentRef{{ID: "att_one", Kind: AttachmentKindImage}}),
		UserMessage("second", []AttachmentRef{
			{ID: "att_two", Kind: AttachmentKindImage},
			{ID: "att_three", MIMEType: "image/png"},
		}),
		{Role: RoleAssistant, Parts: []MessagePart{AttachmentPart(AttachmentRef{ID: "att_ignored", Kind: AttachmentKindImage})}},
		UserMessage("text only", nil),
	}
	if got := MessageAttachmentCount(messages); got != 4 {
		t.Fatalf("MessageAttachmentCount = %d, want 4", got)
	}
	if got := MessageImageAttachmentCount(messages); got != 4 {
		t.Fatalf("MessageImageAttachmentCount = %d, want 4", got)
	}
	if got := MessageImageSubmissionCount(messages); got != 2 {
		t.Fatalf("MessageImageSubmissionCount = %d, want 2", got)
	}
}

func TestCloneMessagePartsDoesNotAliasAttachments(t *testing.T) {
	parts := []MessagePart{AttachmentPart(AttachmentRef{ID: "att_original", FileName: "a.png"})}
	cloned := CloneMessageParts(parts)
	cloned[0].Attachment.ID = "att_changed"
	if parts[0].Attachment.ID != "att_original" {
		t.Fatalf("clone aliased attachment: original=%#v clone=%#v", parts, cloned)
	}
}

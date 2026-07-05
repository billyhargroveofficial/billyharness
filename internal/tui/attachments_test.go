package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/clipboard"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func TestAttachSlashAddsAndRemovesImage(t *testing.T) {
	m := newTestModel(t)
	path := writeTUITestPNG(t, "screen.png", 2, 3)
	handled, cmd := m.handleSlashCommand("/attach " + path)
	if !handled || cmd != nil {
		t.Fatalf("/attach handled=%v cmd=%v", handled, cmd)
	}
	if len(m.attachments) != 1 || m.attachments[0].FileName != "screen.png" || m.attachments[0].Width != 2 || m.attachments[0].Height != 3 {
		t.Fatalf("attachments = %#v", m.attachments)
	}
	if chip := m.attachmentChipsView(); !strings.Contains(chip, "vision image screen.png 2x3") {
		t.Fatalf("chip = %q", chip)
	}
	handled, cmd = m.handleSlashCommand("/attach remove 1")
	if !handled || cmd != nil {
		t.Fatalf("/attach remove handled=%v cmd=%v", handled, cmd)
	}
	if len(m.attachments) != 0 {
		t.Fatalf("attachments after remove = %#v", m.attachments)
	}
}

func TestPastedImagePathBecomesPendingAttachment(t *testing.T) {
	m := newTestModel(t)
	path := writeTUITestPNG(t, "pasted.png", 1, 1)
	m.textarea.SetValue(path)
	next, cmd := m.send()
	updated := next.(Model)
	if cmd != nil {
		t.Fatalf("cmd = %#v", cmd)
	}
	if len(updated.attachments) != 1 || updated.textarea.Value() != "" || !strings.Contains(updated.attachmentChipsView(), "pasted.png") {
		t.Fatalf("updated attachments=%#v textarea=%q chips=%q", updated.attachments, updated.textarea.Value(), updated.attachmentChipsView())
	}
	if len(updated.blocks) != 0 || updated.busy {
		t.Fatalf("pasted path should attach without submitting: blocks=%#v busy=%v", updated.blocks, updated.busy)
	}
}

func TestSubmitWithAttachmentRejectsTextOnlyModelAndKeepsDraft(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("look")
	m.attachments = []protocol.AttachmentRef{{ID: "att_test", Kind: protocol.AttachmentKindImage, FileName: "screen.png"}}
	next, cmd := m.send()
	updated := next.(Model)
	if cmd != nil {
		t.Fatalf("cmd = %#v", cmd)
	}
	if !strings.Contains(updated.status, "image input unsupported by deepseek-v4-flash (text-only)") || updated.textarea.Value() != "look" ||
		len(updated.attachments) != 1 || len(updated.blocks) != 0 {
		t.Fatalf("updated status=%q textarea=%q attachments=%#v blocks=%#v", updated.status, updated.textarea.Value(), updated.attachments, updated.blocks)
	}
}

func TestGatewayRunRequestIncludesAttachments(t *testing.T) {
	m := newTestModel(t)
	ref := protocol.AttachmentRef{ID: "att_test", Kind: protocol.AttachmentKindImage, StorageRef: "att_test.png", FileName: "screen.png"}
	req := m.gatewayRunRequestWithAttachments("look", []protocol.AttachmentRef{ref})
	if req.Prompt != "look" || len(req.Attachments) != 1 || req.Attachments[0].ID != "att_test" {
		t.Fatalf("request = %#v", req)
	}
	ref.ID = "mutated"
	if req.Attachments[0].ID != "att_test" {
		t.Fatalf("request attachments aliased caller ref: %#v", req.Attachments)
	}
}

func TestSubmitWithAttachmentRendersTranscriptChip(t *testing.T) {
	m := newTestModel(t)
	if ok := m.setModel("gpt"); !ok {
		t.Fatal("failed to switch to gpt model")
	}
	m.textarea.SetValue("look")
	m.attachments = []protocol.AttachmentRef{{ID: "att_test", Kind: protocol.AttachmentKindImage, FileName: "screen.png", Width: 2, Height: 3}}
	next, _ := m.send()
	updated := next.(Model)
	if len(updated.attachments) != 0 || len(updated.blocks) == 0 {
		t.Fatalf("updated attachments=%#v blocks=%#v", updated.attachments, updated.blocks)
	}
	if content := updated.blocks[len(updated.blocks)-1].Content; !strings.Contains(content, "look") || !strings.Contains(content, "vision image screen.png 2x3") {
		t.Fatalf("user block content = %q", content)
	}
	if !strings.Contains(updated.status, "running") {
		t.Fatalf("vision-capable attachment should submit, status=%q", updated.status)
	}
}

func TestAttachImageBytesAddsAttachment(t *testing.T) {
	m := newTestModel(t)
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	ok, err := m.attachImageBytes(buf.Bytes(), "clipboard-test.png")
	if err != nil {
		t.Fatalf("attachImageBytes: %v", err)
	}
	if !ok {
		t.Fatal("attachImageBytes returned false")
	}
	if len(m.attachments) != 1 {
		t.Fatalf("attachments = %#v", m.attachments)
	}
	if m.attachments[0].FileName != "clipboard-test.png" {
		t.Fatalf("filename = %q", m.attachments[0].FileName)
	}
	if chip := m.attachmentChipsView(); !strings.Contains(chip, "clipboard-test.png") {
		t.Fatalf("chip = %q", chip)
	}
}

func TestAltVPastesRawClipboardImageAsPendingAttachment(t *testing.T) {
	m := newTestModel(t)
	m.width = 100
	m.height = 24
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	oldRead := readClipboardImage
	readClipboardImage = func() ([]byte, string, error) {
		return buf.Bytes(), "clipboard-raw.png", nil
	}
	t.Cleanup(func() { readClipboardImage = oldRead })

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'v', Text: "v", Mod: tea.ModAlt})
	updated := next.(Model)
	if cmd != nil {
		t.Fatalf("cmd = %#v", cmd)
	}
	if len(updated.attachments) != 1 || updated.attachments[0].FileName != "clipboard-raw.png" ||
		updated.attachments[0].Width != 2 || updated.attachments[0].Height != 3 {
		t.Fatalf("attachments = %#v", updated.attachments)
	}
	if updated.textarea.Value() != "" {
		t.Fatalf("Alt+V should not insert text, textarea=%q", updated.textarea.Value())
	}
	if len(updated.blocks) != 0 || updated.busy {
		t.Fatalf("Alt+V should attach without submitting: blocks=%#v busy=%v", updated.blocks, updated.busy)
	}
	if chip := updated.attachmentChipsView(); !strings.Contains(chip, "vision image clipboard-raw.png 2x3") {
		t.Fatalf("chip = %q", chip)
	}
}

func TestAltVNoImageDoesNotInsertText(t *testing.T) {
	m := newTestModel(t)
	m.textarea.SetValue("draft")
	oldRead := readClipboardImage
	readClipboardImage = func() ([]byte, string, error) {
		return nil, "", clipboard.ErrNoImage
	}
	t.Cleanup(func() { readClipboardImage = oldRead })

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'v', Text: "v", Mod: tea.ModAlt})
	updated := next.(Model)
	if cmd != nil {
		t.Fatalf("cmd = %#v", cmd)
	}
	if updated.textarea.Value() != "draft" {
		t.Fatalf("Alt+V with no image should preserve draft, textarea=%q", updated.textarea.Value())
	}
	if len(updated.attachments) != 0 {
		t.Fatalf("attachments = %#v", updated.attachments)
	}
	if !strings.Contains(updated.status, "no image in clipboard") {
		t.Fatalf("status = %q", updated.status)
	}
}

func writeTUITestPNG(t *testing.T, name string, width, height int) string {
	t.Helper()
	dir := t.TempDir()
	if realDir, err := filepath.EvalSymlinks(dir); err == nil {
		dir = realDir
	}
	path := filepath.Join(dir, name)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

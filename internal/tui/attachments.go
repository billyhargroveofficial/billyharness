package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/attachments"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/toolrender"
)

func (m *Model) attachImage(path string) (bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return false, fmt.Errorf("usage: /attach PATH")
	}
	ref, err := attachments.DefaultStore().ImportLocalImage(path, "")
	if err != nil {
		return false, err
	}
	m.attachments = append(m.attachments, ref)
	m.status = fmt.Sprintf("attached image %d", len(m.attachments))
	return true, nil
}

func (m *Model) applyAttachCommand(arg string) error {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return fmt.Errorf("usage: /attach PATH")
	}
	fields := strings.Fields(arg)
	if len(fields) > 0 {
		switch strings.ToLower(fields[0]) {
		case "remove", "rm", "delete", "detach":
			_, err := m.removeAttachment(strings.TrimSpace(strings.TrimPrefix(arg, fields[0])))
			return err
		case "clear":
			_, err := m.removeAttachment("clear")
			return err
		}
	}
	_, err := m.attachImage(arg)
	return err
}

func (m *Model) attachPastedImagePath(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || strings.Contains(text, "\n") {
		return false
	}
	switch strings.ToLower(filepath.Ext(text)) {
	case ".png", ".jpg", ".jpeg", ".gif":
	default:
		return false
	}
	if _, err := m.attachImage(text); err != nil {
		return false
	}
	m.textarea.SetValue("")
	m.textarea.SetHeight(1)
	return true
}

func (m *Model) removeAttachment(arg string) (bool, error) {
	arg = strings.TrimSpace(arg)
	switch strings.ToLower(arg) {
	case "", "last":
		if len(m.attachments) == 0 {
			return false, fmt.Errorf("no attachments")
		}
		m.attachments = m.attachments[:len(m.attachments)-1]
		m.status = "attachment removed"
		return true, nil
	case "clear", "all":
		if len(m.attachments) == 0 {
			return false, fmt.Errorf("no attachments")
		}
		m.attachments = nil
		m.status = "attachments cleared"
		return true, nil
	default:
		n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimPrefix(arg, "#"), "image "))
		if err != nil || n <= 0 || n > len(m.attachments) {
			return false, fmt.Errorf("unknown attachment %q", arg)
		}
		idx := n - 1
		m.attachments = append(m.attachments[:idx], m.attachments[idx+1:]...)
		m.status = "attachment removed"
		return true, nil
	}
}

func (m Model) attachmentChipsView() string {
	if len(m.attachments) == 0 {
		return ""
	}
	chips := make([]string, 0, len(m.attachments))
	for _, ref := range m.attachments {
		chips = append(chips, attachmentChip(ref))
	}
	line := strings.Join(chips, " ")
	if m.width > 0 && len(line) > max(1, m.width-2) {
		line = line[:max(1, m.width-3)] + "..."
	}
	return " " + line
}

func attachmentChip(ref protocol.AttachmentRef) string {
	return toolrender.VisionImageLabel(ref)
}

func promptWithAttachmentChips(prompt string, refs []protocol.AttachmentRef) string {
	prompt = strings.TrimSpace(prompt)
	lines := make([]string, 0, 1+len(refs))
	if prompt != "" {
		lines = append(lines, prompt)
	}
	for _, ref := range refs {
		lines = append(lines, attachmentChip(ref))
	}
	return strings.Join(lines, "\n")
}

func (m Model) attachmentsSupported() bool {
	if len(m.attachments) == 0 {
		return true
	}
	info := modelinfo.Lookup(m.currentModel())
	return info.VisionInput
}

func cloneAttachmentRefs(refs []protocol.AttachmentRef) []protocol.AttachmentRef {
	if len(refs) == 0 {
		return nil
	}
	return append([]protocol.AttachmentRef(nil), refs...)
}

package transcript

import (
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

const (
	ExportModeRich = "rich"
	ExportModeRaw  = "raw"
)

func NormalizeExportMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", ExportModeRich:
		return ExportModeRich
	case ExportModeRaw:
		return ExportModeRaw
	default:
		return ""
	}
}

func FormatCells(cells []Cell, mode string) string {
	mode = NormalizeExportMode(mode)
	if mode == "" {
		mode = ExportModeRich
	}
	var parts []string
	for _, cell := range cells {
		var text string
		if mode == ExportModeRaw {
			text = strings.TrimSpace(firstNonEmpty(cell.RawCopy, cell.Content, cell.Title))
		} else {
			text = formatRichCell(cell)
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func FormatMessages(messages []protocol.Message, mode string) string {
	return FormatCells(CellsFromMessages(messages), mode)
}

func FormatEvents(events []protocol.Event, mode string) string {
	return FormatCells(CellsFromEvents(events), mode)
}

func FormatSession(messages []protocol.Message, events []protocol.Event, mode string) string {
	mode = NormalizeExportMode(mode)
	if mode == "" {
		mode = ExportModeRich
	}
	messageText := strings.TrimSpace(FormatMessages(messages, mode))
	eventText := strings.TrimSpace(FormatEvents(events, mode))
	switch {
	case messageText == "":
		return eventText
	case eventText == "":
		return messageText
	case mode == ExportModeRich:
		return messageText + "\n\nEVENTS\n" + eventText
	default:
		return messageText + "\n\n" + eventText
	}
}

func CellsFromEvents(events []protocol.Event) []Cell {
	p := NewProjector()
	for _, event := range events {
		p.Apply(event)
	}
	return p.Cells()
}

func CellsFromMessages(messages []protocol.Message) []Cell {
	cells := make([]Cell, 0, len(messages))
	for i, message := range messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(string(message.Role)))
		cellType := CellType(kind)
		title := strings.ToUpper(kind)
		if kind == string(protocol.RoleAssistant) {
			cellType = CellTypeAssistantFinal
		}
		if kind == "" {
			kind = "message"
			cellType = CellTypeStatus
			title = "MESSAGE"
		}
		cells = append(cells, Cell{
			ID:       fmt.Sprintf("message-%d", i+1),
			Kind:     kind,
			CellType: cellType,
			Title:    title,
			Content:  content,
			RawCopy:  content,
		})
	}
	return cells
}

func formatRichCell(cell Cell) string {
	title := strings.TrimSpace(cell.Title)
	body := strings.TrimSpace(cell.Content)
	if body == "" {
		body = strings.TrimSpace(cell.RawCopy)
	}
	if title == "" {
		return body
	}
	if body == "" || strings.EqualFold(title, body) {
		return title
	}
	return title + "\n" + body
}

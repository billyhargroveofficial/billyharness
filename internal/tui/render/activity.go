package render

import (
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
)

type ActivityCell struct {
	Kind  string
	Title string
	Body  string
}

type ActivityStyles struct {
	Text      lipgloss.Style
	Guide     lipgloss.Style
	Tool      lipgloss.Style
	Reasoning lipgloss.Style
	Error     lipgloss.Style
	Status    lipgloss.Style
}

func RenderActivityBlock(cell ActivityCell, width int, styles ActivityStyles) string {
	header := activityTitleStyle(cell.Kind, styles).Render("• " + ActivityTitle(cell))
	body := strings.Trim(cell.Body, "\n")
	if body == "" {
		return header
	}
	return header + "\n" + RenderActivityBody(body, max(12, width-2), styles)
}

func ActivityTitle(cell ActivityCell) string {
	title := strings.TrimSpace(cell.Title)
	switch cell.Kind {
	case "reasoning":
		return "Thinking"
	case "tool":
		if title == "" || strings.EqualFold(title, "TOOL") {
			return "Called tool"
		}
		return title
	case "error":
		if title == "" || strings.EqualFold(title, "ERROR") {
			return "Error"
		}
		return title
	case "status":
		if title == "" {
			return "Status"
		}
		return titleCase(title)
	case "audit":
		if title == "" || strings.EqualFold(title, "AUDIT") {
			return "Tool audit"
		}
		return titleCase(title)
	case "user":
		return "user"
	case "assistant":
		return "assistant"
	default:
		if title != "" {
			return strings.ToLower(oneLinePreview(title, 72))
		}
		return strings.ToLower(cell.Kind)
	}
}

func RenderActivityBody(body string, width int, styles ActivityStyles) string {
	lines := trimEmptyLines(strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n"))
	if len(lines) == 0 {
		return ""
	}
	var out []string
	first := true
	contentW := max(8, width-4)
	for _, line := range lines {
		wrapped := wrapActivityLine(line, contentW)
		for _, part := range wrapped {
			prefix := "  │ "
			if first {
				prefix = "  └ "
				first = false
			}
			out = append(out, styles.Guide.Render(prefix)+styles.Text.Render(part))
		}
	}
	return strings.Join(out, "\n")
}

func activityTitleStyle(kind string, styles ActivityStyles) lipgloss.Style {
	switch kind {
	case "tool":
		return styles.Tool
	case "reasoning":
		return styles.Reasoning
	case "error":
		return styles.Error
	case "status", "audit":
		return styles.Status
	default:
		return styles.Status
	}
}

func trimEmptyLines(lines []string) []string {
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}

func wrapActivityLine(line string, width int) []string {
	width = max(1, width)
	if line == "" {
		return []string{""}
	}
	var out []string
	rest := line
	for xansi.StringWidth(rest) > width {
		part := xansi.Cut(rest, 0, width)
		if part == "" {
			break
		}
		out = append(out, part)
		rest = xansi.Cut(rest, width, xansi.StringWidth(rest))
	}
	out = append(out, rest)
	return out
}

func titleCase(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

func oneLinePreview(text string, maxChars int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if maxChars <= 0 || len([]rune(text)) <= maxChars {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:maxChars])) + "..."
}

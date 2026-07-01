package telegrambot

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func (r *Renderer) StreamPlainText(model, reasoning string, tools *ToolProgress) string {
	return r.StreamPlainTextPulse(model, reasoning, tools, 2)
}

func (r *Renderer) StreamPlainTextPulse(model, reasoning string, tools *ToolProgress, pulse int) string {
	content := strings.TrimSpace(r.assistantText())
	if content == "" {
		content = workingPulseText(pulse)
	}
	contentShown := telegramUTF16Len(content)
	elapsed := time.Since(r.Started).Round(time.Second)
	meta := "🧬 " + model + " · 🧠 " + reasoning + " · ⏱ " + elapsed.String()
	if eventPulse := r.eventPulseText(); eventPulse != "" {
		meta += " · " + eventPulse
	}
	if context := r.contextLine(); context != "" {
		meta += "\n" + context
	}
	header := spinnerFrame(pulse) + " Billyharness · Running\n" + meta + "\n\n"
	footer := r.footerLineWithoutContext()
	limit := telegramLiveProgressLimit
	toolBudget := limit - telegramUTF16Len(header) - telegramUTF16Len(footer) - 760
	if toolBudget < 360 {
		toolBudget = limit - telegramUTF16Len(header) - telegramUTF16Len(footer) - 180
	}
	var suffixParts []string
	if toolText := tools.PlainTextLimit(toolBudget); toolText != "" {
		suffixParts = append(suffixParts, toolText)
	}
	suffixParts = append(suffixParts, footer)
	suffix := "\n\n" + strings.Join(suffixParts, "\n\n")
	budget := limit - telegramUTF16Len(header) - telegramUTF16Len(suffix) - 16
	if budget < 0 {
		budget = 0
	}
	preview := streamContentPreview(content, budget)
	if contentShown > telegramUTF16Len(preview) && !strings.Contains(preview, "live tail") {
		preview = "… live tail, full answer will be sent when done\n" + strings.TrimPrefix(preview, "…\n")
	}
	text := header + preview + suffix
	return trimTelegramTailLimit(text, limit)
}

func workingPulseText(pulse int) string {
	if pulse < 0 {
		pulse = 0
	}
	return "Working" + strings.Repeat(".", pulse%3+1)
}

func spinnerFrame(pulse int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	if pulse < 0 {
		pulse = 0
	}
	return frames[pulse%len(frames)]
}

func (r *Renderer) eventPulseText() string {
	if r == nil || r.LastEventAt.IsZero() {
		return ""
	}
	age := time.Since(r.LastEventAt).Round(time.Second)
	if age < 0 {
		age = 0
	}
	label := "event"
	switch r.LastEventType {
	case protocol.EventAssistantDelta:
		label = "tokens"
	case protocol.EventAssistantReasoning:
		label = "thinking"
	case protocol.EventToolCallRequested, protocol.EventToolCallStarted, protocol.EventToolCallProgress:
		label = "tool"
	case protocol.EventToolCallFinished, protocol.EventToolCallFailed, protocol.EventToolCallAborted:
		label = "tool done"
	case protocol.EventModelCallStarted:
		label = "model"
	case protocol.EventProviderUsageUpdate:
		label = "usage"
	case protocol.EventStreamStillRunning:
		label = "still running"
	}
	if age <= 2*time.Second {
		return "🟢 " + label + " now"
	}
	return "⏳ " + label + " " + compactDuration(age) + " ago"
}

func compactDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		minutes := int(d / time.Minute)
		seconds := int((d % time.Minute) / time.Second)
		if seconds == 0 {
			return fmt.Sprintf("%dm", minutes)
		}
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	hours := int(d / time.Hour)
	minutes := int((d % time.Hour) / time.Minute)
	if minutes == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%02dm", hours, minutes)
}

func streamContentPreview(content string, budget int) string {
	if budget <= 0 {
		return "…"
	}
	if telegramUTF16Len(content) <= budget {
		return content
	}
	prefix := "…\n"
	available := budget - telegramUTF16Len(prefix)
	if available <= 0 {
		return prefix
	}
	runes := []rune(content)
	n := telegramRuneSuffixLen(runes, available)
	return prefix + string(runes[len(runes)-n:])
}

func telegramStillRunningText(value any) string {
	var event protocol.StreamStillRunningEvent
	bytes, _ := json.Marshal(value)
	_ = json.Unmarshal(bytes, &event)
	phase := strings.TrimSpace(event.Phase)
	if phase == "" {
		phase = "run"
	}
	var parts []string
	parts = append(parts, "still running", phase)
	if event.IdleMS > 0 {
		parts = append(parts, "idle "+compactDuration(time.Duration(event.IdleMS)*time.Millisecond))
	}
	if event.ElapsedMS > 0 {
		parts = append(parts, "elapsed "+compactDuration(time.Duration(event.ElapsedMS)*time.Millisecond))
	}
	return strings.Join(parts, " · ")
}

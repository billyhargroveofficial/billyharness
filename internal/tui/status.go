package tui

import (
	"fmt"
	"strconv"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

func (m Model) inlineStatusView() string {
	styles := m.styles()
	access := "Build"
	switch m.currentAccessMode() {
	case "plan":
		access = "Plan"
	case "guarded":
		access = "Guarded"
	default:
	}
	if access == "Build" && (m.dangerous || m.toolPolicy.AutoApproveDangerous) {
		access = "Full Access"
	}
	top := []statusSegment{
		{m.runStateText(), styles.statusState},
		{m.currentModel(), styles.statusModel},
		{"🧠 " + m.currentThinking().effortLabel(), styles.statusReasoning},
		{access, styles.statusAccess},
		{"Context " + m.contextText() + " used", styles.statusUsage},
		{m.costText(), styles.statusCost},
	}
	bottom := []statusSegment{}
	if m.lastCacheHitTok+m.lastCacheMissTok > 0 {
		bottom = append(bottom,
			statusSegment{"cache hit " + compactNumber(m.lastCacheHitTok), styles.statusUsage},
			statusSegment{"miss " + compactNumber(m.lastCacheMissTok), styles.statusUsage},
		)
	}
	if m.toolSummaryInTok > 0 || m.toolSummaryOutTok > 0 {
		bottom = append(bottom, statusSegment{
			"websum " + compactNumber(m.toolSummaryInTok) + "→" + compactNumber(m.toolSummaryOutTok),
			styles.statusUsage,
		})
	}
	if m.helperModelInTok > 0 || m.helperModelOutTok > 0 {
		bottom = append(bottom, statusSegment{"helper " + compactNumber(m.helperModelInTok) + "→" + compactNumber(m.helperModelOutTok), styles.statusDim})
	}
	if m.toolSummaryInTok > 0 || m.toolSummaryOutTok > 0 || m.helperModelAPITok > 0 {
		bottom = append(bottom, statusSegment{"sumapi " + compactNumber(m.helperModelAPITok), styles.statusDim})
	}
	bottom = append(bottom,
		statusSegment{"agent turns " + strconv.Itoa(m.modelCalls), styles.statusDim},
		statusSegment{"tools " + strconv.Itoa(m.toolCalls), styles.statusDim},
		statusSegment{"v" + m.version, styles.statusDim},
		statusSegment{"theme " + m.theme, styles.statusDim},
		statusSegment{"profile " + m.currentProfile(), styles.statusDim},
		statusSegment{"Main [" + shortID(m.localChatID) + "]", styles.statusDim},
	)
	width := max(1, m.statusContentWidth(styles))
	return renderStatusSegments(width, top, styles.statusSeparator) + "\n" +
		renderStatusSegments(width, bottom, styles.statusSeparator)
}

func (m Model) runStatusView() string {
	if !m.busy {
		return ""
	}
	styles := m.styles()
	elapsed := "0s"
	if !m.runStartedAt.IsZero() {
		elapsed = compactDuration(time.Since(m.runStartedAt))
	}
	state := m.status
	if state == "" || state == "running" {
		state = "agent working"
	}
	text := " " + m.spinner() + " " + state + " · " + elapsed
	return styles.runStatus.Width(m.statusContentWidth(styles)).Render(text)
}

func (m Model) runStateText() string {
	if !m.followOutput {
		return "scrolled"
	}
	if m.busy {
		elapsed := "0s"
		if !m.runStartedAt.IsZero() {
			elapsed = compactDuration(time.Since(m.runStartedAt))
		}
		return "running " + elapsed
	}
	if m.lastRunDuration > 0 {
		return m.status + " · last " + compactDuration(m.lastRunDuration)
	}
	return m.status
}

func (m Model) spinner() string {
	if len(spinnerFrames) == 0 {
		return "*"
	}
	return spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
}

func (m Model) contextText() string {
	used := m.contextTokens()
	window := m.runtime.ContextWindowTokens
	if window <= 0 {
		return compactNumber(used)
	}
	percent := float64(used) / float64(window) * 100
	if percent < 10 {
		return fmt.Sprintf("%.1f%%", percent)
	}
	return fmt.Sprintf("%.0f%%", percent)
}

func (m Model) costText() string {
	if modelinfo.Lookup(m.currentModel()).Subscription {
		return "cost subscription"
	}
	hitPrice, missPrice, outputPrice := m.prices()
	if hitPrice <= 0 && missPrice <= 0 && outputPrice <= 0 {
		return "cost n/a"
	}
	hit := m.cacheHitTok
	miss := m.cacheMissTok
	if hit == 0 && miss == 0 {
		miss = m.inputTok
	}
	cost := (float64(hit)/1_000_000)*hitPrice +
		(float64(miss)/1_000_000)*missPrice +
		(float64(m.outputTok)/1_000_000)*outputPrice
	return fmt.Sprintf("cost $%.6f", cost)
}

func (m Model) prices() (hit, miss, output float64) {
	hit = m.settings.CacheHitPricePer1MTokens
	miss = m.settings.CacheMissPricePer1MTokens
	output = m.settings.OutputPricePer1MTokens
	if hit > 0 || miss > 0 || output > 0 {
		if miss == 0 {
			miss = m.settings.InputPricePer1MTokens
		}
		return hit, miss, output
	}
	if pricing := modelinfo.Lookup(m.currentModel()).Pricing; pricing.CacheHitPer1M > 0 || pricing.CacheMissPer1M > 0 || pricing.OutputPer1M > 0 {
		return pricing.CacheHitPer1M, pricing.CacheMissPer1M, pricing.OutputPer1M
	}
	return 0, m.settings.InputPricePer1MTokens, m.settings.OutputPricePer1MTokens
}

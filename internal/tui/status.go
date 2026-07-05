package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/displayfmt"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

func (m Model) inlineStatusView() string {
	styles := m.styles()
	width := max(1, m.statusContentWidth(styles))
	modelSegment := m.inlineStatusModelSegment(styles, "")
	base := []statusSegment{
		{text: statusWorkspaceText(), style: styles.statusDim},
		{text: statusGitText(), style: styles.statusAccess},
		modelSegment,
	}
	if quota := m.codexRateLimitsStatusText(time.Now()); quota != "" {
		withQuota := append([]statusSegment(nil), base...)
		withQuota[2] = m.inlineStatusModelSegment(styles, quota)
		rendered := renderStatusSegments(width, withQuota, styles.statusSeparator)
		if strings.Contains(rendered, quota) {
			return rendered
		}
	}
	return renderStatusSegments(width, base, styles.statusSeparator)
}

func (m Model) inlineStatusModelSegment(styles themeStyles, quota string) statusSegment {
	model := "🤖 " + statusModelText(m.currentModel(), m.currentThinking().effortLabel())
	context := m.statusContextText()
	text := model
	rendered := styles.statusModel.Render(model)
	if context != "" {
		text += " " + context
		rendered += " " + styles.statusUsage.Render(context)
	}
	if quota != "" {
		text += "  " + quota
		rendered += "  " + styles.statusUsage.Render(quota)
	}
	return statusSegment{text: text, rendered: rendered}
}

func (m Model) runStatusView() string {
	if !m.busy {
		return ""
	}
	styles := m.styles()
	text := m.spinner() + " working"
	if !m.runStartedAt.IsZero() {
		text += " · " + compactDuration(time.Since(m.runStartedAt))
	}
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
	text := fmt.Sprintf("%s/%s %s", compactNumber(used), compactNumber(window), displayfmt.ContextPercentValue(percent))
	if m.runtime.ContextWindowSource == "override" {
		text += " override"
	}
	return text
}

func (m Model) contextCompactText() string {
	threshold := int64(m.runtime.ContextCompactTokens)
	if threshold <= 0 {
		return ""
	}
	window := m.runtime.ContextWindowTokens
	if window <= 0 {
		return compactNumber(threshold)
	}
	percent := float64(threshold) / float64(window) * 100
	text := fmt.Sprintf("%s %s", compactNumber(threshold), displayfmt.ContextPercentValue(percent))
	if m.runtime.ContextCompactSource == "override" {
		text += " override"
	}
	return text
}

func (m Model) contextPercentText() string {
	used := m.contextTokens()
	window := m.runtime.ContextWindowTokens
	if window <= 0 {
		return compactNumber(used)
	}
	return displayfmt.ContextPercent(used, window)
}

func (m Model) statusContextText() string {
	used := m.contextTokens()
	window := m.runtime.ContextWindowTokens
	if window <= 0 {
		return displayfmt.CompactContext(used)
	}
	percent := float64(used) / float64(window) * 100
	return displayfmt.FixedPercentValue(percent, 1) + " " + displayfmt.CompactContext(used)
}

func statusModelText(model, effort string) string {
	model = strings.TrimSpace(statusModelDisplay(model))
	effort = strings.TrimSpace(effort)
	if model == "" {
		model = "model"
	}
	if effort == "" || effort == "off" {
		return model
	}
	return model + " " + effort
}

func statusModelDisplay(model string) string {
	model = strings.TrimSpace(model)
	if strings.HasPrefix(strings.ToLower(model), "gpt-") {
		return strings.ReplaceAll(model, "-", " ")
	}
	return shortModel(model)
}

func statusWorkspaceText() string {
	info := cachedStatusGitInfo()
	name := info.rootName
	if name == "" {
		if cwd, err := os.Getwd(); err == nil {
			name = filepath.Base(cwd)
		}
	}
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "workspace"
	}
	return "📁 " + name
}

func statusGitText() string {
	info := cachedStatusGitInfo()
	if info.branch == "" {
		return ""
	}
	text := "⎇ " + info.branch
	if info.added > 0 || info.deleted > 0 {
		text += fmt.Sprintf("(+%d,-%d)", info.added, info.deleted)
	}
	return text
}

type statusGitInfo struct {
	rootName string
	branch   string
	added    int
	deleted  int
}

var statusGitInfoCache = struct {
	sync.Mutex
	cwd     string
	expires time.Time
	info    statusGitInfo
}{}

func cachedStatusGitInfo() statusGitInfo {
	cwd, err := os.Getwd()
	if err != nil {
		return statusGitInfo{}
	}
	now := time.Now()
	statusGitInfoCache.Lock()
	if statusGitInfoCache.cwd == cwd && now.Before(statusGitInfoCache.expires) {
		info := statusGitInfoCache.info
		statusGitInfoCache.Unlock()
		return info
	}
	statusGitInfoCache.Unlock()

	info := loadStatusGitInfo(cwd)

	statusGitInfoCache.Lock()
	statusGitInfoCache.cwd = cwd
	statusGitInfoCache.expires = now.Add(2 * time.Second)
	statusGitInfoCache.info = info
	statusGitInfoCache.Unlock()
	return info
}

func loadStatusGitInfo(cwd string) statusGitInfo {
	root, branch, ok := statusGitRootAndBranch(cwd)
	if !ok {
		return statusGitInfo{rootName: filepath.Base(cwd)}
	}
	added, deleted := statusGitDiff(root)
	return statusGitInfo{
		rootName: filepath.Base(filepath.FromSlash(root)),
		branch:   branch,
		added:    added,
		deleted:  deleted,
	}
}

func statusGitRootAndBranch(cwd string) (string, string, bool) {
	out, ok := runStatusGit(cwd, "rev-parse", "--show-toplevel", "--abbrev-ref", "HEAD")
	if !ok {
		return "", "", false
	}
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(out), "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return "", "", false
	}
	root := strings.TrimSpace(lines[0])
	branch := strings.TrimSpace(lines[1])
	if branch == "HEAD" || branch == "" {
		branch = "detached"
	}
	return root, branch, root != ""
}

func statusGitDiff(root string) (int, int) {
	out, ok := runStatusGit(root, "diff", "--numstat", "HEAD", "--")
	if !ok {
		return 0, 0
	}
	var added, deleted int
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "-" || fields[1] == "-" {
			continue
		}
		a, errA := strconv.Atoi(fields[0])
		d, errD := strconv.Atoi(fields[1])
		if errA == nil {
			added += a
		}
		if errD == nil {
			deleted += d
		}
	}
	return added, deleted
}

func runStatusGit(dir string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil || ctx.Err() != nil {
		return "", false
	}
	return string(out), true
}

func (m Model) costText() string {
	if modelinfo.Lookup(m.currentModel()).Subscription {
		return "subscription"
	}
	hitPrice, missPrice, outputPrice := m.prices()
	if hitPrice <= 0 && missPrice <= 0 && outputPrice <= 0 {
		return "model cost n/a"
	}
	hit := m.cacheHitTok
	miss := m.cacheMissTok
	if hit == 0 && miss == 0 {
		miss = m.inputTok
	}
	cost := (float64(hit)/1_000_000)*hitPrice +
		(float64(miss)/1_000_000)*missPrice +
		(float64(m.outputTok)/1_000_000)*outputPrice
	return fmt.Sprintf("model cost $%.6f", cost)
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

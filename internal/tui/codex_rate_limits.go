package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

const (
	codexRateLimitsSuccessInterval = time.Minute
	codexRateLimitsErrorInterval   = 2 * time.Minute
	codexRateLimitsTimeout         = 8 * time.Second
)

type codexRateLimitsMsg struct {
	snapshot  codexRateLimitSnapshot
	err       error
	fetchedAt time.Time
}

type codexRateLimitSnapshot struct {
	Primary      *codexRateLimitWindow
	Secondary    *codexRateLimitWindow
	PlanType     string
	ReachedType  string
	Credits      *codexRateLimitCredits
	ResetCredits *codexRateLimitResetCredits
}

type codexRateLimitWindow struct {
	UsedPercent        float64
	WindowDurationMins int
	ResetsAt           time.Time
}

type codexRateLimitCredits struct {
	HasCredits bool
	Unlimited  bool
	Balance    string
}

type codexRateLimitResetCredits struct {
	AvailableCount int
}

func (s codexRateLimitSnapshot) empty() bool {
	return s.Primary == nil && s.Secondary == nil
}

func (m Model) codexRateLimitsEnabled() bool {
	return m.currentProvider() == modelinfo.ProviderOpenAICodex || modelinfo.Lookup(m.currentModel()).Subscription
}

func (m *Model) maybeCodexRateLimitsCmd(now time.Time) tea.Cmd {
	if !m.codexRateLimitsEnabled() {
		m.codexRateLimitsRefreshing = false
		return nil
	}
	if m.codexRateLimitsRefreshing {
		return nil
	}
	if !m.codexRateLimitsNextRefresh.IsZero() && now.Before(m.codexRateLimitsNextRefresh) {
		return nil
	}
	m.codexRateLimitsRefreshing = true
	return m.codexRateLimitsCmd()
}

func (m Model) codexRateLimitsCmd() tea.Cmd {
	version := strings.TrimSpace(m.version)
	if version == "" {
		version = "dev"
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), codexRateLimitsTimeout)
		defer cancel()
		snapshot, err := fetchCodexRateLimits(ctx, version)
		return codexRateLimitsMsg{snapshot: snapshot, err: err, fetchedAt: time.Now()}
	}
}

func (m Model) codexRateLimitsTickCmd(after time.Duration) tea.Cmd {
	if after <= 0 {
		after = codexRateLimitsSuccessInterval
	}
	return tea.Tick(after, func(t time.Time) tea.Msg {
		return codexRateLimitsTickMsg(t)
	})
}

func (m Model) codexRateLimitsStatusText(now time.Time) string {
	if !m.codexRateLimitsEnabled() || m.codexRateLimits.empty() {
		return ""
	}
	parts := []string{}
	if text := formatCodexRateLimitWindow(m.codexRateLimits.Primary, now, true); text != "" {
		parts = append(parts, text)
	}
	if text := formatCodexRateLimitWindow(m.codexRateLimits.Secondary, now, false); text != "" {
		parts = append(parts, text)
	}
	return strings.Join(parts, "  ")
}

func formatCodexRateLimitWindow(window *codexRateLimitWindow, now time.Time, includeWindowLabel bool) string {
	if window == nil {
		return ""
	}
	parts := []string{"●"}
	if includeWindowLabel {
		if label := codexRateLimitWindowLabel(window.WindowDurationMins); label != "" {
			parts = append(parts, label)
		}
	}
	parts = append(parts, fmt.Sprintf("%.1f%%", window.UsedPercent))
	if reset := codexRateLimitResetDuration(now, window.ResetsAt); reset != "" {
		parts = append(parts, reset)
	}
	return strings.Join(parts, " ")
}

func codexRateLimitWindowLabel(minutes int) string {
	if minutes <= 0 {
		return ""
	}
	if minutes%1440 == 0 {
		return fmt.Sprintf("%dd", minutes/1440)
	}
	if minutes%60 == 0 {
		return fmt.Sprintf("%dh", minutes/60)
	}
	return fmt.Sprintf("%dm", minutes)
}

func codexRateLimitResetDuration(now, resetsAt time.Time) string {
	if resetsAt.IsZero() {
		return ""
	}
	d := resetsAt.Sub(now)
	if d < 0 {
		d = 0
	}
	totalMinutes := int((d + time.Minute - time.Nanosecond) / time.Minute)
	days := totalMinutes / (24 * 60)
	totalMinutes %= 24 * 60
	hours := totalMinutes / 60
	minutes := totalMinutes % 60
	parts := []string{}
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dhr", hours))
	}
	if minutes > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	return strings.Join(parts, " ")
}

func fetchCodexRateLimits(ctx context.Context, version string) (codexRateLimitSnapshot, error) {
	command, err := resolveCodexAppServerCommand()
	if err != nil {
		return codexRateLimitSnapshot{}, err
	}
	cmd := exec.CommandContext(ctx, command, "app-server")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return codexRateLimitSnapshot{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return codexRateLimitSnapshot{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return codexRateLimitSnapshot{}, err
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	writer := bufio.NewWriter(stdin)
	if err := writeCodexRPC(writer, map[string]any{
		"id":     0,
		"method": "initialize",
		"params": map[string]any{
			"clientInfo": map[string]any{
				"name":    "billyharness",
				"title":   "Billyharness",
				"version": version,
			},
			"capabilities": map[string]any{},
		},
	}); err != nil {
		return codexRateLimitSnapshot{}, err
	}
	if err := writeCodexRPC(writer, map[string]any{
		"method": "initialized",
		"params": map[string]any{},
	}); err != nil {
		return codexRateLimitSnapshot{}, err
	}
	if err := writeCodexRPC(writer, map[string]any{
		"id":     1,
		"method": "account/rateLimits/read",
	}); err != nil {
		return codexRateLimitSnapshot{}, err
	}
	if err := writer.Flush(); err != nil {
		return codexRateLimitSnapshot{}, err
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg codexRPCMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if rpcIDInt(msg.ID) == 0 && msg.Error != nil {
			return codexRateLimitSnapshot{}, fmt.Errorf("codex app-server initialize: %s", msg.Error.Message)
		}
		if rpcIDInt(msg.ID) != 1 {
			continue
		}
		if msg.Error != nil {
			return codexRateLimitSnapshot{}, fmt.Errorf("codex rate limits: %s", msg.Error.Message)
		}
		return parseCodexRateLimitsResult(msg.Result)
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return codexRateLimitSnapshot{}, ctx.Err()
		}
		return codexRateLimitSnapshot{}, err
	}
	if ctx.Err() != nil {
		return codexRateLimitSnapshot{}, ctx.Err()
	}
	if text := strings.TrimSpace(stderr.String()); text != "" {
		return codexRateLimitSnapshot{}, fmt.Errorf("codex app-server exited before rate limits response: %s", oneLinePreview(text, 180))
	}
	return codexRateLimitSnapshot{}, errors.New("codex app-server exited before rate limits response")
}

func writeCodexRPC(writer *bufio.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}
	return writer.WriteByte('\n')
}

func resolveCodexAppServerCommand() (string, error) {
	for _, candidate := range codexAppServerCommandCandidates() {
		if candidate == "" {
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
		if filepath.IsAbs(candidate) {
			if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
				return candidate, nil
			}
		}
	}
	return "", errors.New("codex app-server command not found")
}

func codexAppServerCommandCandidates() []string {
	candidates := []string{strings.TrimSpace(os.Getenv("BILLYHARNESS_CODEX_COMMAND"))}
	if runtime.GOOS == "windows" {
		if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
			candidates = append(candidates, filepath.Join(localAppData, "OpenAI", "Codex", "bin", "codex.exe"))
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			candidates = append(candidates, filepath.Join(home, "AppData", "Local", "OpenAI", "Codex", "bin", "codex.exe"))
		}
		candidates = append(candidates, "codex.exe")
	}
	candidates = append(candidates, "codex")

	seen := map[string]bool{}
	unique := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		unique = append(unique, candidate)
	}
	return unique
}

type codexRPCMessage struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *codexRPCError  `json:"error"`
}

type codexRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func rpcIDInt(raw json.RawMessage) int {
	var id int
	if err := json.Unmarshal(raw, &id); err == nil {
		return id
	}
	return -1
}

func parseCodexRateLimitsResult(data json.RawMessage) (codexRateLimitSnapshot, error) {
	var result rawCodexRateLimitsResult
	if err := json.Unmarshal(data, &result); err != nil {
		return codexRateLimitSnapshot{}, err
	}
	selected := result.RateLimits
	if byID := result.RateLimitsByLimitID["codex"]; byID != nil {
		selected = byID
	}
	if selected == nil {
		return codexRateLimitSnapshot{}, errors.New("codex rate limits response did not include a codex bucket")
	}
	snapshot := codexRateLimitSnapshot{
		Primary:      convertCodexRateLimitWindow(selected.Primary),
		Secondary:    convertCodexRateLimitWindow(selected.Secondary),
		PlanType:     selected.PlanType,
		ReachedType:  selected.RateLimitReachedType,
		Credits:      convertCodexCredits(selected.Credits),
		ResetCredits: convertCodexResetCredits(result.RateLimitResetCredits),
	}
	if snapshot.empty() {
		return codexRateLimitSnapshot{}, errors.New("codex rate limits response did not include quota windows")
	}
	return snapshot, nil
}

type rawCodexRateLimitsResult struct {
	RateLimits            *rawCodexRateLimit             `json:"rateLimits"`
	RateLimitsByLimitID   map[string]*rawCodexRateLimit  `json:"rateLimitsByLimitId"`
	RateLimitResetCredits *rawCodexRateLimitResetCredits `json:"rateLimitResetCredits"`
}

type rawCodexRateLimit struct {
	LimitID              string                    `json:"limitId"`
	Primary              *rawCodexRateLimitWindow  `json:"primary"`
	Secondary            *rawCodexRateLimitWindow  `json:"secondary"`
	PlanType             string                    `json:"planType"`
	RateLimitReachedType string                    `json:"rateLimitReachedType"`
	Credits              *rawCodexRateLimitCredits `json:"credits"`
}

type rawCodexRateLimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins int     `json:"windowDurationMins"`
	ResetsAt           int64   `json:"resetsAt"`
}

type rawCodexRateLimitCredits struct {
	HasCredits bool   `json:"hasCredits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance"`
}

type rawCodexRateLimitResetCredits struct {
	AvailableCount int `json:"availableCount"`
}

func convertCodexRateLimitWindow(raw *rawCodexRateLimitWindow) *codexRateLimitWindow {
	if raw == nil {
		return nil
	}
	window := &codexRateLimitWindow{
		UsedPercent:        raw.UsedPercent,
		WindowDurationMins: raw.WindowDurationMins,
	}
	if raw.ResetsAt > 0 {
		window.ResetsAt = time.Unix(raw.ResetsAt, 0)
	}
	return window
}

func convertCodexCredits(raw *rawCodexRateLimitCredits) *codexRateLimitCredits {
	if raw == nil {
		return nil
	}
	return &codexRateLimitCredits{
		HasCredits: raw.HasCredits,
		Unlimited:  raw.Unlimited,
		Balance:    raw.Balance,
	}
}

func convertCodexResetCredits(raw *rawCodexRateLimitResetCredits) *codexRateLimitResetCredits {
	if raw == nil {
		return nil
	}
	return &codexRateLimitResetCredits{AvailableCount: raw.AvailableCount}
}

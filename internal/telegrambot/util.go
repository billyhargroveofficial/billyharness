package telegrambot

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/billyhargroveofficial/billyharness/internal/gatewayclient"
	"github.com/billyhargroveofficial/billyharness/internal/modelinfo"
)

func modelAlias(value string) string {
	return modelinfo.NormalizeAlias(value)
}

func modelWithCapability(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		model = "the current model"
	}
	return model + " (" + modelinfo.InputCapabilityLabel(model) + ")"
}

func fallback(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func short(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func preview(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func gatewaySessionMissing(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gatewayclient.ErrSessionNotFound) {
		return true
	}
	text := strings.ToLower(err.Error())
	return (strings.Contains(text, "gateway run http 404") || strings.Contains(text, "gateway events http 404")) && strings.Contains(text, "session not found")
}

func durationSince(start, mark time.Time) string {
	if mark.IsZero() {
		return "n/a"
	}
	return mark.Sub(start).Round(time.Millisecond).String()
}

func DefaultStatePath() string {
	home := os.Getenv("BILLYHARNESS_HOME")
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil && userHome != "" {
			home = userHome + "/billyharness"
		} else {
			home = ".billyharness"
		}
	}
	return home + "/telegram/state.json"
}

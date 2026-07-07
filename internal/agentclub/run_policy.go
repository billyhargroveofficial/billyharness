package agentclub

import (
	"fmt"
	"strings"
	"time"
)

const (
	RunPolicyModeStartIfIdle = "start_if_idle"
	RunPolicyModeInterrupt   = "interrupt"

	MaxRunPolicyRunsPerHour = 60
	MaxRunPolicyCooldown    = 24 * time.Hour
	MaxRunPolicyToolRounds  = 1000
)

type RunPolicyConfig struct {
	Enabled         bool   `json:"enabled"`
	Mode            string `json:"mode,omitempty"`
	InterruptPolicy string `json:"interrupt_policy,omitempty"`
	MaxRunsPerHour  int    `json:"max_runs_per_hour,omitempty"`
	Cooldown        string `json:"cooldown,omitempty"`
	MaxToolRounds   int    `json:"max_tool_rounds,omitempty"`
	AccessMode      string `json:"access_mode,omitempty"`
}

type RunPolicy struct {
	Enabled         bool
	Mode            string
	InterruptPolicy string
	MaxRunsPerHour  int
	Cooldown        time.Duration
	MaxToolRounds   int
	AccessMode      string
}

func NormalizeRunPolicyConfig(cfg RunPolicyConfig) (RunPolicy, error) {
	if !cfg.Enabled {
		return RunPolicy{}, nil
	}
	policy := RunPolicy{Enabled: true}
	policy.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	if policy.Mode == "" {
		policy.Mode = RunPolicyModeStartIfIdle
	}
	switch policy.Mode {
	case RunPolicyModeStartIfIdle:
	case RunPolicyModeInterrupt:
	default:
		return RunPolicy{}, fmt.Errorf("%w: unsupported run_policy mode %q", ErrInvalidConfig, cfg.Mode)
	}
	policy.InterruptPolicy = strings.ToLower(strings.TrimSpace(cfg.InterruptPolicy))
	switch policy.InterruptPolicy {
	case "":
	case "interrupt":
	default:
		return RunPolicy{}, fmt.Errorf("%w: unsupported run_policy interrupt_policy %q", ErrInvalidConfig, cfg.InterruptPolicy)
	}
	if policy.Mode == RunPolicyModeInterrupt && policy.InterruptPolicy == "" {
		policy.InterruptPolicy = "interrupt"
	}
	policy.MaxRunsPerHour = cfg.MaxRunsPerHour
	if policy.MaxRunsPerHour == 0 {
		policy.MaxRunsPerHour = 1
	}
	if policy.MaxRunsPerHour < 0 || policy.MaxRunsPerHour > MaxRunPolicyRunsPerHour {
		return RunPolicy{}, fmt.Errorf("%w: run_policy max_runs_per_hour must be between 1 and %d", ErrInvalidConfig, MaxRunPolicyRunsPerHour)
	}
	if strings.TrimSpace(cfg.Cooldown) != "" {
		cooldown, err := time.ParseDuration(strings.TrimSpace(cfg.Cooldown))
		if err != nil || cooldown < 0 || cooldown > MaxRunPolicyCooldown {
			return RunPolicy{}, fmt.Errorf("%w: run_policy cooldown must be a duration between 0 and %s", ErrInvalidConfig, MaxRunPolicyCooldown)
		}
		policy.Cooldown = cooldown
	}
	policy.MaxToolRounds = cfg.MaxToolRounds
	if policy.MaxToolRounds < 0 || policy.MaxToolRounds > MaxRunPolicyToolRounds {
		return RunPolicy{}, fmt.Errorf("%w: run_policy max_tool_rounds must be between 0 and %d", ErrInvalidConfig, MaxRunPolicyToolRounds)
	}
	accessMode, ok := normalizeRunPolicyAccessMode(cfg.AccessMode)
	if !ok {
		return RunPolicy{}, fmt.Errorf("%w: unsupported run_policy access_mode %q", ErrInvalidConfig, cfg.AccessMode)
	}
	policy.AccessMode = accessMode
	return policy, nil
}

func normalizeTriggerRunPolicyConfig(triggerID string, cfg *RunPolicyConfig) (*RunPolicyConfig, error) {
	if cfg == nil {
		return nil, nil
	}
	policy, err := NormalizeRunPolicyConfig(*cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: trigger %s: %v", ErrInvalidConfig, triggerID, err)
	}
	if !policy.Enabled {
		return nil, nil
	}
	return &RunPolicyConfig{
		Enabled:         true,
		Mode:            policy.Mode,
		InterruptPolicy: policy.InterruptPolicy,
		MaxRunsPerHour:  policy.MaxRunsPerHour,
		Cooldown:        durationString(policy.Cooldown),
		MaxToolRounds:   policy.MaxToolRounds,
		AccessMode:      policy.AccessMode,
	}, nil
}

func cloneRunPolicyConfig(in *RunPolicyConfig) *RunPolicyConfig {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func normalizeRunPolicyAccessMode(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "", true
	case "build":
		return "build", true
	case "guarded", "safe":
		return "guarded", true
	case "plan", "readonly", "read-only", "read_only", "analysis":
		return "plan", true
	default:
		return "", false
	}
}

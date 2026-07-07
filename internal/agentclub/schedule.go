package agentclub

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ScheduleKindInterval = "interval"

	DefaultScheduleMaxCatchup = 1
	MaxScheduleCatchup        = 100
	MaxScheduleJitter         = time.Hour

	SchedulerStateSchemaVersion = 1
)

type ScheduleConfig struct {
	Kind       string `json:"kind"`
	Every      string `json:"every"`
	Jitter     string `json:"jitter,omitempty"`
	StartAtUTC string `json:"start_at_utc"`
	MaxCatchup int    `json:"max_catchup,omitempty"`
}

type ScheduleSpec struct {
	Kind       string
	Every      time.Duration
	Jitter     time.Duration
	StartAtUTC time.Time
	MaxCatchup int
}

type SchedulerState struct {
	SchemaVersion int                              `json:"schema_version"`
	UpdatedAtUTC  string                           `json:"updated_at_utc,omitempty"`
	RunCount      int64                            `json:"run_count,omitempty"`
	DeliveryCount int64                            `json:"delivery_count,omitempty"`
	ErrorCount    int64                            `json:"error_count,omitempty"`
	Triggers      map[string]SchedulerTriggerState `json:"triggers,omitempty"`
}

type SchedulerTriggerState struct {
	LastScheduledAtUTC string `json:"last_scheduled_at_utc,omitempty"`
	LastSuccessAtUTC   string `json:"last_success_at_utc,omitempty"`
	LastErrorAtUTC     string `json:"last_error_at_utc,omitempty"`
	LastError          string `json:"last_error,omitempty"`
	DeliveryCount      int64  `json:"delivery_count,omitempty"`
	ErrorCount         int64  `json:"error_count,omitempty"`
}

func NormalizeScheduleConfig(cfg ScheduleConfig) (ScheduleSpec, error) {
	spec := ScheduleSpec{Kind: strings.ToLower(strings.TrimSpace(cfg.Kind)), MaxCatchup: cfg.MaxCatchup}
	if spec.Kind == "" {
		spec.Kind = ScheduleKindInterval
	}
	if spec.Kind != ScheduleKindInterval {
		return ScheduleSpec{}, fmt.Errorf("%w: unsupported schedule kind %q", ErrInvalidConfig, cfg.Kind)
	}
	every, err := time.ParseDuration(strings.TrimSpace(cfg.Every))
	if err != nil || every <= 0 {
		return ScheduleSpec{}, fmt.Errorf("%w: schedule every must be a positive duration", ErrInvalidConfig)
	}
	spec.Every = every
	if strings.TrimSpace(cfg.Jitter) != "" {
		jitter, err := time.ParseDuration(strings.TrimSpace(cfg.Jitter))
		if err != nil || jitter < 0 {
			return ScheduleSpec{}, fmt.Errorf("%w: schedule jitter must be a non-negative duration", ErrInvalidConfig)
		}
		if jitter > MaxScheduleJitter || jitter > every/2 {
			return ScheduleSpec{}, fmt.Errorf("%w: schedule jitter exceeds bounds", ErrInvalidConfig)
		}
		spec.Jitter = jitter
	}
	if spec.MaxCatchup == 0 {
		spec.MaxCatchup = DefaultScheduleMaxCatchup
	}
	if spec.MaxCatchup < 0 || spec.MaxCatchup > MaxScheduleCatchup {
		return ScheduleSpec{}, fmt.Errorf("%w: schedule max_catchup must be between 0 and %d", ErrInvalidConfig, MaxScheduleCatchup)
	}
	startAt := strings.TrimSpace(cfg.StartAtUTC)
	if startAt == "" {
		return ScheduleSpec{}, fmt.Errorf("%w: schedule start_at_utc required", ErrInvalidConfig)
	}
	parsed, err := time.Parse(time.RFC3339Nano, startAt)
	if err != nil {
		return ScheduleSpec{}, fmt.Errorf("%w: schedule start_at_utc must be RFC3339", ErrInvalidConfig)
	}
	spec.StartAtUTC = parsed.UTC()
	return spec, nil
}

func DueScheduleTicks(triggerID string, cfg ScheduleConfig, lastScheduledAt time.Time, now time.Time) ([]time.Time, error) {
	spec, err := NormalizeScheduleConfig(cfg)
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	first := spec.StartAtUTC.Add(deterministicJitterOffset(triggerID, spec.Jitter))
	if now.Before(first) {
		return nil, nil
	}
	next := first
	if !lastScheduledAt.IsZero() {
		last := lastScheduledAt.UTC()
		if last.After(next) || last.Equal(next) {
			steps := last.Sub(first)/spec.Every + 1
			next = first.Add(steps * spec.Every)
		}
	}
	if next.Before(first) {
		next = first
	}
	if next.After(now) {
		return nil, nil
	}
	max := spec.MaxCatchup
	if max <= 0 {
		max = DefaultScheduleMaxCatchup
	}
	out := make([]time.Time, 0, max)
	for tick := next; !tick.After(now) && len(out) < max; tick = tick.Add(spec.Every) {
		out = append(out, tick.UTC())
	}
	return out, nil
}

func EnabledScheduleTriggers(cfg FileConfig) []TriggerBindingConfig {
	out := make([]TriggerBindingConfig, 0, len(cfg.Triggers))
	for _, trigger := range cfg.Triggers {
		if !trigger.Enabled || strings.ToLower(strings.TrimSpace(trigger.Kind)) != TriggerKindSchedule {
			continue
		}
		out = append(out, cloneTriggerBindingConfig(trigger))
	}
	return out
}

func LoadSchedulerState(path string) (SchedulerState, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return NewSchedulerState(), nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewSchedulerState(), nil
		}
		return SchedulerState{}, err
	}
	var state SchedulerState
	if err := json.Unmarshal(body, &state); err != nil {
		return SchedulerState{}, fmt.Errorf("load agentclub scheduler state %s: %w", path, err)
	}
	if state.SchemaVersion != SchedulerStateSchemaVersion {
		return SchedulerState{}, fmt.Errorf("load agentclub scheduler state %s: unsupported schema_version %d", path, state.SchemaVersion)
	}
	if state.Triggers == nil {
		state.Triggers = map[string]SchedulerTriggerState{}
	}
	return state, nil
}

func SaveSchedulerState(path string, state SchedulerState) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("%w: scheduler state path required", ErrInvalidConfig)
	}
	if state.SchemaVersion == 0 {
		state.SchemaVersion = SchedulerStateSchemaVersion
	}
	if state.Triggers == nil {
		state.Triggers = map[string]SchedulerTriggerState{}
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agentclub-scheduler-state-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, writeErr := tmp.Write(body)
	closeErr := tmp.Close()
	if writeErr != nil {
		_ = os.Remove(tmpName)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpName)
		return closeErr
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func NewSchedulerState() SchedulerState {
	return SchedulerState{
		SchemaVersion: SchedulerStateSchemaVersion,
		Triggers:      map[string]SchedulerTriggerState{},
	}
}

func RecordSchedulerSuccess(state *SchedulerState, triggerID string, scheduledAt time.Time, now time.Time) {
	if state.SchemaVersion == 0 {
		state.SchemaVersion = SchedulerStateSchemaVersion
	}
	if state.Triggers == nil {
		state.Triggers = map[string]SchedulerTriggerState{}
	}
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	item := state.Triggers[triggerID]
	item.LastScheduledAtUTC = scheduledAt.UTC().Format(time.RFC3339Nano)
	item.LastSuccessAtUTC = now.Format(time.RFC3339Nano)
	item.LastError = ""
	item.DeliveryCount++
	state.Triggers[triggerID] = item
	state.DeliveryCount++
	state.UpdatedAtUTC = now.Format(time.RFC3339Nano)
}

func RecordSchedulerError(state *SchedulerState, triggerID string, err error, now time.Time) {
	if state.SchemaVersion == 0 {
		state.SchemaVersion = SchedulerStateSchemaVersion
	}
	if state.Triggers == nil {
		state.Triggers = map[string]SchedulerTriggerState{}
	}
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	item := state.Triggers[triggerID]
	item.LastErrorAtUTC = now.Format(time.RFC3339Nano)
	item.LastError = strings.TrimSpace(err.Error())
	item.ErrorCount++
	state.Triggers[triggerID] = item
	state.ErrorCount++
	state.UpdatedAtUTC = now.Format(time.RFC3339Nano)
}

func SchedulerLastScheduledAt(state SchedulerState, triggerID string) time.Time {
	item := state.Triggers[triggerID]
	if strings.TrimSpace(item.LastScheduledAtUTC) == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, item.LastScheduledAtUTC)
	if err != nil {
		return time.Time{}
	}
	return ts.UTC()
}

func normalizeTriggerScheduleConfig(triggerID, kind string, enabled bool, cfg *ScheduleConfig) (*ScheduleConfig, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != TriggerKindSchedule {
		if cfg != nil {
			return nil, fmt.Errorf("%w: trigger %s schedule is only valid for kind=schedule", ErrInvalidConfig, triggerID)
		}
		return nil, nil
	}
	if cfg == nil {
		if enabled {
			return nil, fmt.Errorf("%w: trigger %s schedule required for enabled kind=schedule", ErrInvalidConfig, triggerID)
		}
		return nil, nil
	}
	spec, err := NormalizeScheduleConfig(*cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: trigger %s: %v", ErrInvalidConfig, triggerID, err)
	}
	return &ScheduleConfig{
		Kind:       spec.Kind,
		Every:      spec.Every.String(),
		Jitter:     durationString(spec.Jitter),
		StartAtUTC: spec.StartAtUTC.Format(time.RFC3339Nano),
		MaxCatchup: spec.MaxCatchup,
	}, nil
}

func cloneScheduleConfig(in *ScheduleConfig) *ScheduleConfig {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneTriggerBindingConfig(in TriggerBindingConfig) TriggerBindingConfig {
	in.Metadata = copyStringMap(in.Metadata)
	in.Schedule = cloneScheduleConfig(in.Schedule)
	return in
}

func deterministicJitterOffset(triggerID string, jitter time.Duration) time.Duration {
	if jitter <= 0 {
		return 0
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(triggerID)))
	n := binary.BigEndian.Uint64(sum[:8])
	return time.Duration(n % uint64(jitter+1))
}

func durationString(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

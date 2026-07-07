package agentclub

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestDueScheduleTicksFirstRunAndCatchup(t *testing.T) {
	cfg := ScheduleConfig{
		Kind:       ScheduleKindInterval,
		Every:      "30m",
		StartAtUTC: "2026-07-07T00:00:00Z",
		MaxCatchup: 2,
	}
	now := time.Date(2026, 7, 7, 1, 10, 0, 0, time.UTC)
	ticks, err := DueScheduleTicks("fixture.schedule", cfg, time.Time{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 2 || ticks[0].Format(time.RFC3339) != "2026-07-07T00:00:00Z" || ticks[1].Format(time.RFC3339) != "2026-07-07T00:30:00Z" {
		t.Fatalf("first ticks = %#v", ticks)
	}

	last := time.Date(2026, 7, 7, 0, 30, 0, 0, time.UTC)
	catchupNow := time.Date(2026, 7, 7, 1, 40, 0, 0, time.UTC)
	ticks, err = DueScheduleTicks("fixture.schedule", cfg, last, catchupNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 2 || ticks[0].Format(time.RFC3339) != "2026-07-07T01:00:00Z" || ticks[1].Format(time.RFC3339) != "2026-07-07T01:30:00Z" {
		t.Fatalf("catchup ticks = %#v", ticks)
	}
}

func TestDueScheduleTicksNoDueAndDeterministicJitter(t *testing.T) {
	cfg := ScheduleConfig{
		Kind:       ScheduleKindInterval,
		Every:      "30m",
		Jitter:     "10s",
		StartAtUTC: "2026-07-07T00:00:00Z",
		MaxCatchup: 1,
	}
	before := time.Date(2026, 7, 6, 23, 59, 59, 0, time.UTC)
	ticks, err := DueScheduleTicks("fixture.schedule", cfg, time.Time{}, before)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 0 {
		t.Fatalf("ticks before start = %#v", ticks)
	}
	now := time.Date(2026, 7, 7, 0, 30, 30, 0, time.UTC)
	a, err := DueScheduleTicks("fixture.schedule", cfg, time.Time{}, now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DueScheduleTicks("fixture.schedule", cfg, time.Time{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || len(b) != 1 || !a[0].Equal(b[0]) {
		t.Fatalf("jitter ticks not deterministic: %#v %#v", a, b)
	}
	base := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	if a[0].Before(base) || a[0].After(base.Add(10*time.Second)) {
		t.Fatalf("jitter tick outside bounds: %s", a[0])
	}
}

func TestScheduleValidationRejectsBadSpecs(t *testing.T) {
	cases := []ScheduleConfig{
		{Kind: "cron", Every: "1m", StartAtUTC: "2026-07-07T00:00:00Z"},
		{Kind: ScheduleKindInterval, Every: "0s", StartAtUTC: "2026-07-07T00:00:00Z"},
		{Kind: ScheduleKindInterval, Every: "1m", Jitter: "40s", StartAtUTC: "2026-07-07T00:00:00Z"},
		{Kind: ScheduleKindInterval, Every: "1m", StartAtUTC: "bad"},
		{Kind: ScheduleKindInterval, Every: "1m", StartAtUTC: "2026-07-07T00:00:00Z", MaxCatchup: MaxScheduleCatchup + 1},
	}
	for _, tc := range cases {
		if _, err := NormalizeScheduleConfig(tc); err == nil {
			t.Fatalf("expected invalid schedule for %#v", tc)
		}
	}
}

func TestSchedulerStateRoundTripPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "agentclub-scheduler-state.json")
	state := NewSchedulerState()
	RecordSchedulerSuccess(&state, "fixture.schedule", time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC), time.Date(2026, 7, 7, 12, 0, 1, 0, time.UTC))
	RecordSchedulerError(&state, "fixture.schedule", errFixtureScheduler, time.Date(2026, 7, 7, 12, 1, 0, 0, time.UTC))
	if err := SaveSchedulerState(path, state); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("state permissions = %v", info.Mode().Perm())
	}
	loaded, err := LoadSchedulerState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeliveryCount != 1 || loaded.ErrorCount != 1 || SchedulerLastScheduledAt(loaded, "fixture.schedule").IsZero() {
		t.Fatalf("loaded state = %#v", loaded)
	}
}

func TestEnabledScheduleTriggersFiltersDisabledAndNonSchedule(t *testing.T) {
	cfg := FileConfig{Triggers: []TriggerBindingConfig{
		{ID: "manual", Kind: TriggerKindManual, Enabled: true},
		{ID: "disabled", Kind: TriggerKindSchedule, Enabled: false},
		{ID: "enabled", Kind: TriggerKindSchedule, Enabled: true, Schedule: &ScheduleConfig{Kind: ScheduleKindInterval, Every: "1m", StartAtUTC: "2026-07-07T00:00:00Z"}},
	}}
	got := EnabledScheduleTriggers(cfg)
	if len(got) != 1 || got[0].ID != "enabled" {
		t.Fatalf("enabled schedules = %#v", got)
	}
	got[0].Schedule.Every = "changed"
	if cfg.Triggers[2].Schedule.Every == "changed" {
		t.Fatal("schedule config was not cloned")
	}
}

type fixtureSchedulerError string

func (e fixtureSchedulerError) Error() string { return string(e) }

var errFixtureScheduler = fixtureSchedulerError("gateway HTTP 503")

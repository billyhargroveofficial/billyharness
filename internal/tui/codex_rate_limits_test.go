package tui

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseCodexRateLimitsResultPrefersCodexBucket(t *testing.T) {
	snapshot, err := parseCodexRateLimitsResult(json.RawMessage(`{
		"rateLimits": {
			"limitId": "legacy",
			"primary": { "usedPercent": 1, "windowDurationMins": 15, "resetsAt": 1779450000 },
			"secondary": null,
			"rateLimitReachedType": null
		},
		"rateLimitsByLimitId": {
			"codex": {
				"limitId": "codex",
				"primary": { "usedPercent": 25, "windowDurationMins": 300, "resetsAt": 1779459394 },
				"secondary": { "usedPercent": 18, "windowDurationMins": 10080, "resetsAt": 1779826837 },
				"credits": { "hasCredits": true, "unlimited": false, "balance": "766.76" },
				"planType": "prolite",
				"rateLimitReachedType": null
			}
		},
		"rateLimitResetCredits": { "availableCount": 2 }
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Primary == nil || snapshot.Primary.UsedPercent != 25 || snapshot.Primary.WindowDurationMins != 300 {
		t.Fatalf("primary = %#v", snapshot.Primary)
	}
	if got := snapshot.Primary.ResetsAt; !got.Equal(time.Unix(1779459394, 0)) {
		t.Fatalf("primary reset = %s", got)
	}
	if snapshot.Secondary == nil || snapshot.Secondary.UsedPercent != 18 || snapshot.Secondary.WindowDurationMins != 10080 {
		t.Fatalf("secondary = %#v", snapshot.Secondary)
	}
	if snapshot.PlanType != "prolite" {
		t.Fatalf("plan type = %q", snapshot.PlanType)
	}
	if snapshot.Credits == nil || !snapshot.Credits.HasCredits || snapshot.Credits.Balance != "766.76" {
		t.Fatalf("credits = %#v", snapshot.Credits)
	}
	if snapshot.ResetCredits == nil || snapshot.ResetCredits.AvailableCount != 2 {
		t.Fatalf("reset credits = %#v", snapshot.ResetCredits)
	}
}

package telegrambot

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
	"github.com/billyhargroveofficial/billyharness/internal/testkit"
)

func TestCanonicalTelegramInterruptionFixtureRendersProgressAndFailure(t *testing.T) {
	fixture := canonicalTelegramFixture(t, "telegram_interruption")
	r := NewRenderer()
	var progress []RenderEvent
	for i, body := range fixture.Events {
		var event protocol.Event
		if err := json.Unmarshal(body, &event); err != nil {
			t.Fatalf("decode fixture event %d: %v", i, err)
		}
		progress = append(progress, r.Apply(event)...)
	}
	progressText := renderEventsText(progress)
	for _, want := range []string{"Question rejected", "new Telegram message interrupted", "interrupted by Telegram user"} {
		if !strings.Contains(progressText, want) {
			t.Fatalf("progress missing %q in:\n%s", want, progressText)
		}
	}
	finalText := strings.Join(r.FinalChunks("mock", ""), "\n")
	if !strings.Contains(finalText, "Error: telegram interruption") {
		t.Fatalf("final chunks missing interruption error:\n%s", finalText)
	}
}

func canonicalTelegramFixture(t *testing.T, name string) testkit.GoldenEdgeCaseFixture {
	t.Helper()
	for _, fixture := range testkit.ReadCanonicalEdgeCaseCatalog(t).Fixtures {
		if fixture.Name == name {
			return fixture
		}
	}
	t.Fatalf("missing fixture %q", name)
	return testkit.GoldenEdgeCaseFixture{}
}

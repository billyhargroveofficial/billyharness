package displayfmt

import "testing"

func TestCompactContext(t *testing.T) {
	tests := map[int64]string{
		-12_345:    "-12.3k",
		999:        "999",
		1_000:      "1.0k",
		12_345:     "12.3k",
		1_234_567:  "1.23M",
		10_000_000: "10.00M",
	}
	for value, want := range tests {
		if got := CompactContext(value); got != want {
			t.Fatalf("CompactContext(%d) = %q, want %q", value, got, want)
		}
	}
}

func TestCompactTerminal(t *testing.T) {
	tests := map[int64]string{
		999:       "999",
		1_000:     "1.0k",
		9_999:     "10.0k",
		10_000:    "10k",
		123_456:   "123k",
		1_234_567: "1.2m",
	}
	for value, want := range tests {
		if got := CompactTerminal(value); got != want {
			t.Fatalf("CompactTerminal(%d) = %q, want %q", value, got, want)
		}
	}
}

func TestCompactTool(t *testing.T) {
	tests := map[int64]string{
		999:       "999",
		1_000:     "1k",
		1_250:     "1.2k",
		1_000_000: "1M",
		1_250_000: "1.2M",
	}
	for value, want := range tests {
		if got := CompactTool(value); got != want {
			t.Fatalf("CompactTool(%d) = %q, want %q", value, got, want)
		}
	}
}

func TestContextPercent(t *testing.T) {
	tests := []struct {
		name   string
		used   int64
		window int64
		want   string
	}{
		{name: "unknown window", used: 123, window: 0, want: "0%"},
		{name: "small", used: 12, window: 1_000, want: "1.2%"},
		{name: "large", used: 150, window: 1_000, want: "15%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContextPercent(tt.used, tt.window); got != tt.want {
				t.Fatalf("ContextPercent(%d, %d) = %q, want %q", tt.used, tt.window, got, tt.want)
			}
		})
	}
}

func TestFixedPercentValue(t *testing.T) {
	if got := FixedPercentValue(12.345, 1); got != "12.3%" {
		t.Fatalf("FixedPercentValue(12.345, 1) = %q, want %q", got, "12.3%")
	}
	if got := FixedPercentValue(12.345, -1); got != "12%" {
		t.Fatalf("FixedPercentValue(12.345, -1) = %q, want %q", got, "12%")
	}
}

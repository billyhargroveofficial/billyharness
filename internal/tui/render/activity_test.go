package render

import (
	"strings"
	"testing"
)

func TestRenderActivityBlockNormalizesToolTitleAndBody(t *testing.T) {
	rendered := RenderActivityBlock(ActivityCell{
		Kind:  "tool",
		Title: "TOOL",
		Body:  "\nfirst\nsecond\n",
	}, 40, ActivityStyles{})

	for _, want := range []string{"• Called tool", "└ first", "│ second"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered activity missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "┌") || strings.Contains(rendered, "└─") {
		t.Fatalf("activity block should not render heavy box borders: %q", rendered)
	}
}

func TestRenderActivityBlockNormalizesStatusAndAuditTitles(t *testing.T) {
	tests := []struct {
		name string
		cell ActivityCell
		want string
	}{
		{
			name: "reasoning",
			cell: ActivityCell{Kind: "reasoning", Title: "THINKING"},
			want: "• Thinking",
		},
		{
			name: "status",
			cell: ActivityCell{Kind: "status", Title: "context compacted"},
			want: "• Context compacted",
		},
		{
			name: "audit",
			cell: ActivityCell{Kind: "audit", Title: "AUDIT"},
			want: "• Tool audit",
		},
		{
			name: "error",
			cell: ActivityCell{Kind: "error", Title: "ERROR"},
			want: "• Error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RenderActivityBlock(tt.cell, 40, ActivityStyles{}); got != tt.want {
				t.Fatalf("rendered title = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderActivityBodyWrapsByDisplayWidth(t *testing.T) {
	rendered := RenderActivityBody("abcdefghijklmnop", 12, ActivityStyles{})
	lines := strings.Split(rendered, "\n")
	if len(lines) != 2 {
		t.Fatalf("wrapped lines = %#v, want 2 lines", lines)
	}
	if !strings.HasPrefix(lines[0], "  └ ") || !strings.HasPrefix(lines[1], "  │ ") {
		t.Fatalf("wrapped lines should use activity guides: %#v", lines)
	}
	if strings.Contains(rendered, "abcdefghijklmnop") {
		t.Fatalf("body should be wrapped: %q", rendered)
	}
}

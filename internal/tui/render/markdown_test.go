package render

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestRenderTerminalMarkdownStripsSyntax(t *testing.T) {
	rendered := stripANSITest(RenderTerminalMarkdown(strings.Join([]string{
		"# Summary",
		"",
		"- **fast** path with `code`",
		"1. [docs](https://example.com)",
		"> quoted",
		"---",
		"| Name | Score |",
		"| --- | ---: |",
		"| **Billy** | `10` |",
		"```go",
		"fmt.Println(1)",
		"```",
	}, "\n"), 100, testMarkdownStyles()))

	for _, want := range []string{"Summary", "•", "fast", "code", "docs", "https://example.com", "│ quoted", "────", "┌", "Name", "Billy", "10", "fmt.Println(1)"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered markdown missing %q: %q", want, rendered)
		}
	}
	for _, leak := range []string{"```", "**", "`10`"} {
		if strings.Contains(rendered, leak) {
			t.Fatalf("markdown syntax %q leaked: %q", leak, rendered)
		}
	}
}

func TestRenderAssistantMarkdownKeepsUnstableTailRaw(t *testing.T) {
	live := stripANSITest(RenderAssistantMarkdown(strings.Join([]string{
		"## Weather",
		"",
		"| Name | Score |",
		"| --- | ---: |",
		"| **Billy** | `10` |",
	}, "\n"), 100, testMarkdownStyles(), true))
	if !strings.Contains(live, "| **Billy** | `10` |") {
		t.Fatalf("live markdown tail should stay raw, got: %q", live)
	}

	final := stripANSITest(RenderAssistantMarkdown(strings.Join([]string{
		"## Weather",
		"",
		"| Name | Score |",
		"| --- | ---: |",
		"| **Billy** | `10` |",
	}, "\n"), 100, testMarkdownStyles(), false))
	if !strings.Contains(final, "┌") || !strings.Contains(final, "Billy") || !strings.Contains(final, "10") {
		t.Fatalf("final markdown table did not render: %q", final)
	}
	for _, leak := range []string{"**", "`10`"} {
		if strings.Contains(final, leak) {
			t.Fatalf("final markdown syntax %q leaked: %q", leak, final)
		}
	}
}

func TestStreamingMarkdownStateSplitsRawStableTailAndHoldback(t *testing.T) {
	code := "Intro\n```go\nfmt.Println(1)\n"
	codeState := newStreamingMarkdownState(code, true)
	if codeState.rawMarkdown != code || codeState.stableCommitted != "Intro\n" ||
		codeState.mutableLiveTail != "```go\nfmt.Println(1)\n" ||
		codeState.holdbackKind != markdownHoldbackCodeFence ||
		codeState.finalCanonical {
		t.Fatalf("code state = %#v", codeState)
	}

	table := "Scores\n| Name | Value |\n| --- | --- |\n| Billy | 10 |\n"
	tableState := newStreamingMarkdownState(table, true)
	if tableState.rawMarkdown != table || tableState.stableCommitted != "Scores\n" ||
		!strings.Contains(tableState.mutableLiveTail, "| Billy | 10 |") ||
		tableState.holdbackKind != markdownHoldbackTable ||
		tableState.finalCanonical {
		t.Fatalf("table state = %#v", tableState)
	}

	final := newStreamingMarkdownState(table, false)
	if !final.finalCanonical || final.stableCommitted != table || final.mutableLiveTail != "" || final.holdbackKind != "" {
		t.Fatalf("final state = %#v", final)
	}
}

func TestLastFencedCodeBlock(t *testing.T) {
	text, ok := LastFencedCodeBlock("before\n```go\nfmt.Println(1)\n```\n\n~~~txt\nsecond\n~~~\n")
	if !ok {
		t.Fatal("expected fenced code block")
	}
	if text != "second" {
		t.Fatalf("last fenced code block = %q", text)
	}
}

func TestMarkdownTableRowKeepsEscapedAndCodePipesInCells(t *testing.T) {
	cells, ok := parseMarkdownTableRow("| `a\\|b` | escaped \\| literal | plain |")
	if !ok {
		t.Fatal("expected markdown table row")
	}
	want := []string{"`a|b`", "escaped | literal", "plain"}
	if len(cells) != len(want) {
		t.Fatalf("cells = %#v, want %#v", cells, want)
	}
	for i := range want {
		if cells[i] != want[i] {
			t.Fatalf("cells = %#v, want %#v", cells, want)
		}
	}
}

func TestRenderTerminalMarkdownGoldenTableAtCommonWidths(t *testing.T) {
	input := strings.Join([]string{
		"See [docs](https://e.test) and `code`.",
		"",
		"| Name | Score |",
		"| --- | ---: |",
		"| Billy | `10` |",
		"| Zoë 中 | [docs](https://e.test) |",
	}, "\n")
	want := strings.Join([]string{
		"See docs (https://e.test) and code.",
		"",
		"┌────────┬───────────────────────┐",
		"│ Name   │ Score                 │",
		"├────────┼───────────────────────┤",
		"│ Billy  │ 10                    │",
		"│ Zoë 中 │ docs (https://e.test) │",
		"└────────┴───────────────────────┘",
	}, "\n")

	for _, width := range []int{40, 80, 120} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			got := stripANSITest(RenderTerminalMarkdown(input, width, testMarkdownStyles()))
			if got != want {
				t.Fatalf("width %d rendered:\n%s\n\nwant:\n%s", width, got, want)
			}
		})
	}
}

func TestRenderTerminalMarkdownLongTableFitsGoldenWidths(t *testing.T) {
	input := strings.Join([]string{
		"| Path | Description |",
		"| --- | --- |",
		"| internal/tui/render/markdown.go | **long** markdown cell with `code` and wide 中 grapheme content |",
	}, "\n")

	for _, width := range []int{40, 80, 120} {
		rendered := stripANSITest(RenderTerminalMarkdown(input, width, testMarkdownStyles()))
		for _, line := range strings.Split(rendered, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d line width = %d > %d: %q\nfull:\n%s", width, got, width, line, rendered)
			}
		}
		for _, leak := range []string{"**", "`code`", "| --- |"} {
			if strings.Contains(rendered, leak) {
				t.Fatalf("width %d leaked markdown syntax %q: %q", width, leak, rendered)
			}
		}
		if width == 40 && !strings.Contains(rendered, "…") {
			t.Fatalf("width 40 should truncate the long table, got: %q", rendered)
		}
	}
}

func TestRenderAssistantMarkdownStreamingTableHoldbackUntilBoundary(t *testing.T) {
	openTable := strings.Join([]string{
		"Intro",
		"| A | B |",
		"| --- | --- |",
		"| one | two |",
	}, "\n")
	liveOpen := stripANSITest(RenderAssistantMarkdown(openTable, 80, testMarkdownStyles(), true))
	if !strings.Contains(liveOpen, "| one | two |") || strings.Contains(liveOpen, "┌") {
		t.Fatalf("open live table should remain raw, got: %q", liveOpen)
	}

	withBoundary := openTable + "\n\nDone"
	liveBoundary := stripANSITest(RenderAssistantMarkdown(withBoundary, 80, testMarkdownStyles(), true))
	if !strings.Contains(liveBoundary, "┌") || !strings.Contains(liveBoundary, "│ one │ two │") || !strings.Contains(liveBoundary, "Done") {
		t.Fatalf("bounded live table should render stable table and raw tail, got: %q", liveBoundary)
	}
	if strings.Contains(liveBoundary, "| one | two |") {
		t.Fatalf("bounded live table leaked raw table row: %q", liveBoundary)
	}
}

func testMarkdownStyles() TerminalMarkdownStyles {
	return TerminalMarkdownStyleSet(MarkdownTheme{
		AssistantForeground: "#111111",
		ToolForeground:      "#222222",
		ToolBackground:      "#eeeeee",
		ToolBorder:          "#333333",
		BlockBorder:         "#444444",
		MutedForeground:     "#777777",
	})
}

func stripANSITest(text string) string {
	return regexp.MustCompile(`\x1b\[[0-9;:]*[A-Za-z]`).ReplaceAllString(text, "")
}

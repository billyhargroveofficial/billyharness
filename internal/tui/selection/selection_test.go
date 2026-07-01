package selection

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"
)

func TestSelectedTextAndHighlightUseVisibleColumns(t *testing.T) {
	content := "\x1b[90mmeta\x1b[m привет 🏳️‍🌈 中 done"
	target := "привет 🏳️‍🌈 中"
	visible := xansi.Strip(content)
	startByte := strings.Index(visible, target)
	if startByte < 0 {
		t.Fatalf("test target missing from %q", visible)
	}
	startCol := xansi.StringWidth(visible[:startByte])
	endCol := startCol + xansi.StringWidth(target)
	start := Point{Row: 0, Col: startCol}
	end := Point{Row: 0, Col: endCol}

	if got := SelectedText(content, start, end); got != target {
		t.Fatalf("SelectedText = %q, want %q", got, target)
	}
	highlighted := HighlightedContent(content, start, end, lipgloss.NewStyle().Background(lipgloss.Color("#FFD166")))
	if xansi.Strip(highlighted) != visible {
		t.Fatalf("highlight changed visible content: %q", highlighted)
	}
	if !strings.Contains(highlighted, "48;2;255;209;102") {
		t.Fatalf("highlight style missing: %q", highlighted)
	}
}

func TestSelectedTextAndHighlightRespectLineFilter(t *testing.T) {
	content := strings.Join([]string{
		"alpha",
		"\x1b[90mstatus: hidden chrome\x1b[m",
		"привет 🏳️‍🌈 中 tail",
	}, "\n")
	start := Point{Row: 0, Col: 0}
	end := Point{Row: 2, Col: xansi.StringWidth("привет 🏳️‍🌈 中")}
	selectable := []bool{true, false, true}

	got := SelectedTextWithLineFilter(content, start, end, selectable)
	if got != "alpha\nпривет 🏳️‍🌈 中" {
		t.Fatalf("SelectedTextWithLineFilter = %q", got)
	}
	highlighted := HighlightedContentWithLineFilter(content, start, end, lipgloss.NewStyle().Background(lipgloss.Color("#FFD166")), selectable)
	if xansi.Strip(highlighted) != xansi.Strip(content) {
		t.Fatalf("highlight changed visible content: %q", highlighted)
	}
	lines := strings.Split(highlighted, "\n")
	if strings.Contains(lines[1], "48;2;255;209;102") {
		t.Fatalf("non-selectable status line was highlighted: %q", lines[1])
	}
	if !strings.Contains(lines[0], "48;2;255;209;102") || !strings.Contains(lines[2], "48;2;255;209;102") {
		t.Fatalf("selectable lines were not highlighted: %q", highlighted)
	}
}

func TestPointFromMouseClampedKeepsRightEdgeExclusive(t *testing.T) {
	point := PointFromMouseClamped(10, 2, 3, 1, 999, 0)
	if point.Row != 10 || point.Col != 5 {
		t.Fatalf("point = %#v, want row 10 col 5", point)
	}
}

func TestControllerMouseLifecycle(t *testing.T) {
	var controller Controller
	viewport := Viewport{YOffset: 10, XOffset: 2, Width: 3, Height: 2}
	if controller.Begin(viewport, 99, 0) {
		t.Fatalf("begin outside viewport returned true")
	}
	if !controller.Begin(viewport, 1, 0) {
		t.Fatalf("begin inside viewport returned false")
	}
	if !controller.Selecting || controller.Start != (Point{Row: 10, Col: 3}) || controller.End != controller.Start {
		t.Fatalf("after begin controller = %#v", controller)
	}
	if !controller.Drag(viewport, 99, 8) {
		t.Fatalf("drag while selecting returned false")
	}
	if controller.End != (Point{Row: 11, Col: 5}) {
		t.Fatalf("drag end = %#v, want row 11 col 5", controller.End)
	}
	if !controller.Release(viewport, -10, -5) {
		t.Fatalf("release while selecting returned false")
	}
	if controller.Selecting {
		t.Fatalf("controller should not still be selecting")
	}
	if controller.End != (Point{Row: 10, Col: 2}) {
		t.Fatalf("release end = %#v, want row 10 col 2", controller.End)
	}
}

func TestByteRangeMatchesSelectedText(t *testing.T) {
	content := "alpha\nbeta\ngamma"
	start := Point{Row: 0, Col: 1}
	end := Point{Row: 1, Col: 4}
	startByte, endByte := ByteRange(content, start, end)
	if startByte < 0 || endByte <= startByte {
		t.Fatalf("ByteRange = %d:%d", startByte, endByte)
	}
	if got := content[startByte:endByte]; got != "lpha\nbeta" {
		t.Fatalf("byte range text = %q", got)
	}
}

func TestCopyFallsBackToOSC52(t *testing.T) {
	var out bytes.Buffer
	result := Copy("hello", CopyOptions{
		ClipboardWriter: func(string) error { return errors.New("clipboard unavailable") },
		OSC52Writer:     &out,
	})
	if result.Err != "" || result.Method != "osc52" || result.Chars != len("hello") {
		t.Fatalf("copy result = %#v", result)
	}
	if got := out.String(); !strings.HasPrefix(got, "\x1b]52;c;") || !strings.HasSuffix(got, "\x07") {
		t.Fatalf("osc52 output = %q", got)
	}
}

func TestTrimUTF8BytesPreservesValidString(t *testing.T) {
	if got := TrimUTF8Bytes("éclair", 1); got != "" {
		t.Fatalf("trimmed partial rune = %q, want empty", got)
	}
	if got := TrimUTF8Bytes("éclair", 3); got != "éc" {
		t.Fatalf("trimmed text = %q, want éc", got)
	}
}

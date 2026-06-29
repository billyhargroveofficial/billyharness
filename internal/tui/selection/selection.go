package selection

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	xansi "github.com/charmbracelet/x/ansi"
)

const DefaultOSC52MaxBytes = 100 * 1024

type Point struct {
	Row int
	Col int
}

type Viewport struct {
	YOffset int
	XOffset int
	Width   int
	Height  int
}

type Controller struct {
	Selecting bool
	Start     Point
	End       Point
}

type LineRange struct {
	Row   int
	Left  int
	Right int
}

type CopyOptions struct {
	ClipboardWriter func(string) error
	OSC52Writer     io.Writer
	OSC52MaxBytes   int
}

type CopyResult struct {
	Chars  int
	Method string
	Err    string
}

func PointFromMouse(viewportYOffset, viewportXOffset, x, y int) Point {
	return Point{Row: viewportYOffset + y, Col: maxInt(0, viewportXOffset+x)}
}

func MouseInViewport(viewport Viewport, x, y int) bool {
	return x >= 0 && y >= 0 && y < viewport.Height && x < maxInt(1, viewport.Width)
}

func PointFromMouseClamped(viewportYOffset, viewportXOffset, viewportWidth, viewportHeight, x, y int) Point {
	if y < 0 {
		y = 0
	}
	if y >= viewportHeight {
		y = maxInt(0, viewportHeight-1)
	}
	if x < 0 {
		x = 0
	}
	if x >= viewportWidth {
		x = maxInt(0, viewportWidth)
	}
	return PointFromMouse(viewportYOffset, viewportXOffset, x, y)
}

func (c *Controller) Begin(viewport Viewport, x, y int) bool {
	if !MouseInViewport(viewport, x, y) {
		return false
	}
	point := PointFromMouse(viewport.YOffset, viewport.XOffset, x, y)
	c.Selecting = true
	c.Start = point
	c.End = point
	return true
}

func (c *Controller) Drag(viewport Viewport, x, y int) bool {
	if !c.Selecting {
		return false
	}
	c.End = PointFromMouseClamped(viewport.YOffset, viewport.XOffset, viewport.Width, viewport.Height, x, y)
	return true
}

func (c *Controller) Release(viewport Viewport, x, y int) bool {
	if !c.Selecting {
		return false
	}
	c.End = PointFromMouseClamped(viewport.YOffset, viewport.XOffset, viewport.Width, viewport.Height, x, y)
	c.Selecting = false
	return true
}

func (c Controller) HasSelection() bool {
	return HasSelection(c.Start, c.End)
}

func (c Controller) SelectedText(content string) string {
	return SelectedText(content, c.Start, c.End)
}

func (c Controller) HighlightedContent(content string, style lipgloss.Style) string {
	return HighlightedContent(content, c.Start, c.End, style)
}

func (c Controller) ByteRange(content string) (int, int) {
	return ByteRange(content, c.Start, c.End)
}

func HasSelection(start, end Point) bool {
	start, end = Ordered(start, end)
	return start.Row != end.Row || start.Col != end.Col
}

func SelectedText(content string, start, end Point) string {
	start, end = Ordered(start, end)
	if start.Row == end.Row && start.Col == end.Col {
		return ""
	}
	lines := strings.Split(xansi.Strip(content), "\n")
	if len(lines) == 0 {
		return ""
	}
	start.Row = maxInt(0, minInt(start.Row, len(lines)-1))
	end.Row = maxInt(0, minInt(end.Row, len(lines)-1))
	var selected []string
	for row := start.Row; row <= end.Row; row++ {
		line := lines[row]
		left := 0
		right := xansi.StringWidth(line)
		if row == start.Row {
			left = minInt(start.Col, right)
		}
		if row == end.Row {
			right = minInt(maxInt(end.Col, left), right)
		}
		selected = append(selected, strings.TrimRight(xansi.Cut(line, left, right), " "))
	}
	return strings.Trim(strings.Join(selected, "\n"), "\n")
}

func HighlightedContent(content string, start, end Point, style lipgloss.Style) string {
	rawLines := strings.Split(content, "\n")
	ranges := LineRanges(rawLines, start, end)
	if len(ranges) == 0 {
		return content
	}
	for _, rng := range ranges {
		rawLines[rng.Row] = lipgloss.StyleRanges(
			rawLines[rng.Row],
			lipgloss.NewRange(rng.Left, rng.Right, style),
		)
	}
	return strings.Join(rawLines, "\n")
}

func LineRanges(rawLines []string, start, end Point) []LineRange {
	start, end = Ordered(start, end)
	if (start.Row == end.Row && start.Col == end.Col) || len(rawLines) == 0 {
		return nil
	}
	start.Row = maxInt(0, minInt(start.Row, len(rawLines)-1))
	end.Row = maxInt(0, minInt(end.Row, len(rawLines)-1))
	ranges := make([]LineRange, 0, end.Row-start.Row+1)
	for row := start.Row; row <= end.Row; row++ {
		lineWidth := xansi.StringWidth(xansi.Strip(rawLines[row]))
		left := 0
		right := lineWidth
		if row == start.Row {
			left = minInt(maxInt(start.Col, 0), lineWidth)
		}
		if row == end.Row {
			right = minInt(maxInt(end.Col, left), lineWidth)
		}
		if right > left {
			ranges = append(ranges, LineRange{Row: row, Left: left, Right: right})
		}
	}
	return ranges
}

func ByteRange(content string, start, end Point) (int, int) {
	start, end = Ordered(start, end)
	if start.Row == end.Row && start.Col == end.Col {
		return -1, -1
	}
	lines := strings.Split(xansi.Strip(content), "\n")
	if len(lines) == 0 {
		return -1, -1
	}
	start.Row = maxInt(0, minInt(start.Row, len(lines)-1))
	end.Row = maxInt(0, minInt(end.Row, len(lines)-1))
	startByte := byteOffset(lines, start)
	endByte := byteOffset(lines, end)
	if endByte < startByte {
		return endByte, startByte
	}
	return startByte, endByte
}

func Ordered(a, b Point) (Point, Point) {
	if a.Row > b.Row || (a.Row == b.Row && a.Col > b.Col) {
		return b, a
	}
	return a, b
}

func Copy(text string, opts CopyOptions) CopyResult {
	clipboardWriter := opts.ClipboardWriter
	if clipboardWriter == nil {
		clipboardWriter = clipboard.WriteAll
	}
	osc52Writer := opts.OSC52Writer
	if osc52Writer == nil {
		osc52Writer = os.Stdout
	}
	maxBytes := opts.OSC52MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultOSC52MaxBytes
	}
	if err := clipboardWriter(text); err == nil {
		return CopyResult{Chars: len([]rune(text)), Method: "clipboard"}
	} else if oscText := TrimUTF8Bytes(text, maxBytes); oscText == "" {
		return CopyResult{Chars: 0, Err: err.Error() + "; osc52: selection too large or empty"}
	} else if oscErr := WriteOSC52(osc52Writer, oscText); oscErr != nil {
		return CopyResult{Chars: len([]rune(text)), Err: err.Error() + "; osc52: " + oscErr.Error()}
	} else if len(oscText) < len(text) {
		return CopyResult{Chars: len([]rune(oscText)), Method: "osc52 truncated"}
	}
	return CopyResult{Chars: len([]rune(text)), Method: "osc52"}
}

func TrimUTF8Bytes(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	text = text[:maxBytes]
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func WriteOSC52(w io.Writer, text string) error {
	payload := base64.StdEncoding.EncodeToString([]byte(text))
	_, err := fmt.Fprint(w, "\x1b]52;c;"+payload+"\x07")
	return err
}

func byteOffset(lines []string, point Point) int {
	offset := 0
	for row := 0; row < point.Row && row < len(lines); row++ {
		offset += len(lines[row])
		if row < len(lines)-1 {
			offset++
		}
	}
	if point.Row >= len(lines) {
		return offset
	}
	return offset + byteOffsetForDisplayCol(lines[point.Row], point.Col)
}

func byteOffsetForDisplayCol(line string, col int) int {
	if col <= 0 {
		return 0
	}
	line = xansi.Strip(line)
	width := 0
	offset := 0
	for offset < len(line) {
		cluster, w := xansi.FirstGraphemeCluster(line[offset:], xansi.GraphemeWidth)
		if cluster == "" {
			break
		}
		clusterWidth := maxInt(1, w)
		if width+clusterWidth > col {
			return offset
		}
		width += clusterWidth
		offset += len(cluster)
	}
	return len(line)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

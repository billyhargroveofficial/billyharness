package render

import (
	"crypto/sha1"
	"encoding/hex"
	"strconv"
	"strings"
)

type BlockCacheKeyInput struct {
	ID           string
	Kind         string
	CellType     string
	EventType    string
	Title        string
	Content      string
	RawCopy      string
	Live         bool
	TurnID       string
	StepID       string
	CallID       string
	AttemptID    string
	ParentStepID string
	Collapsed    bool
	CollapseSet  bool
}

type RichCacheKeyInput struct {
	BlockCacheKey  string
	Width          int
	Theme          string
	ThinkView      string
	ToolView       string
	BlockCollapsed bool
	ToolCollapsed  bool
}

func BlockCacheKey(input BlockCacheKeyInput) string {
	return shortHash(
		input.ID,
		input.Kind,
		input.CellType,
		input.EventType,
		input.Title,
		input.Content,
		input.RawCopy,
		strconv.FormatBool(input.Live),
		input.TurnID,
		input.StepID,
		input.CallID,
		input.AttemptID,
		input.ParentStepID,
		strconv.FormatBool(input.Collapsed),
		strconv.FormatBool(input.CollapseSet),
	)
}

func RichCacheKey(input RichCacheKeyInput) string {
	return strings.Join([]string{
		input.BlockCacheKey,
		strconv.Itoa(input.Width),
		input.Theme,
		input.ThinkView,
		input.ToolView,
		strconv.FormatBool(input.BlockCollapsed),
		strconv.FormatBool(input.ToolCollapsed),
	}, "\x00")
}

func shortHash(parts ...string) string {
	sum := sha1.Sum([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8])
}

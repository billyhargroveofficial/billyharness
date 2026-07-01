package toolrender

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

func DecodeTurnChange(data any) (protocol.TurnChangeEvent, bool) {
	switch change := data.(type) {
	case protocol.TurnChangeEvent:
		return change, strings.TrimSpace(change.ChangeID) != ""
	case *protocol.TurnChangeEvent:
		if change == nil {
			return protocol.TurnChangeEvent{}, false
		}
		return *change, strings.TrimSpace(change.ChangeID) != ""
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return protocol.TurnChangeEvent{}, false
	}
	var change protocol.TurnChangeEvent
	if err := json.Unmarshal(bytes, &change); err != nil {
		return protocol.TurnChangeEvent{}, false
	}
	return change, strings.TrimSpace(change.ChangeID) != ""
}

func TurnChangeSummary(change protocol.TurnChangeEvent) string {
	var parts []string
	if change.Status == "reverted" {
		parts = append(parts, "reverted")
	}
	if change.FileCount > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", change.FileCount, plural(change.FileCount, "file", "files")))
	} else if strings.TrimSpace(change.Summary) != "" {
		parts = append(parts, strings.TrimSpace(change.Summary))
	}
	if change.Additions > 0 || change.Deletions > 0 {
		parts = append(parts, fmt.Sprintf("+%d -%d", change.Additions, change.Deletions))
	}
	if counts := changeCountParts(change); counts != "" {
		parts = append(parts, counts)
	}
	if change.BinaryFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d binary", change.BinaryFiles))
	}
	if change.LargeFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d large", change.LargeFiles))
	}
	if strings.EqualFold(change.ToolName, "shell_exec") {
		parts = append(parts, "shell changes")
	}
	if change.PatchOutputRef != "" {
		parts = append(parts, "patch ref "+CompactText(filepathBase(change.PatchOutputRef), 72))
	}
	if !change.Reversible {
		parts = append(parts, "not reversible")
	}
	if len(parts) == 0 {
		return strings.TrimSpace(change.ChangeID)
	}
	return strings.Join(parts, " | ")
}

func TurnChangeDetails(change protocol.TurnChangeEvent) string {
	var lines []string
	summary := TurnChangeSummary(change)
	if summary != "" {
		lines = append(lines, "summary: "+summary)
	}
	if change.ChangeID != "" {
		lines = append(lines, "change: "+change.ChangeID)
	}
	if change.ToolName != "" {
		lines = append(lines, "tool: "+change.ToolName)
	}
	if change.TurnID != "" {
		lines = append(lines, "turn: "+change.TurnID)
	}
	if change.PatchOutputRef != "" {
		lines = append(lines, "patch_ref: "+change.PatchOutputRef)
	}
	if change.PatchOutputRefID != "" {
		lines = append(lines, "patch_ref_id: "+change.PatchOutputRefID)
	}
	if change.PatchOutputRefBytes > 0 {
		lines = append(lines, "patch_ref_bytes: "+CompactInt(change.PatchOutputRefBytes))
	}
	lines = append(lines, fmt.Sprintf("reversible: %t", change.Reversible))
	if len(change.Files) > 0 {
		lines = append(lines, "files:")
		for _, file := range change.Files {
			lines = append(lines, "  "+turnChangeFileLine(file))
		}
	}
	return strings.Join(lines, "\n")
}

func turnChangeFileLine(file protocol.TurnChangeFile) string {
	path := firstTurnChangeText(file.RelPath, file.Path)
	if path == "" {
		path = "-"
	}
	var parts []string
	if file.Change != "" {
		parts = append(parts, strings.ToUpper(file.Change[:1]))
	}
	parts = append(parts, path)
	if file.Additions > 0 || file.Deletions > 0 {
		parts = append(parts, fmt.Sprintf("+%d -%d", file.Additions, file.Deletions))
	}
	if file.Binary {
		parts = append(parts, "binary")
	}
	if file.Large {
		parts = append(parts, "large")
	}
	if !file.Reversible {
		parts = append(parts, "not reversible")
	}
	return strings.Join(parts, " ")
}

func changeCountParts(change protocol.TurnChangeEvent) string {
	var parts []string
	if change.Added > 0 {
		parts = append(parts, fmt.Sprintf("%d added", change.Added))
	}
	if change.Modified > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", change.Modified))
	}
	if change.Deleted > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", change.Deleted))
	}
	if change.Directories > 0 {
		parts = append(parts, fmt.Sprintf("%d dirs", change.Directories))
	}
	return strings.Join(parts, ", ")
}

func plural(n int, single, many string) string {
	if n == 1 {
		return single
	}
	return many
}

func firstTurnChangeText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

package transcript

import "strings"

type Index struct {
	toolByCallID   map[string]int
	stepByKey      map[string]int
	runSummaryCell int
}

func BuildIndex(cells []Cell) Index {
	idx := Index{
		toolByCallID:   map[string]int{},
		stepByKey:      map[string]int{},
		runSummaryCell: -1,
	}
	for i, cell := range cells {
		if cell.Kind == "tool" {
			if callID := strings.TrimSpace(cell.CallID); callID != "" {
				idx.toolByCallID[callID] = i
			}
		}
		if stepID := strings.TrimSpace(cell.StepID); stepID != "" {
			idx.stepByKey[stepIndexKey(stepID, cell.CellType)] = i
		}
		if cell.CellType == CellTypeRunSummary {
			idx.runSummaryCell = i
		}
	}
	return idx
}

func (idx Index) ToolCall(callID string) (int, bool) {
	i, ok := idx.toolByCallID[strings.TrimSpace(callID)]
	return i, ok
}

func (idx Index) Step(stepID string, cellType CellType) (int, bool) {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return 0, false
	}
	i, ok := idx.stepByKey[stepIndexKey(stepID, cellType)]
	return i, ok
}

func (idx Index) RunSummary() (int, bool) {
	if idx.runSummaryCell < 0 {
		return 0, false
	}
	return idx.runSummaryCell, true
}

func stepIndexKey(stepID string, cellType CellType) string {
	return strings.TrimSpace(stepID) + "\x00" + string(cellType)
}

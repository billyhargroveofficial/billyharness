package transcript

import "testing"

func TestBuildIndexFindsToolStepAndRunSummaryCells(t *testing.T) {
	idx := BuildIndex([]Cell{
		{Kind: "tool", CellType: CellTypeToolCall, CallID: "call-1", StepID: "step-call"},
		{Kind: "status", CellType: CellTypeRunSummary},
		{Kind: "tool", CellType: CellTypeToolBatch, StepID: "step-batch"},
		{Kind: "tool", CellType: CellTypeToolCall, CallID: "call-2"},
		{Kind: "status", CellType: CellTypeRunSummary},
	})

	if got, ok := idx.ToolCall("call-2"); !ok || got != 3 {
		t.Fatalf("ToolCall(call-2) = %d, %t; want 3, true", got, ok)
	}
	if got, ok := idx.Step("step-batch", CellTypeToolBatch); !ok || got != 2 {
		t.Fatalf("Step(step-batch) = %d, %t; want 2, true", got, ok)
	}
	if _, ok := idx.Step("step-batch", CellTypeToolCall); ok {
		t.Fatal("Step should include cell type in the lookup key")
	}
	if got, ok := idx.RunSummary(); !ok || got != 4 {
		t.Fatalf("RunSummary() = %d, %t; want latest index 4, true", got, ok)
	}
}

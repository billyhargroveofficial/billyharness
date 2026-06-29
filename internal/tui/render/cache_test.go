package render

import "testing"

func TestBlockCacheKeyChangesWithRenderedFields(t *testing.T) {
	base := BlockCacheKeyInput{
		ID:        "block-1",
		Kind:      "assistant",
		CellType:  "assistant_final",
		EventType: "assistant.content_delta",
		Title:     "ASSISTANT",
		Content:   "hello",
		RawCopy:   "hello",
	}
	first := BlockCacheKey(base)
	if first == "" {
		t.Fatal("cache key should not be empty")
	}
	if got := BlockCacheKey(base); got != first {
		t.Fatalf("cache key should be stable, got %q want %q", got, first)
	}
	base.Content = "hello again"
	if got := BlockCacheKey(base); got == first {
		t.Fatal("content change should change cache key")
	}
}

func TestRichCacheKeyIncludesViewState(t *testing.T) {
	base := RichCacheKeyInput{
		BlockCacheKey:  "block",
		Width:          80,
		Theme:          "dark",
		ThinkView:      "expanded",
		ToolView:       "auto",
		BlockCollapsed: false,
		ToolCollapsed:  false,
	}
	first := RichCacheKey(base)
	if first == "" {
		t.Fatal("rich cache key should not be empty")
	}
	base.ToolCollapsed = true
	if got := RichCacheKey(base); got == first {
		t.Fatal("tool collapsed state should change rich cache key")
	}
}

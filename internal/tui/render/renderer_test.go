package render

import "testing"

func TestCellRendererUsesCachedText(t *testing.T) {
	cacheKey := RichCacheKeyInput{
		BlockCacheKey: "block",
		Width:         80,
		Theme:         "dark",
		ThinkView:     "expanded",
		ToolView:      "auto",
	}
	cache := CellCache{
		Key:  RichCacheKey(cacheKey),
		Text: "cached render",
	}
	called := false
	result := NewCellRenderer().Render(CellRenderInput{
		Cache:    cache,
		CacheKey: cacheKey,
		Render: func() string {
			called = true
			return "new render"
		},
	})

	if called {
		t.Fatal("render callback should not run on cache hit")
	}
	if !result.Hit {
		t.Fatal("expected cache hit")
	}
	if result.Text != cache.Text || result.Cache != cache {
		t.Fatalf("unexpected cached result: %#v", result)
	}
}

func TestCellRendererRefreshesCacheOnMiss(t *testing.T) {
	cacheKey := RichCacheKeyInput{
		BlockCacheKey: "block",
		Width:         80,
		Theme:         "dark",
		ThinkView:     "expanded",
		ToolView:      "auto",
	}
	calls := 0
	result := NewCellRenderer().Render(CellRenderInput{
		Cache: CellCache{
			Key:  "stale",
			Text: "stale render",
		},
		CacheKey: cacheKey,
		Render: func() string {
			calls++
			return "fresh render"
		},
	})

	if calls != 1 {
		t.Fatalf("render calls = %d, want 1", calls)
	}
	if result.Hit {
		t.Fatal("expected cache miss")
	}
	if result.Text != "fresh render" {
		t.Fatalf("text = %q", result.Text)
	}
	if result.Cache.Key != RichCacheKey(cacheKey) || result.Cache.Text != result.Text {
		t.Fatalf("unexpected refreshed cache: %#v", result.Cache)
	}
}

func TestCellRendererTreatsEmptyCachedTextAsMiss(t *testing.T) {
	cacheKey := RichCacheKeyInput{
		BlockCacheKey: "block",
		Width:         80,
		Theme:         "dark",
		ThinkView:     "expanded",
		ToolView:      "auto",
	}
	calls := 0
	result := NewCellRenderer().Render(CellRenderInput{
		Cache: CellCache{
			Key: RichCacheKey(cacheKey),
		},
		CacheKey: cacheKey,
		Render: func() string {
			calls++
			return "fresh render"
		},
	})

	if calls != 1 {
		t.Fatalf("render calls = %d, want 1", calls)
	}
	if result.Hit {
		t.Fatal("expected miss for empty cached text")
	}
	if result.Cache.Text != "fresh render" {
		t.Fatalf("cache text = %q", result.Cache.Text)
	}
}

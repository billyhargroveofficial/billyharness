package render

type CellCache struct {
	Key  string
	Text string
}

type CellRenderInput struct {
	Cache    CellCache
	CacheKey RichCacheKeyInput
	Render   func() string
}

type CellRenderResult struct {
	Text  string
	Cache CellCache
	Hit   bool
}

type CellRenderer struct{}

func NewCellRenderer() CellRenderer {
	return CellRenderer{}
}

func (CellRenderer) Render(input CellRenderInput) CellRenderResult {
	key := RichCacheKey(input.CacheKey)
	if input.Cache.Key == key && input.Cache.Text != "" {
		return CellRenderResult{
			Text:  input.Cache.Text,
			Cache: input.Cache,
			Hit:   true,
		}
	}
	rendered := ""
	if input.Render != nil {
		rendered = input.Render()
	}
	return CellRenderResult{
		Text: rendered,
		Cache: CellCache{
			Key:  key,
			Text: rendered,
		},
	}
}

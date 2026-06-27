package tui

import "encoding/json"

type tokenUsage struct {
	InputTokens     int64
	OutputTokens    int64
	CacheHitTokens  int64
	CacheMissTokens int64
	ReasoningTokens int64
}

type usageAccumulator struct {
	last    tokenUsage
	hasLast bool
}

func usageFromAny(value any) tokenUsage {
	bytes, _ := json.Marshal(value)
	var u struct {
		InputTokens     int64 `json:"input_tokens"`
		OutputTokens    int64 `json:"output_tokens"`
		CacheHitTokens  int64 `json:"cache_hit_tokens"`
		CacheMissTokens int64 `json:"cache_miss_tokens"`
		ReasoningTokens int64 `json:"reasoning_tokens"`
	}
	_ = json.Unmarshal(bytes, &u)
	return tokenUsage{
		InputTokens:     nonNegative(u.InputTokens),
		OutputTokens:    nonNegative(u.OutputTokens),
		CacheHitTokens:  nonNegative(u.CacheHitTokens),
		CacheMissTokens: nonNegative(u.CacheMissTokens),
		ReasoningTokens: nonNegative(u.ReasoningTokens),
	}
}

func (a *usageAccumulator) Reset() {
	a.last = tokenUsage{}
	a.hasLast = false
}

func (a *usageAccumulator) Apply(update tokenUsage) tokenUsage {
	if update.zero() {
		return tokenUsage{}
	}
	if !a.hasLast {
		a.last = update
		a.hasLast = true
		return update
	}
	if update == a.last {
		return tokenUsage{}
	}
	if update.atLeast(a.last) {
		delta := update.minus(a.last)
		a.last = update
		return delta
	}
	a.last = update
	return update
}

func (a usageAccumulator) Current() tokenUsage {
	if !a.hasLast {
		return tokenUsage{}
	}
	return a.last
}

func (u tokenUsage) zero() bool {
	return u == tokenUsage{}
}

func (u tokenUsage) atLeast(other tokenUsage) bool {
	return u.InputTokens >= other.InputTokens &&
		u.OutputTokens >= other.OutputTokens &&
		u.CacheHitTokens >= other.CacheHitTokens &&
		u.CacheMissTokens >= other.CacheMissTokens &&
		u.ReasoningTokens >= other.ReasoningTokens
}

func (u tokenUsage) minus(other tokenUsage) tokenUsage {
	return tokenUsage{
		InputTokens:     u.InputTokens - other.InputTokens,
		OutputTokens:    u.OutputTokens - other.OutputTokens,
		CacheHitTokens:  u.CacheHitTokens - other.CacheHitTokens,
		CacheMissTokens: u.CacheMissTokens - other.CacheMissTokens,
		ReasoningTokens: u.ReasoningTokens - other.ReasoningTokens,
	}
}

func nonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

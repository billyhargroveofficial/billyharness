package agent

import (
	"github.com/billyhargroveofficial/billyharness/internal/config"
	"github.com/billyhargroveofficial/billyharness/internal/protocol"
)

var contextThresholdPercents = []int{50, 70, 85, 95}

func emitContextThresholdEvents(
	messages []protocol.Message,
	limits config.RuntimeLimits,
	round int,
	stage string,
	emitted map[int]bool,
	emit func(protocol.Event),
) {
	if limits.ContextWindowTokens <= 0 || emit == nil {
		return
	}
	if emitted == nil {
		emitted = map[int]bool{}
	}
	estimated := estimateMessagesTokens(messages)
	if estimated <= 0 {
		return
	}
	for _, percent := range contextThresholdPercents {
		if emitted[percent] {
			continue
		}
		threshold := contextThresholdTokens(limits.ContextWindowTokens, percent)
		if threshold <= 0 || estimated < threshold {
			continue
		}
		emitted[percent] = true
		remaining := limits.ContextWindowTokens - estimated
		if remaining < 0 {
			remaining = 0
		}
		emit(protocol.Event{
			Type: protocol.EventContextThreshold,
			Data: protocol.ContextThresholdEvent{
				Percent:             percent,
				EstimatedTokens:     estimated,
				ContextWindowTokens: limits.ContextWindowTokens,
				ThresholdTokens:     threshold,
				RemainingTokens:     remaining,
				MessageCount:        len(messages),
				Round:               round,
				Stage:               stage,
				Estimator:           "chars_div_4",
			},
		})
	}
}

func contextThresholdTokens(window int64, percent int) int64 {
	if window <= 0 || percent <= 0 {
		return 0
	}
	return (window*int64(percent) + 99) / 100
}

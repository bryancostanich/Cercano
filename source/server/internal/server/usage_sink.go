package server

import (
	"time"

	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/usage"
)

// UsageEventSink adapts usage.Usage values into telemetry.Event values and
// forwards them via emit. emit is typically collector.Emit. Returns a sink
// suitable for usage.Wrap. Nil emit yields a no-op sink.
func UsageEventSink(emit func(*telemetry.Event)) func(usage.Usage) {
	if emit == nil {
		return func(usage.Usage) {}
	}
	return func(u usage.Usage) {
		e := &telemetry.Event{
			Timestamp:    time.Now(),
			ToolName:     u.Source,
			Model:        u.Model,
			InputTokens:  u.InputTokens,
			OutputTokens: u.OutputTokens,
		}
		// Mirror emitEvent convention: for cloud calls, populate the cloud
		// identification fields so telemetry can distinguish tiers.
		if u.IsCloud {
			e.CloudProvider = "anthropic"
			e.CloudModel = u.Model
		}
		emit(e)
	}
}

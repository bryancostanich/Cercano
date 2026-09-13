package server

import (
	"cercano/source/server/internal/worker"
	"context"
	"log"
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
			DurationMs:   u.DurationMs,
		}
		e.ContentTokensAvoided = u.ContentTokensAvoided
		e.TokenSaving = u.TokenSaving
		// Mirror emitEvent convention: for cloud calls, populate the cloud
		// identification fields so telemetry can distinguish tiers. Use the
		// provider's own name — hardcoding "anthropic" mislabelled every
		// OpenAI/other-provider call and made per-provider latency
		// comparison impossible.
		if u.IsCloud {
			e.CloudProvider = u.Provider
			if e.CloudProvider == "" {
				e.CloudProvider = "cloud"
			}
			e.CloudModel = u.Model
		}
		emit(e)
	}
}

// SetAttemptSink extends existing telemetry wiring with actual attempt records.
// Legacy aggregate/context telemetry remains separate and is not added to them.
func (s *Server) SetAttemptSink(sink usage.AttemptSink) {
	s.attemptSinkMu.Lock()
	s.attemptSink = sink
	s.attemptSinkMu.Unlock()
}
func (s *Server) accountingContext(ctx context.Context, source, conversation string) context.Context {
	s.attemptSinkMu.RLock()
	sink := s.attemptSink
	s.attemptSinkMu.RUnlock()
	return usage.ForOperation(ctx, sink, source, conversation)
}

// SetAccountingCollector configures actual accounting at startup, including
// receipt-backed worker delivery. A plain AttemptSink remains useful for
// embedded/in-process callers; workers need the collector's receipt surface.
func (s *Server) SetAccountingCollector(collector *telemetry.Collector) {
	if collector == nil {
		return
	}
	s.attemptSinkMu.Lock()
	s.accountingReceiver = collector
	s.attemptSinkMu.Unlock()
	s.SetAttemptSink(collector.EmitAttempt)
	s.configureWorkerAccounting()
}
func (s *Server) configureWorkerAccounting() {
	s.attemptSinkMu.RLock()
	receiver := s.accountingReceiver
	s.attemptSinkMu.RUnlock()
	if receiver == nil {
		return
	}
	if target, ok := s.workerRunner.(worker.AccountingConfigurer); ok {
		if err := target.SetAccountingReceiver(receiver); err != nil {
			log.Printf("[accounting] worker configuration failed: %v", err)
			receiver.MarkAccountingIncomplete("worker accounting configuration failed")
		}
	}
}

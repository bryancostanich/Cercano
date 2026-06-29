package server

import (
	"testing"

	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/usage"
)

func TestUsageEventSinkMapsFields(t *testing.T) {
	var got *telemetry.Event
	sink := UsageEventSink(func(e *telemetry.Event) { got = e })
	sink(usage.Usage{Source: "main", Model: "claude-x", IsCloud: true, InputTokens: 12, OutputTokens: 8})
	if got == nil {
		t.Fatal("no event emitted")
	}
	if got.ToolName != "main" || got.Model != "claude-x" || got.InputTokens != 12 || got.OutputTokens != 8 {
		t.Fatalf("bad mapping: %+v", got)
	}
	// cloud fields populated for a cloud call (match emitEvent convention)
	if got.CloudModel == "" && got.CloudProvider == "" {
		t.Fatal("cloud call should set cloud provider/model per the existing convention")
	}
}

func TestUsageEventSinkNilEmitNoPanic(t *testing.T) {
	UsageEventSink(nil)(usage.Usage{Source: "x"}) // must not panic
}

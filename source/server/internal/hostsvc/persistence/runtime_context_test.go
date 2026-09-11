package persistence

import (
	"cercano/source/server/internal/conversation"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"testing"
)

func TestMeterUsesServingModelAndRejectsStaleGeneration(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureConversation(t.Context(), "conv", "/fixture", "local"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{OpenRuntime: "llama_server", LocusMode: "cloud_primary"}
	cfgSvc := cfgsvc.New("", cfg, nil)
	s := &svc{convAgent: viewportResumeFakeAgent{store: store}, cfgSvc: cfgSvc, primaryModel: func() string { return "claude-sonnet-4-6" }}
	current := llm.RuntimeContext{Window: 65536, InstanceID: "process-1"}
	s.SetLocalRuntimeContext(func(model string) (llm.RuntimeContext, error) {
		if model != "local" {
			return llm.RuntimeContext{}, nil
		}
		return current, nil
	})
	s.RecordTurnContextUsage(t.Context(), "conv", TurnContextUsage{Model: "local", Provider: "llama_server", RuntimeInstanceID: "process-1", ContextWindow: 65536, ContextWindowKnown: true, EstimatedRequestTokens: 100, MessageTokens: 90})
	stored, ok, err := store.GetContextUsage(t.Context(), "conv")
	if err != nil || !ok || stored.Model != "local" || stored.Provider != "llama_server" || stored.RuntimeInstanceID != "process-1" {
		t.Fatalf("lost runtime identity: %+v %v", stored, err)
	}
	get := func() *proto.GetContextUsageResponse {
		t.Helper()
		r, err := s.GetContextUsage(t.Context(), &proto.GetContextUsageRequest{ConversationId: "conv"})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if got := get(); got.ModelMax != 65536 || !got.ContextWindowKnown || got.UsageStale {
		t.Fatalf("bad serving meter: %+v", got)
	}
	n := 8192
	cfg.LlamaServer.ContextSize = &n
	if err := cfgSvc.Set(cfg); err != nil {
		t.Fatal(err)
	}
	if got := get(); got.ModelMax != 65536 {
		t.Fatalf("config edit changed meter: %+v", got)
	}
	current.InstanceID = "process-2"
	if got := get(); !got.UsageStale || got.ModelMax != 65536 {
		t.Fatalf("replacement inherited fresh usage: %+v", got)
	}
	current = llm.RuntimeContext{}
	if got := get(); got.ContextWindowKnown || got.ModelMax != 0 || !got.UsageStale {
		t.Fatalf("stopped runtime retained denominator: %+v", got)
	}
	s.SetLocalRuntimeContext(nil)
	if got := get(); got.ContextWindowKnown || got.ModelMax != 0 {
		t.Fatalf("missing resolver guessed: %+v", got)
	}
}

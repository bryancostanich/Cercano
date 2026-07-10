package runner

import (
	"context"
	"sync"
	"testing"

	"cercano/source/server/internal/llm"
)

// TestCore_PersistsWithoutLocalStore_WorkerParity is the regression guard for
// the worker-turn persistence bug (docs/bugs/2026-07-09-worker-turn-persistence.md).
//
// The worker child process runs the runner with Deps.Agent == nil (the store
// lives on the host, not the child) and relies entirely on the injected persist
// PersistFunc, which forwards writes to the host over the stream. Persistence
// used to be gated on `Deps.Agent != nil`, so in worker mode persistEnabled was
// false and EVERY worker-executed turn — user prompt and assistant reply alike —
// was silently dropped from the conversation store. No error was logged.
//
// buildDeps already models the worker shape (Agent: nil). This test supplies a
// capturing persist func and asserts the runner honors it: the user turn must be
// persisted up front (crash resilience), even with no local store.
func TestCore_PersistsWithoutLocalStore_WorkerParity(t *testing.T) {
	deps := buildDeps(&spyProvider{})
	if deps.Agent != nil {
		t.Fatal("precondition: buildDeps must model the worker shape (Deps.Agent == nil)")
	}

	var mu sync.Mutex
	var persisted []llm.Message
	persist := func(m llm.Message) {
		mu.Lock()
		persisted = append(persisted, m)
		mu.Unlock()
	}

	core := New(deps)
	if _, err := core.RunTurn(context.Background(), Request{
		ConversationID: "conv-worker",
		Input:          "hello worker",
		WorkDir:        t.TempDir(),
		Gen:            1,
	}, noopSink{}, nil, persist); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(persisted) == 0 {
		t.Fatal("no turns persisted with Deps.Agent == nil — worker-executed turns " +
			"are being dropped (regression: persistence gated on Agent != nil)")
	}
	if got := string(persisted[0].Role); got != "user" {
		t.Fatalf("first persisted message role = %q, want \"user\" "+
			"(the user turn must be persisted up front, before the model call)", got)
	}
}

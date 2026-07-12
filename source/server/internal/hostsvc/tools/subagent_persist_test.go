package tools

import (
	"context"
	"errors"
	"testing"
)

// TestEnsureSubagentConv covers the persistence decision RunAgenticDispatch uses
// to create the sub-agent conversation row and to report SubConversationID. The
// sub-agent's identity (subConvID) is minted unconditionally elsewhere; this
// helper only governs whether a persisted row exists.
func TestEnsureSubagentConv(t *testing.T) {
	ctx := context.Background()

	// Neither a store (in-process) nor an ensureSubagent proxy (worker) wired:
	// persistence is inactive, but dispatch still runs.
	if (&Service{}).ensureSubagentConv(ctx, "id", "parent", "/wd", "model", []string{"Read"}) {
		t.Error("no store and no proxy: want false")
	}

	// The injected ensureSubagent proxy (worker path) is preferred over the
	// store and receives the sub-agent's identity + linkage verbatim.
	var got struct {
		id, parent, dir, model string
		tools                  []string
	}
	worker := &Service{}
	worker.SetEnsureSubagent(func(_ context.Context, id, parentID, projectDir, model string, grantedTools []string) error {
		got.id, got.parent, got.dir, got.model, got.tools = id, parentID, projectDir, model, grantedTools
		return nil
	})
	if !worker.ensureSubagentConv(ctx, "sub-1", "parent-1", "/repo", "opus", []string{"Read", "Grep"}) {
		t.Fatal("proxy returning nil: want true")
	}
	if got.id != "sub-1" || got.parent != "parent-1" || got.dir != "/repo" || got.model != "opus" {
		t.Errorf("proxy received wrong linkage: %+v", got)
	}
	if len(got.tools) != 2 || got.tools[0] != "Read" || got.tools[1] != "Grep" {
		t.Errorf("proxy received wrong granted tools: %v", got.tools)
	}

	// A proxy error means the row was not created: persistence inactive (the
	// dispatch still runs and tabs — this only gates SubConversationID).
	failing := &Service{}
	failing.SetEnsureSubagent(func(context.Context, string, string, string, string, []string) error {
		return errors.New("store unavailable")
	})
	if failing.ensureSubagentConv(ctx, "id", "", "", "", nil) {
		t.Error("proxy error: want false")
	}
}

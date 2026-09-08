package runner

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/routinglog"
)

// readRoutingEvents returns every routing event of the given name from path.
func readRoutingEvents(t *testing.T, path, event string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open routing log: %v", err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var o map[string]any
		if err := json.Unmarshal(line, &o); err != nil {
			continue
		}
		if o["event"] == event {
			out = append(out, o)
		}
	}
	return out
}

func num(t *testing.T, o map[string]any, key string) int {
	t.Helper()
	v, ok := o[key]
	if !ok {
		t.Fatalf("routing event missing %q; got keys %v", key, o)
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("routing event %q is %T, want number", key, v)
	}
	return int(f)
}

// TestLoopSink_LogsFullRequestBudget pins the regression that motivated this
// event: the assembly-time estimate records messages only, so the routing log
// used to report system_tokens and tool_schema_tokens as zero for every
// request. The tool loop knows the true shape immediately before the provider
// call, and this asserts those components survive to the log intact.
func TestLoopSink_LogsFullRequestBudget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "routing.jsonl")
	w, err := routinglog.NewWriter(path)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	defer w.Close()

	c := New(Deps{RoutingLog: w})
	s := &captureSink{}
	c.makeLoopSink(s, nil, "conv-budget")(agent.LoopEvent{
		Kind:                   agent.LoopRequestAccounting,
		MessageTokens:          1000,
		SystemTokens:           250,
		ToolSchemaTokens:       4000,
		OutputReserveTokens:    512,
		EstimatedRequestTokens: 5762,
		ContextWindow:          32768,
		ContextWindowKnown:     true,
	})

	evs := readRoutingEvents(t, path, "request.budget")
	if len(evs) != 1 {
		t.Fatalf("got %d request.budget events, want 1", len(evs))
	}
	ev := evs[0]

	if got := ev["conversation_id"]; got != "conv-budget" {
		t.Errorf("conversation_id = %v, want conv-budget", got)
	}
	// The components that were previously always zero are the point of the fix.
	if got := num(t, ev, "system_tokens"); got != 250 {
		t.Errorf("system_tokens = %d, want 250", got)
	}
	if got := num(t, ev, "tool_schema_tokens"); got != 4000 {
		t.Errorf("tool_schema_tokens = %d, want 4000", got)
	}
	if got := num(t, ev, "output_reserve_tokens"); got != 512 {
		t.Errorf("output_reserve_tokens = %d, want 512", got)
	}
	if got := num(t, ev, "message_tokens"); got != 1000 {
		t.Errorf("message_tokens = %d, want 1000", got)
	}
	if got := num(t, ev, "estimated_request_tokens"); got != 5762 {
		t.Errorf("estimated_request_tokens = %d, want 5762", got)
	}
	if got := num(t, ev, "context_window"); got != 32768 {
		t.Errorf("context_window = %d, want 32768", got)
	}

	// The whole point: the logged total must exceed messages alone, otherwise
	// analysis of this log understates real request size exactly as before.
	if num(t, ev, "estimated_request_tokens") <= num(t, ev, "message_tokens") {
		t.Error("estimated_request_tokens must exceed message_tokens; " +
			"a messages-only total is the bug this event exists to fix")
	}
}

// TestLoopSink_RequestBudgetSurvivesNilRoutingLog guards the common
// configuration where routing telemetry is disabled.
func TestLoopSink_RequestBudgetSurvivesNilRoutingLog(t *testing.T) {
	c := New(Deps{})
	s := &captureSink{}
	c.makeLoopSink(s, nil, "conv-nil")(agent.LoopEvent{
		Kind:                   agent.LoopRequestAccounting,
		MessageTokens:          10,
		EstimatedRequestTokens: 10,
	})
}

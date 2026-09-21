package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/dispatchtrace"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// Exercise the production dispatch entry, actual tool loop, and provider seam.
// Assert the trace describes what the provider received, not stored history.
func TestDispatchTraceMatchesProviderAndIsolatesDispatches(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	t.Setenv(dispatchtrace.EnableEnv, "1")
	t.Setenv(dispatchtrace.ParentEnv, "trace-parent")
	t.Setenv(dispatchtrace.DirEnv, dir)
	svc := New(nil, nil, nil, nil)
	installTestFailureLog(t, svc)
	svc.SetRegistry(historyProbeRegistry())
	svc.SetLoopCompactorFactory(func() agent.LoopCompactor {
		return agent.LoopCompactorFunc(func(ctx context.Context, h []llm.Message) ([]llm.Message, int, error) {
			// Same-size rewrite, with deliberately mutable blocks: snapshots must still
			// preserve pre-pass evidence and must not depend on message count changing.
			h[0].Blocks[0].Text += " [compacted]"
			return h, 7, nil
		})
	})
	run := func(parent string) *traceHistoryProvider {
		t.Helper()
		p := &traceHistoryProvider{historyProbeProvider: historyProbeProvider{name: "llama_server", answer: "The configuration is loaded in config.go:42."}}
		_, err := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{Mode: dispatch.Agentic, ConversationID: parent, Task: "Trace configuration loading.", Tools: []string{"Read", "Grep"}, MaxIterations: 4}, inference.Selection{Provider: p}, "same-model")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	run("unrelated")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("unrelated dispatch captured")
	}
	p := run("trace-parent")
	run("trace-parent")
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("want one trace: %v %v", paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r struct {
			Kind      string          `json:"kind"`
			Iteration int             `json:"iteration"`
			Event     json.RawMessage `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		counts[r.Kind]++
		switch r.Kind {
		case "model_request":
			var ev struct {
				System   string        `json:"system"`
				Model    string        `json:"model"`
				Messages []llm.Message `json:"messages"`
				Tools    []struct {
					Name   string          `json:"name"`
					Schema json.RawMessage `json:"schema"`
				} `json:"tools"`
			}
			if err := json.Unmarshal(r.Event, &ev); err != nil {
				t.Fatal(err)
			}
			want := p.requests[r.Iteration-1]
			if !reflect.DeepEqual(ev.Messages, want.Messages) || ev.System != want.System || ev.Model != want.Model || len(ev.Tools) != len(want.Tools) {
				t.Fatalf("request %d did not match provider input: traced=%+v provider=%+v", r.Iteration, ev.Messages, want.Messages)
			}
			for i, tool := range ev.Tools {
				var a, b any
				json.Unmarshal(tool.Schema, &a)
				json.Unmarshal(want.Tools[i].Schema, &b)
				if tool.Name != want.Tools[i].Name || !reflect.DeepEqual(a, b) {
					t.Fatalf("tool schema mismatch: %s", tool.Name)
				}
			}
		case "compaction":
			var ev struct {
				Before []llm.Message `json:"history_before"`
				After  []llm.Message `json:"history_after"`
				Spent  int           `json:"spent_tokens"`
			}
			if err := json.Unmarshal(r.Event, &ev); err != nil {
				t.Fatal(err)
			}
			if len(ev.Before) != len(ev.After) || ev.After[0].Blocks[0].Text != ev.Before[0].Blocks[0].Text+" [compacted]" || ev.Spent != 7 {
				t.Fatalf("lost same-size compaction evidence: %+v", ev)
			}
		}
	}
	for kind, want := range map[string]int{"model_request": 3, "model_response": 3, "compaction": 3, "tool_call": 2, "tool_result": 2, "dispatch_done": 1} {
		if counts[kind] != want {
			t.Errorf("%s: got %d want %d", kind, counts[kind], want)
		}
	}
}

// Keep an immutable observation of the adapter input. The production loop may
// reuse/mutate history slices on later iterations, which is precisely why
// comparing saved shallow ChatRequest values is not a valid fidelity test.
type traceHistoryProvider struct{ historyProbeProvider }

func (p *traceHistoryProvider) StreamChat(ctx context.Context, r llm.ChatRequest) (llm.StreamReader, error) {
	reader, err := p.historyProbeProvider.StreamChat(ctx, r)
	p.requests[len(p.requests)-1].Messages = dispatchtrace.Snapshot(r.Messages)
	return reader, err
}

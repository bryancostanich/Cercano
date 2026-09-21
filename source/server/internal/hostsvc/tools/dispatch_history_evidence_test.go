package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"reflect"

	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/dispatchhistory"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// Exercise the production dispatch entry, actual tool loop, and provider seam.
// Assert the trace describes what the provider received, not stored history.
func TestDispatchHistoryMatchesProviderAndRecordsEveryDispatch(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"unrelated", "trace-parent"} {
		if err := store.EnsureConversation(t.Context(), id, "", "model"); err != nil {
			t.Fatal(err)
		}
	}
	svc := New(nil, nil, func() conversation.Store { return store }, nil)
	events := store.(conversation.DispatchEventStore)
	var ids []string

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
		result, err := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{Mode: dispatch.Agentic, ConversationID: parent, Task: "Trace configuration loading.", Tools: []string{"Read", "Grep"}, MaxIterations: 4}, inference.Selection{Provider: p}, "same-model")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, result.SubConversationID)
		return p
	}
	run("unrelated")
	p := run("trace-parent")
	run("trace-parent")
	for _, id := range ids {
		rows, err := events.ListDispatchEvents(t.Context(), id)
		if err != nil || len(rows) == 0 {
			t.Fatalf("dispatch %s not recorded: %v", id, err)
		}
	}
	rows, err := events.ListDispatchEvents(t.Context(), ids[1])
	if err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{}
	for _, row := range rows {
		r := struct {
			Kind      string
			Iteration int
			Event     json.RawMessage
		}{row.Kind, row.Iteration, json.RawMessage(row.PayloadJSON)}

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
	p.requests[len(p.requests)-1].Messages = dispatchhistory.Snapshot(r.Messages)
	return reader, err
}

func TestDispatchHistoryFailureIsVisibleWithoutFailingTask(t *testing.T) {
	svc := New(nil, nil, nil, nil)
	installTestFailureLog(t, svc)
	svc.SetRegistry(historyProbeRegistry())
	svc.SetDispatchEventSink(func(context.Context, conversation.DispatchEvent) error { return errors.New("disk failure SECRET") })
	p := &historyProbeProvider{name: "llama_server", answer: "The configuration is loaded in config.go:42."}
	result, err := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{Mode: dispatch.Agentic, Task: "Trace configuration loading", Tools: []string{"Read", "Grep"}, MaxIterations: 4}, inference.Selection{Provider: p}, "model")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "Dispatch evidence incomplete") || strings.Contains(result.Text, "SECRET") {
		t.Fatal("missing safe failure warning")
	}
}

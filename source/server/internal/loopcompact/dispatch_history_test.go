package loopcompact

import (
	"context"
	"encoding/json"

	"strings"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/dispatchhistory"
	"cercano/source/server/internal/llm"
)

type traceSummaryRunner struct{ requests []*agent.Request }

func (*traceSummaryRunner) Name() string { return "scripted" }
func (r *traceSummaryRunner) Process(_ context.Context, req *agent.Request) (*agent.Response, error) {
	r.requests = append(r.requests, req)
	return &agent.Response{Output: "<goal>implement task</goal><state>exact summary evidence</state>", InputTokens: 37, OutputTokens: 11}, nil
}

func TestSummarizerDispatchHistoryWiring(t *testing.T) {
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureConversation(t.Context(), "child", "", "model"); err != nil {
		t.Fatal(err)
	}
	events := store.(conversation.DispatchEventStore)
	tr := dispatchhistory.Begin(t.Context(), "child", events.AppendDispatchEvent)
	defer tr.Close()

	runner := &traceSummaryRunner{}
	summarize := BuildSummarizer(WiringDeps{OpenRunner: func() agent.TurnRunner { return runner }})
	ctx := dispatchhistory.WithRecorder(t.Context(), tr)
	ctx = agent.WithLoopCompactionScope(ctx, agent.LoopCompactionScope{ConversationID: "child", Iteration: 4})
	_, err = summarize(ctx, []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "exact task evidence"}}}})
	if err != nil {
		t.Fatal(err)
	}
	tr.Close()
	if len(runner.requests) != 1 {
		t.Fatalf("runner calls=%d", len(runner.requests))
	}
	rows, err := events.ListDispatchEvents(t.Context(), "child")
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, row := range rows {
		var rec struct {
			Kind      string `json:"kind"`
			Iteration int    `json:"iteration"`
			Event     struct {
				RequestID   string   `json:"request_id"`
				Prompt      string   `json:"prompt"`
				Output      string   `json:"output"`
				InputTokens int      `json:"input_tokens"`
				Temperature *float64 `json:"temperature"`
			} `json:"event"`
		}
		rec.Kind = row.Kind
		rec.Iteration = row.Iteration
		if err := json.Unmarshal([]byte(row.PayloadJSON), &rec.Event); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(rec.Kind, "summarizer_") {
			continue
		}
		seen[rec.Kind] = true
		if rec.Iteration != 4 || rec.Event.RequestID != runner.requests[0].RequestID {
			t.Fatalf("lost correlation: %+v", rec)
		}
		if rec.Kind == "summarizer_request" && (rec.Event.Prompt != runner.requests[0].Input || rec.Event.Temperature == nil || *rec.Event.Temperature != 0) {
			t.Fatalf("wrong request: %+v", rec)
		}
		if rec.Kind == "summarizer_response" && (rec.Event.InputTokens != 37 || !strings.Contains(rec.Event.Output, "exact summary evidence")) {
			t.Fatalf("wrong response: %+v", rec)
		}
	}
	if !seen["summarizer_request"] || !seen["summarizer_response"] {
		t.Fatal("missing summarizer events")
	}
}

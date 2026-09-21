package loopcompact

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/dispatchtrace"
	"cercano/source/server/internal/llm"
)

type traceSummaryRunner struct{ requests []*agent.Request }

func (*traceSummaryRunner) Name() string { return "scripted" }
func (r *traceSummaryRunner) Process(_ context.Context, req *agent.Request) (*agent.Response, error) {
	r.requests = append(r.requests, req)
	return &agent.Response{Output: "<goal>implement task</goal><state>exact summary evidence</state>", InputTokens: 37, OutputTokens: 11}, nil
}

func TestSummarizerDispatchTraceWiring(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "trace")
	t.Setenv(dispatchtrace.EnableEnv, "1")
	t.Setenv(dispatchtrace.ParentEnv, "parent")
	t.Setenv(dispatchtrace.DirEnv, dir)
	tr := dispatchtrace.Begin("child", "parent")
	if tr == nil {
		t.Fatal("trace disabled")
	}
	defer tr.Close()
	runner := &traceSummaryRunner{}
	summarize := BuildSummarizer(WiringDeps{OpenRunner: func() agent.TurnRunner { return runner }})
	ctx := dispatchtrace.WithTrace(t.Context(), tr)
	ctx = agent.WithLoopCompactionScope(ctx, agent.LoopCompactionScope{ConversationID: "child", Iteration: 4})
	_, err := summarize(ctx, []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "exact task evidence"}}}})
	if err != nil {
		t.Fatal(err)
	}
	tr.Close()
	if len(runner.requests) != 1 {
		t.Fatalf("runner calls=%d", len(runner.requests))
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
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
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
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

package tools

import (
	"context"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

func TestDispatchPreservesOriginalPromptAcrossCompactions(t *testing.T) {
	const task = "Implement ONLY phase one.\nSmall bounded probes only; no global downloads.\nReturn an architecture blocker before coding.\nDo not invent calibration — retain these exact bytes.\n"
	for _, providerName := range []string{"llama_server", "mistralrs", "anthropic"} {
		t.Run(providerName, func(t *testing.T) {
			svc := New(nil, nil, nil, nil)
			installTestFailureLog(t, svc)
			svc.SetRegistry(historyProbeRegistry())
			passes, taskSeen := 0, false
			svc.SetLoopCompactorFactory(func() agent.LoopCompactor {
				return agent.LoopCompactorFunc(func(_ context.Context, h []llm.Message) ([]llm.Message, int, error) {
					passes++
					for _, m := range h {
						for _, b := range m.Blocks {
							if b.Text == task {
								taskSeen = true
							}
						}
					}
					out := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Summary has omitted the original constraints."}}}}
					if len(h) >= 2 {
						out = append(out, h[len(h)-2:]...)
					}
					return out, 1, nil
				})
			})
			p := &traceHistoryProvider{historyProbeProvider: historyProbeProvider{name: providerName, answer: "Configuration evidence collected."}}
			_, err := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{Mode: dispatch.Agentic, Task: task, Tools: []string{"Read", "Grep"}, MaxIterations: 4}, inference.Selection{Provider: p, IsCloud: providerName == "anthropic"}, "model")
			if err != nil {
				t.Fatal(err)
			}
			if len(p.requests) != 3 || passes < 2 {
				t.Fatalf("requests=%d passes=%d", len(p.requests), passes)
			}
			for i, r := range p.requests {
				if len(r.Messages) == 0 || r.Messages[0].Role != llm.RoleUser || len(r.Messages[0].Blocks) != 1 || r.Messages[0].Blocks[0].Text != task {
					t.Errorf("request %d lost or rewrote the original task", i+1)
				}
				count := 0
				for _, m := range r.Messages {
					for _, b := range m.Blocks {
						if b.Text == task {
							count++
						}
					}
				}
				if count != 1 {
					t.Errorf("request %d task occurrences=%d, want exactly one", i+1, count)
				}
				if !llm.IsValidPairing(r.Messages) {
					t.Errorf("request %d broke tool pairing", i+1)
				}
			}
			if taskSeen {
				t.Error("original task was passed to the compactor")
			}
		})
	}
}

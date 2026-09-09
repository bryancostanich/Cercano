package tools

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/inference"
)

// runDispatchCapturingLog runs a dispatch with the supplied context-window
// resolver and returns the tool loop's request log output.
//
// The window/known pair is emitted on an internal EventSink that does not
// escape RunAgenticDispatch, so the "[tool-loop] model request:" line is the
// observable seam for this behavior at the package boundary. It is also the
// exact line used to diagnose this bug in production.
func runDispatchCapturingLog(t *testing.T, resolver func(string, bool) int, isCloud bool, model string) string {
	t.Helper()

	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})

	svc := New(nil, nil, nil, nil)
	svc.SetRegistry(regWith(permStub{"Glob", agenttools.PermR}))
	if resolver != nil {
		svc.SetContextWindowResolver(resolver)
	}

	if _, err := svc.RunAgenticDispatch(t.Context(), dispatch.Spec{
		Mode:           dispatch.Agentic,
		Task:           "Summarize the repository layout.",
		Tools:          []string{"Glob"},
		MaxIterations:  1,
		ConversationID: "parent-ctxwindow",
	}, inference.Selection{Provider: textOnlyProvider{}, IsCloud: isCloud}, model); err != nil {
		t.Fatalf("RunAgenticDispatch() error = %v", err)
	}

	return buf.String()
}

// TestRunAgenticDispatch_PropagatesContextWindowKnown guards the bug where the
// dispatch path set ToolLoopInput.ContextWindow but left ContextWindowKnown at
// its zero value, so a correctly-resolved 131072-token window was still
// reported as "unknown" to request accounting, persistence, and the context
// meter UI.
func TestRunAgenticDispatch_PropagatesContextWindowKnown(t *testing.T) {
	tests := []struct {
		name     string
		resolver func(string, bool) int
		isCloud  bool
		model    string
		want     string
	}{
		{
			name:     "local model with resolved window is known",
			resolver: func(string, bool) int { return 131072 },
			isCloud:  false,
			model:    "glm-4.5-air-q4_k_m",
			want:     "context_window=131072 context_window_known=true",
		},
		{
			name:     "resolver reporting zero stays unknown",
			resolver: func(string, bool) int { return 0 },
			isCloud:  true,
			model:    "gpt-5",
			want:     "context_window_known=false",
		},
		{
			name:     "no resolver wired stays unknown",
			resolver: nil,
			isCloud:  false,
			model:    "some-model",
			want:     "context_window_known=false",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := runDispatchCapturingLog(t, tc.resolver, tc.isCloud, tc.model)
			if !strings.Contains(out, "[tool-loop] model request:") {
				t.Fatalf("no tool-loop request log line captured; got:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("want log to contain %q\ngot:\n%s", tc.want, out)
			}
		})
	}
}

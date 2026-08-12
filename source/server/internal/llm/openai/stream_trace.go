//go:build cercano_streamtrace

// Package openai's raw-stream tracer. This file is compiled ONLY when the
// binary is built with the `cercano_streamtrace` build tag:
//
//	go build -tags cercano_streamtrace ./...
//
// It is the ground-truth observation hook for diagnosing how an
// OpenAI-compatible server (e.g. llama-server serving GLM-4.5-Air) splits a
// single tool call across streaming fragments — the exact wire shape the
// deferred-name reassembly in stream.go must tolerate. Normal builds get the
// zero-cost no-op stub in stream_trace_stub.go instead, so production never
// pays an os.Stat / env read on the streaming hot path.
//
// Once you are running a trace-tagged binary, turn the trace on at runtime with
// EITHER:
//   - env: CERCANO_TRACE_OPENAI_STREAM=1 (read once at process start), or
//   - sentinel file: touch ~/.cercano/trace-openai-stream (checked live, so it
//     can be toggled on an already-running server without a restart — useful for
//     the long-lived singleton server spawned by the CLI).
//
// Output goes to stderr as one line per tool-call fragment, e.g.:
//
//	[openai-stream-trace] tool_call fragment idx=0 id="abc" name="Read" args="{"
package openai

import (
	"fmt"
	"os"

	goopenai "github.com/sashabaranov/go-openai"
)

// traceStreamEnv is read once at process start.
var traceStreamEnv = os.Getenv("CERCANO_TRACE_OPENAI_STREAM") == "1"

func traceStreamEnabled() bool {
	if traceStreamEnv {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	if _, err := os.Stat(home + "/.cercano/trace-openai-stream"); err == nil {
		return true
	}
	return false
}

func traceToolFragment(tc goopenai.ToolCall) {
	if !traceStreamEnabled() {
		return
	}
	idx := -1
	if tc.Index != nil {
		idx = *tc.Index
	}
	fmt.Fprintf(os.Stderr, "[openai-stream-trace] tool_call fragment idx=%d id=%q name=%q args=%q\n",
		idx, tc.ID, tc.Function.Name, tc.Function.Arguments)
}

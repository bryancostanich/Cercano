//go:build !cercano_streamtrace

package openai

import goopenai "github.com/sashabaranov/go-openai"

// traceToolFragment is a no-op in normal builds. The real raw-stream tracer is
// compiled in only under the `cercano_streamtrace` build tag (see
// stream_trace.go). This stub keeps the streaming path free of any os.Stat /
// env lookup — the Go compiler inlines the empty body away entirely.
func traceToolFragment(goopenai.ToolCall) {}

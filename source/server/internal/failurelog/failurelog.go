package failurelog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Writer appends one structured failure/degradation event per line. It records
// diagnostic metadata only: event classes, provider/model/tool names, IDs, and
// short sanitized messages. It must not receive prompt text, tool arguments,
// tool outputs, source snippets, API keys, or raw provider payloads.
type Writer struct {
	mu sync.Mutex
	f  *os.File
}

// DefaultPath returns ~/.config/cercano/failures.jsonl.
func DefaultPath() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "cercano", "failures.jsonl")
	}
	return "failures.jsonl"
}

// NewWriter opens path for append. Parent directories are created.
func NewWriter(path string) (*Writer, error) {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f}, nil
}

func (w *Writer) Close() error {
	if w == nil || w.f == nil {
		return nil
	}
	return w.f.Close()
}

// Event is intentionally generic so call sites can add metadata as diagnostics
// needs evolve. Use stable snake_case keys and sanitized values only.
type Event map[string]any

// SanitizeMessage normalizes diagnostic messages to short single-line strings.
// Callers must still avoid passing prompt text, tool arguments, tool outputs,
// source snippets, API keys, or raw provider payloads.
func SanitizeMessage(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	const max = 500
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// Log appends a JSONL event. Logging is best-effort: invalid fields or write
// errors are ignored so diagnostics can never break the agent path being logged.
func (w *Writer) Log(event string, fields Event) {
	if w == nil || w.f == nil {
		return
	}
	if fields == nil {
		fields = Event{}
	}
	fields["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	fields["event"] = event
	b, err := json.Marshal(fields)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = w.f.Write(append(b, '\n'))
}

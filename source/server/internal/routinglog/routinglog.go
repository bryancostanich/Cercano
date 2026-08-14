package routinglog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Writer appends one structured routing event per line. It deliberately records
// provider/profile/model routing metadata and error classes, never prompt text,
// tool arguments, API keys, or response bodies.
type Writer struct {
	mu sync.Mutex
	f  *os.File
}

// DefaultPath returns ~/.config/cercano/turn-routing.jsonl.
func DefaultPath() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "cercano", "turn-routing.jsonl")
	}
	return "turn-routing.jsonl"
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

// Event is intentionally generic so call sites can add fields as routing bugs
// reveal what is missing. Use stable snake_case keys.
type Event map[string]any

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

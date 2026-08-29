package failurelog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	path := DefaultPath()
	if !strings.HasSuffix(path, filepath.Join(".config", "cercano", "failures.jsonl")) && path != "failures.jsonl" {
		t.Fatalf("DefaultPath() = %q, want failures.jsonl under config dir", path)
	}
}

func TestLogAppendsJSONLEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "failures.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	defer w.Close()

	w.Log("main.tool_loop_failed", Event{
		"scope":           "main",
		"conversation_id": "conv_123",
		"error_class":     "network",
		"message":         "provider unavailable",
	})
	w.Log("dispatch.failed", nil)

	f, err := NewWriter(filepath.Join(t.TempDir(), "other.jsonl"))
	if err != nil {
		t.Fatalf("NewWriter second writer error = %v", err)
	}
	_ = f.Close()

	data, err := readJSONLLines(path)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(data) != 2 {
		t.Fatalf("got %d events, want 2", len(data))
	}
	if data[0]["event"] != "main.tool_loop_failed" {
		t.Fatalf("event = %v", data[0]["event"])
	}
	if data[0]["scope"] != "main" || data[0]["conversation_id"] != "conv_123" || data[0]["error_class"] != "network" {
		t.Fatalf("metadata not preserved: %#v", data[0])
	}
	if _, ok := data[0]["ts"].(string); !ok {
		t.Fatalf("missing string timestamp: %#v", data[0])
	}
	if data[1]["event"] != "dispatch.failed" {
		t.Fatalf("second event = %v", data[1]["event"])
	}
}

func TestLogDropsUnmarshalableFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failures.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	defer w.Close()

	w.Log("bad", Event{"fn": func() {}})

	data, err := readJSONLLines(path)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("got %d events, want invalid event dropped", len(data))
	}
}

func TestNilWriterNoops(t *testing.T) {
	var w *Writer
	w.Log("main.provider_error", Event{"message": "ignored"})
	if err := w.Close(); err != nil {
		t.Fatalf("nil Close() error = %v", err)
	}
}

func readJSONLLines(path string) ([]map[string]any, error) {
	f, err := NewWriter(filepath.Join(filepath.Dir(path), "noop.jsonl"))
	if err != nil {
		return nil, err
	}
	_ = f.Close()

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var out []map[string]any
	s := bufio.NewScanner(file)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, s.Err()
}

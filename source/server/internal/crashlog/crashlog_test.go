package crashlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestNewWriter_CreatesFileAndParentDirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "path")
	path := filepath.Join(dir, "crash.log")
	w, err := NewWriter(path, "test-1.0")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("parent dir was not created: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("crash log file was not created: %v", err)
	}
	if w.Path() != path {
		t.Errorf("Path() = %q, want %q", w.Path(), path)
	}
}

func TestLogPanic_WritesJSONLineWithStandardFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.log")
	w, err := NewWriter(path, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	w.LogPanic("boom", []byte("goroutine 1 [running]:\nfoo\nbar"), map[string]any{"conv_id": "abc"})
	w.Close()

	entries, err := TailEntries(path, 10)
	if err != nil {
		t.Fatalf("TailEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Kind != KindPanic {
		t.Errorf("Kind = %q, want panic", e.Kind)
	}
	if e.Reason != "boom" {
		t.Errorf("Reason = %q", e.Reason)
	}
	if !strings.Contains(e.Stack, "goroutine 1") {
		t.Errorf("Stack missing goroutine header: %q", e.Stack)
	}
	if e.CercanoVersion != "1.2.3" {
		t.Errorf("CercanoVersion = %q, want 1.2.3", e.CercanoVersion)
	}
	if e.Extra["conv_id"] != "abc" {
		t.Errorf("Extra: %+v", e.Extra)
	}
	if e.Timestamp.IsZero() {
		t.Error("Timestamp was not stamped")
	}
}

func TestLogSignal_CapturesGoroutineDump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.log")
	w, err := NewWriter(path, "test")
	if err != nil {
		t.Fatal(err)
	}
	w.LogSignal("SIGTERM", nil)
	w.Close()

	entries, err := TailEntries(path, 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d (err=%v)", len(entries), err)
	}
	e := entries[0]
	if e.Kind != KindSignal {
		t.Errorf("Kind = %q, want signal", e.Kind)
	}
	if e.Signal != "SIGTERM" {
		t.Errorf("Signal = %q", e.Signal)
	}
	// runtime.Stack(true) always returns at least the calling goroutine.
	if !strings.Contains(e.Stack, "goroutine") {
		t.Errorf("Stack missing goroutine header: %q", e.Stack)
	}
}

func TestConcurrentWritesDoNotInterleave(t *testing.T) {
	// A panic on one goroutine and a signal handler running concurrently
	// must both write complete JSON lines. The mutex around write() is
	// what guarantees this; the test is a regression guard.
	path := filepath.Join(t.TempDir(), "crash.log")
	w, err := NewWriter(path, "test")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			w.LogPanic("panic reason", []byte("stack"), map[string]any{"idx": i})
		}(i)
		go func(i int) {
			defer wg.Done()
			w.LogSignal("SIGTERM", map[string]any{"idx": i})
		}(i)
	}
	wg.Wait()
	w.Close()

	// Every line must parse as valid JSON.
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 100 {
		t.Fatalf("got %d lines, want 100 (concurrent writes lost or interleaved)", len(lines))
	}
	for i, line := range lines {
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %d not valid JSON: %v\n%s", i, err, line)
		}
	}
}

func TestTailEntries_ReturnsMostRecentFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.log")
	w, err := NewWriter(path, "test")
	if err != nil {
		t.Fatal(err)
	}
	w.LogPanic("first", nil, nil)
	w.LogPanic("second", nil, nil)
	w.LogPanic("third", nil, nil)
	w.Close()

	entries, err := TailEntries(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d, want 3", len(entries))
	}
	if entries[0].Reason != "third" {
		t.Errorf("newest-first ordering broken: [0]=%q", entries[0].Reason)
	}
	if entries[2].Reason != "first" {
		t.Errorf("newest-first ordering broken: [2]=%q", entries[2].Reason)
	}
}

func TestTailEntries_MissingFileReturnsNilNoError(t *testing.T) {
	// No crashes yet = valid state, not an error. CLI should be able to
	// call LatestSummary on a fresh install without any error handling.
	entries, err := TailEntries(filepath.Join(t.TempDir(), "does-not-exist.log"), 10)
	if err != nil {
		t.Fatalf("expected nil err for missing file, got %v", err)
	}
	if entries != nil {
		t.Fatalf("expected nil entries, got %+v", entries)
	}
}

func TestTailEntries_SkipsMalformedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.log")
	// Write mixed valid + garbage lines directly.
	content := `{"kind":"panic","reason":"real"}
this is not json
{"kind":"panic","reason":"also-real"}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := TailEntries(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Malformed line dropped; the two valid ones survive.
	if len(entries) != 2 {
		t.Fatalf("got %d, want 2", len(entries))
	}
}

func TestLatestSummary_FormatsFirstLineOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.log")
	w, err := NewWriter(path, "test")
	if err != nil {
		t.Fatal(err)
	}
	w.LogPanic("nil pointer dereference\nat foo.go:42\nother junk", nil, nil)
	w.Close()

	summary := LatestSummary(path)
	if !strings.Contains(summary, "nil pointer dereference") {
		t.Errorf("summary = %q, expected first line of reason", summary)
	}
	if strings.Contains(summary, "foo.go:42") {
		t.Errorf("summary should not include lines past the first: %q", summary)
	}
}

func TestLatestSummary_EmptyForMissingLog(t *testing.T) {
	if s := LatestSummary(filepath.Join(t.TempDir(), "nope.log")); s != "" {
		t.Errorf("expected empty summary for missing file, got %q", s)
	}
}

func TestRecoverAndLog_CapturesPanicAndDoesNotRepanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.log")
	w, err := NewWriter(path, "test")
	if err != nil {
		t.Fatal(err)
	}

	func() {
		defer RecoverAndLog(w, "test worker", map[string]any{"attempt": 3})
		panic("simulated background panic")
	}()

	w.Close()
	entries, err := TailEntries(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Kind != KindGoroutinePanic {
		t.Errorf("Kind = %q, want goroutine_panic", entries[0].Kind)
	}
	if !strings.Contains(entries[0].Reason, "simulated background panic") {
		t.Errorf("Reason = %q", entries[0].Reason)
	}
	if entries[0].Extra["description"] != "test worker" {
		t.Errorf("Extra description missing: %+v", entries[0].Extra)
	}
}

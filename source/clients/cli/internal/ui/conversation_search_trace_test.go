package ui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestConversationSearchTraceLifecycleAndPrivacy(t *testing.T) {
	t.Setenv("CERCANO_SEARCH_TRACE", "1")
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	t.Setenv("CERCANO_SEARCH_TRACE_LOG", path)
	m := searchModel(t)
	const query = "PRIVATE_QUERY_73921"
	const body = "PRIVATE_MESSAGE_81935"
	m.mainChat().SetEntries([]*Entry{{Role: RoleUser, Content: body + " " + query}})
	m.relayout()
	m.openConversationSearch("")
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'P', Text: query})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("missing worker")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	_ = m.View()
	var data []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, _ = os.ReadFile(path)
		if bytes.Contains(data, []byte(`"stage":"view.end"`)) && bytes.HasSuffix(data, []byte("\n")) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if bytes.Contains(data, []byte(query)) || bytes.Contains(data, []byte(body)) {
		t.Fatal("private text leaked into telemetry")
	}
	stages := map[string]bool{}
	spans := map[uint64]int{}
	var session uint64
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var event searchTraceEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("invalid trace: %v", err)
		}
		if session == 0 {
			session = event.Session
		}
		if event.Session != session || event.PID != os.Getpid() {
			t.Fatal("trace lost process/session correlation")
		}
		if event.Span != 0 {
			spans[event.Span]++
		}
		stages[event.Stage] = true
		if event.Stage == "worker.results" && (event.Matches != 1 || event.QueryBytes != len(query)) {
			t.Fatalf("incorrect worker metadata: %+v", event)
		}
	}
	for _, stage := range []string{"session.open", "invalidate", "update.tea.KeyPressMsg.begin", "update.tea.KeyPressMsg.end", "snapshot.begin", "snapshot.end", "worker.queue", "worker.begin", "worker.end", "projection.total", "matching.total", "result.queue", "result.applied", "apply.begin", "apply.end", "view.begin", "view.end"} {
		if !stages[stage] {
			t.Errorf("missing stage %s", stage)
		}
	}
	for span, count := range spans {
		if count != 2 {
			t.Errorf("unpaired span %d: %d events", span, count)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatal("trace file is readable by other users")
	}
}
func TestConversationSearchTraceDisabled(t *testing.T) {
	t.Setenv("CERCANO_SEARCH_TRACE", "0")
	path := filepath.Join(t.TempDir(), "absent.jsonl")
	t.Setenv("CERCANO_SEARCH_TRACE_LOG", path)
	m := searchModel(t)
	m.openConversationSearch("t")
	settleSearch(t, &m)
	_ = m.View()
	if m.search.traceContext().Session != 0 {
		t.Fatal("disabled trace created a session")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("disabled trace created a file: %v", err)
	}
}
func TestConversationSearchTraceQueueNeverWaitsForDisk(t *testing.T) {
	queue := make(chan searchTraceEvent, 1)
	var dropped atomic.Uint64
	enqueueSearchTrace(queue, searchTraceEvent{}, &dropped)
	done := make(chan struct{})
	go func() { enqueueSearchTrace(queue, searchTraceEvent{}, &dropped); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("full trace queue blocked producer")
	}
	if dropped.Load() != 1 {
		t.Fatal("dropped event not counted")
	}
	<-queue
	enqueueSearchTrace(queue, searchTraceEvent{}, &dropped)
	if (<-queue).Dropped != 1 {
		t.Fatal("dropped event count not exposed")
	}
}
func TestConversationSearchTraceUnwritablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeSearchTrace(searchTraceEvent{path: filepath.Join(path, "trace")}); err == nil {
		t.Fatal("expected write error")
	}
}

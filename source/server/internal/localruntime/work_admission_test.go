package localruntime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type admissionTransport func(*http.Request) (*http.Response, error)

func (f admissionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type admissionBody struct {
	started chan struct{}
	finish  chan struct{}
	once    sync.Once
}

func (b *admissionBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.finish
	return 0, io.EOF
}
func (*admissionBody) Close() error { return nil }
func testDownloadRecord(t *testing.T) ModelRecord {
	return ModelRecord{ID: "fixture-model", Runtime: "fixture-runtime", Path: filepath.Join(t.TempDir(), "model.bin"), DownloadURL: "https://fixture.invalid/model", DownloadState: DownloadNotStarted}
}
func TestDownloadWorkLifetimeOutlivesCancellationStatus(t *testing.T) {
	m := NewManager()
	var active atomic.Int64
	if err := m.BindDownloadWork(func() (func(), error) { active.Add(1); return func() { active.Add(-1) }, nil }); err != nil {
		t.Fatal(err)
	}
	body := &admissionBody{started: make(chan struct{}), finish: make(chan struct{})}
	defer close(body.finish)
	m.httpClient = &http.Client{Transport: admissionTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header)}, nil
	})}
	model := testDownloadRecord(t)
	m.EnrollDownload(model)
	req := DownloadRequest{Runtime: model.Runtime, ModelID: model.ID}
	if _, err := m.DownloadModel(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	select {
	case <-body.started:
	case <-time.After(2 * time.Second):
		t.Fatal("download never read fixture body")
	}
	if active.Load() != 1 {
		t.Fatal("asynchronous job not retained")
	}
	if _, err := m.DownloadModel(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if active.Load() != 1 {
		t.Fatal("duplicate request allocated second job")
	}
	if _, err := m.CancelDownload(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if active.Load() != 1 {
		t.Fatal("cancellation status incorrectly marked running IO idle")
	}
	// Let the job really end, then observe release; avoid racing test cleanup.
	body.finish <- struct{}{}
	deadline := time.After(2 * time.Second)
	for active.Load() != 0 {
		select {
		case <-deadline:
			t.Fatal("completed job lease leaked")
		case <-time.After(time.Millisecond):
		}
	}
}
func TestDownloadWorkAdmissionRefusesBeforeSpawning(t *testing.T) {
	m := NewManager()
	refused := errors.New("paused")
	if err := m.BindDownloadWork(func() (func(), error) { return nil, refused }); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	m.httpClient = &http.Client{Transport: admissionTransport(func(*http.Request) (*http.Response, error) { requests.Add(1); return nil, errors.New("unexpected") })}
	model := testDownloadRecord(t)
	m.EnrollDownload(model)
	if _, err := m.DownloadModel(context.Background(), DownloadRequest{Runtime: model.Runtime, ModelID: model.ID}); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatal("refused work spawned")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.downloadJobs) != 0 || len(m.allDownloadJobs) != 0 {
		t.Fatal("refused work registered a job")
	}
}
func TestDownloadWorkRetroactiveRetiredJobAndIdempotentRelease(t *testing.T) {
	m := NewManager()
	old, newer := &downloadJob{}, &downloadJob{}
	m.downloadJobs["model"] = newer
	m.allDownloadJobs = map[*downloadJob]struct{}{old: {}, newer: {}}
	active := 0
	if err := m.BindDownloadWork(func() (func(), error) { active++; return func() { active-- }, nil }); err != nil {
		t.Fatal(err)
	}
	if active != 2 {
		t.Fatalf("retired job lost: %d", active)
	}
	m.clearDownloadJob("model", old)
	m.clearDownloadJob("model", old)
	if active != 1 || m.downloadJobs["model"] != newer {
		t.Fatal("old cleanup affected replacement")
	}
	m.clearDownloadJob("model", newer)
	if active != 0 {
		t.Fatal("lease leaked")
	}
}
func TestDownloadWorkFailedBindingRollsBackAcquiredLeases(t *testing.T) {
	m := NewManager()
	m.allDownloadJobs = map[*downloadJob]struct{}{&downloadJob{}: {}, &downloadJob{}: {}}
	active, calls := 0, 0
	err := m.BindDownloadWork(func() (func(), error) {
		calls++
		if calls == 2 {
			return nil, errors.New("fixture")
		}
		active++
		return func() { active-- }, nil
	})
	if err == nil || active != 0 || m.downloadAdmission != nil {
		t.Fatal("partial binding installed")
	}
}

package worker

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestWorkerOutputVisibleBeforeExit(t *testing.T) {
	r, w := io.Pipe()
	lines := make(chan string, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		forwardWorkerOutput(r, 42, func(f string, a ...any) { lines <- fmt.Sprintf(f, a...) })
	}()
	defer func() { w.Close(); <-done }()
	for _, line := range []string{"first dispatch done", "second dispatch started"} {
		if _, err := io.WriteString(w, line+"\n"); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-lines:
			if got != "[worker pid=42] "+line {
				t.Fatalf("unexpected log %q", got)
			}
		case <-time.After(time.Second):
			t.Fatal("worker output withheld while worker pipe remains open")
		}
	}
}

func TestWorkerOutputLongLineAndFinalPartial(t *testing.T) {
	long := strings.Repeat("x", 256*1024)
	var parts []string
	forwardWorkerOutput(io.NopCloser(strings.NewReader(long+"\nlast partial")), 7, func(f string, a ...any) {
		line := fmt.Sprintf(f, a...)
		if !strings.HasPrefix(line, "[worker pid=7] ") {
			t.Fatalf("missing worker identity: %q", line[:20])
		}
		parts = append(parts, strings.TrimPrefix(line, "[worker pid=7] "))
	})
	if got := strings.Join(parts, ""); got != long+"last partial" {
		t.Fatalf("lost or altered output: got %d chars", len(got))
	}
	if len(parts) < 3 {
		t.Fatal("long line not drained in bounded chunks")
	}
}

type workerOutputErrorReader struct {
	closed bool
	reads  int
}

func (r *workerOutputErrorReader) Read(p []byte) (int, error) {
	r.reads++
	return copy(p, "last diagnostic"), errors.New("test read failure")
}
func (r *workerOutputErrorReader) Close() error { r.closed = true; return nil }

func TestWorkerOutputReadErrorFlushesAndCloses(t *testing.T) {
	r := &workerOutputErrorReader{}
	var lines []string
	forwardWorkerOutput(r, 9, func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) })
	if !r.closed || r.reads != 1 {
		t.Fatalf("reader lifecycle: %+v", r)
	}
	if len(lines) != 2 || lines[0] != "[worker pid=9] last diagnostic" || !strings.Contains(lines[1], "output read failed: test read failure") {
		t.Fatalf("lost diagnostic or error: %v", lines)
	}
}

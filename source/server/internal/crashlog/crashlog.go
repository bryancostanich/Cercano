// Package crashlog captures process-level agent crashes to a persistent
// newline-delimited-JSON file so operators can debug why the server died
// after the fact.
//
// The gRPC-handler recovery interceptors (see server/recovery.go) already
// catch panics inside RPC calls so one bad handler can't take down the
// singleton. This package handles the classes those interceptors don't:
//
//   - Panics in background goroutines the server spawns (agent loops,
//     compactors, watchers, download workers).
//   - Fatal signals delivered to the process (SIGTERM from a shutdown
//     script, SIGSEGV from a runtime bug, SIGKILL by the OS OOM killer —
//     though SIGKILL can't be caught; we log everything up to the death).
//   - Panics in top-level goroutines the recovery interceptors don't wrap.
//
// The default location is ~/.config/cercano/crash.log — persistent across
// reboots (unlike $TMPDIR) and colocated with the rest of Cercano's
// per-user state (config, telemetry, permissions). One line per crash so
// the file plays nicely with `grep`, `jq`, and a future
// `cercano logs --crashes` subcommand.
package crashlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

// Kind classifies a crash so downstream tools (CLI reader, dashboards)
// can filter or colour differently.
type Kind string

const (
	// KindPanic is a Go panic caught by a deferred recover somewhere in
	// the server. Includes reason (recover() value formatted as string)
	// and stack trace.
	KindPanic Kind = "panic"
	// KindGoroutinePanic is a panic from a background goroutine (not the
	// main RPC-handler flow). Same fields as KindPanic; the separate
	// kind exists so operators can quickly see whether the crash was
	// user-initiated (via an RPC) or internal.
	KindGoroutinePanic Kind = "goroutine_panic"
	// KindSignal is a fatal-signal handler firing. Reason carries the
	// signal name (e.g. "SIGTERM"). Stack is a full goroutine dump —
	// useful for OOM-killer investigations.
	KindSignal Kind = "signal"
)

// Entry is one crash record. Serialized to JSON as one line in the
// crash log. All timestamps are RFC3339 in UTC so log correlation
// across timezones is unambiguous.
//
// Extra is a bag of free-form metadata callers can attach — active
// conversation ids, recent config changes, etc. — without needing to
// grow this struct for every use case.
type Entry struct {
	Timestamp      time.Time      `json:"ts"`
	Kind           Kind           `json:"kind"`
	Reason         string         `json:"reason,omitempty"`
	Signal         string         `json:"signal,omitempty"`
	Stack          string         `json:"stack,omitempty"`
	CercanoVersion string         `json:"cercano_version,omitempty"`
	Uptime         time.Duration  `json:"uptime_seconds,omitempty"`
	Extra          map[string]any `json:"extra,omitempty"`
}

// Writer appends crash entries to a file. All operations are safe for
// concurrent use — a panicking goroutine and a signal handler may race
// to write, and both should succeed without corrupting the file.
type Writer struct {
	mu             sync.Mutex
	path           string
	f              *os.File
	startedAt      time.Time
	cercanoVersion string
}

// NewWriter opens (or creates + appends) the crash log at path. Parent
// directories are created if missing. cercanoVersion is stamped on
// every Entry so operators can tell whether a crash predates a fix.
func NewWriter(path, cercanoVersion string) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("crashlog: mkdir %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("crashlog: open %s: %w", path, err)
	}
	return &Writer{
		path:           path,
		f:              f,
		startedAt:      time.Now().UTC(),
		cercanoVersion: cercanoVersion,
	}, nil
}

// Path returns the file path being written to. Used by CLI subcommands
// that want to tell users where to look ("see logs at PATH").
func (w *Writer) Path() string {
	if w == nil {
		return ""
	}
	return w.path
}

// Close flushes and closes the log file. Safe to call multiple times.
func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// LogPanic records a panic caught by a deferred recover. reason is the
// stringified panic value; stack is the trace (typically debug.Stack()).
// extra may be nil.
//
// The panic is only logged, not re-raised — callers are expected to
// decide whether to re-panic (typical: yes, so the process still dies
// after logging) or continue (background workers that should keep
// running after a bad iteration).
func (w *Writer) LogPanic(reason string, stack []byte, extra map[string]any) {
	w.write(Entry{
		Kind:   KindPanic,
		Reason: reason,
		Stack:  string(stack),
		Extra:  extra,
	})
}

// LogGoroutinePanic is like LogPanic but tags the entry as a background
// goroutine crash. Behavior is identical.
func (w *Writer) LogGoroutinePanic(reason string, stack []byte, extra map[string]any) {
	w.write(Entry{
		Kind:   KindGoroutinePanic,
		Reason: reason,
		Stack:  string(stack),
		Extra:  extra,
	})
}

// LogSignal records a fatal-signal delivery. signal is the string name
// (e.g. "SIGTERM"). The stack of every live goroutine is captured so
// OOM-killer or hung-goroutine investigations have something concrete
// to work from.
func (w *Writer) LogSignal(signal string, extra map[string]any) {
	buf := make([]byte, 1<<20) // 1 MiB should hold most goroutine dumps
	n := runtime.Stack(buf, true)
	w.write(Entry{
		Kind:   KindSignal,
		Signal: signal,
		Stack:  string(buf[:n]),
		Extra:  extra,
	})
}

// RecoverAndLog is a convenience for the standard defer pattern in
// background goroutines:
//
//	go func() {
//	    defer crashlog.RecoverAndLog(writer, "compaction worker", nil)
//	    ...
//	}()
//
// It calls recover(), logs if there was a panic, and does NOT re-panic
// (the goroutine is expected to die cleanly and the parent supervisor,
// if any, handles restart policy).
func RecoverAndLog(w *Writer, description string, extra map[string]any) {
	r := recover()
	if r == nil {
		return
	}
	extraCopy := map[string]any{"description": description}
	for k, v := range extra {
		extraCopy[k] = v
	}
	w.LogGoroutinePanic(fmt.Sprintf("%v", r), debug.Stack(), extraCopy)
}

// write is the shared serialization + flush path. Holds the mutex so
// concurrent panic + signal-handler writers can't interleave partial
// JSON lines.
func (w *Writer) write(e Entry) {
	if w == nil || w.f == nil {
		return
	}
	e.Timestamp = time.Now().UTC()
	e.CercanoVersion = w.cercanoVersion
	e.Uptime = time.Since(w.startedAt)
	line, err := json.Marshal(e)
	if err != nil {
		// A marshal failure here is either an internal bug or a
		// caller passing something un-marshalable in Extra. Write a
		// minimal fallback entry so we still record that SOMETHING
		// happened.
		fallback := Entry{
			Timestamp:      e.Timestamp,
			Kind:           e.Kind,
			Reason:         fmt.Sprintf("crashlog marshal error: %v (original reason: %s)", err, e.Reason),
			CercanoVersion: e.CercanoVersion,
			Uptime:         e.Uptime,
		}
		if fbLine, fbErr := json.Marshal(fallback); fbErr == nil {
			line = fbLine
		} else {
			return // give up
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return
	}
	_, _ = w.f.Write(line)
	_, _ = w.f.Write([]byte("\n"))
	_ = w.f.Sync() // flush to disk — crashes often follow within milliseconds
}

package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// This opt-in local trace never records queries, key values, message text, or
// conversation identifiers. File I/O happens only in the bounded writer queue.
type searchTraceContext struct {
	Session    uint64 `json:"session"`
	Revision   uint64 `json:"revision"`
	QueryBytes int    `json:"query_bytes"`
	Entries    int    `json:"entries"`
	Lines      int    `json:"lines"`
}
type searchTraceDetails struct {
	Count       int    `json:"count,omitempty"`
	Matches     int    `json:"matches,omitempty"`
	CacheHits   int    `json:"cache_hits,omitempty"`
	Projected   int    `json:"projected,omitempty"`
	SourceBytes int    `json:"source_bytes,omitempty"`
	Status      string `json:"status,omitempty"`
}
type searchTraceEvent struct {
	searchTraceContext
	searchTraceDetails
	Time      time.Time `json:"time"`
	PID       int       `json:"pid"`
	Stage     string    `json:"stage"`
	Span      uint64    `json:"span,omitempty"`
	ElapsedMS float64   `json:"elapsed_ms"`
	Dropped   uint64    `json:"dropped_total"`
	path      string
}

var searchTraceIDs atomic.Uint64
var searchTraceOnce sync.Once
var searchTraceQueue chan searchTraceEvent
var searchTraceDropped atomic.Uint64

func searchTraceEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CERCANO_SEARCH_TRACE"))) {
	case "1", "true", "on":
		return true
	}
	return false
}
func (s *conversationSearch) traceContext() searchTraceContext {
	if s == nil || !searchTraceEnabled() {
		return searchTraceContext{}
	}
	fresh := s.traceID == 0
	if fresh {
		s.traceID = searchTraceIDs.Add(1)
	}
	c := searchTraceContext{Session: s.traceID, Revision: s.revision, QueryBytes: len(s.input.Value())}
	if s.chat != nil {
		c.Entries = len(s.chat.entries)
		c.Lines = s.chat.TotalLineCount()
	}
	if fresh {
		c.record("session.open", 0, searchTraceDetails{Status: searchTraceBuild()})
	}
	return c
}
func searchTracePath() string {
	if path := os.Getenv("CERCANO_SEARCH_TRACE_LOG"); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "cercano-search-perf.jsonl"
	}
	return filepath.Join(home, ".config", "cercano", "search-perf.jsonl")
}
func enqueueSearchTrace(queue chan<- searchTraceEvent, event searchTraceEvent, dropped *atomic.Uint64) {
	event.Dropped = dropped.Load()
	select {
	case queue <- event:
	default:
		dropped.Add(1)
	}
}
func (c searchTraceContext) record(stage string, elapsed time.Duration, details searchTraceDetails) {
	c.emit(stage, elapsed, 0, details)
}
func (c searchTraceContext) emit(stage string, elapsed time.Duration, span uint64, details searchTraceDetails) {
	if c.Session == 0 {
		return
	}
	searchTraceOnce.Do(func() {
		searchTraceQueue = make(chan searchTraceEvent, 1024)
		go func() {
			for event := range searchTraceQueue {
				_ = writeSearchTrace(event)
			}
		}()
	})
	enqueueSearchTrace(searchTraceQueue, searchTraceEvent{searchTraceContext: c, searchTraceDetails: details, Time: time.Now(), PID: os.Getpid(), Stage: stage, Span: span, ElapsedMS: float64(elapsed) / float64(time.Millisecond), path: searchTracePath()}, &searchTraceDropped)
}
func (c searchTraceContext) span(stage string) func() {
	if c.Session == 0 {
		return func() {}
	}
	id := searchTraceIDs.Add(1)
	start := time.Now()
	c.emit(stage+".begin", 0, id, searchTraceDetails{})
	return func() { c.emit(stage+".end", time.Since(start), id, searchTraceDetails{}) }
}
func writeSearchTrace(event searchTraceEvent) error {
	if err := os.MkdirAll(filepath.Dir(event.path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(event.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}
func searchTraceBuild() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	revision, modified := "unknown", "unknown"
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	return "revision=" + revision + " modified=" + modified
}

package llm

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"time"
)

// RecordAnomaly appends a structured turn/stream anomaly record — a resume-replay
// or fabricated-turn fingerprint — to the anomaly log, so occurrences across every
// conversation are captured with their content and conversation id: a reviewable
// trail instead of ad-hoc, per-thread incident hunting. The log path is
// ~/.config/cercano/stream-anomalies.jsonl, overridable via CERCANO_ANOMALY_LOG
// (tests, ops). Best-effort; every failure is swallowed so it never affects the
// caller or the stream.
func RecordAnomaly(conversationID, reason, content string) {
	path := os.Getenv("CERCANO_ANOMALY_LOG")
	if path == "" {
		// A unit test with no explicit override must never write to the real
		// user anomaly log (test.v is registered only in test binaries).
		if flag.Lookup("test.v") != nil {
			return
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		path = filepath.Join(home, ".config", "cercano", "stream-anomalies.jsonl")
	}
	rec := map[string]any{
		"ts":           time.Now().Unix(),
		"conversation": conversationID,
		"reason":       reason,
		"bytes":        len(content),
		"content":      content,
	}
	blob, err := json.Marshal(rec)
	if err != nil {
		return
	}
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer fh.Close()
	_, _ = fh.Write(append(blob, '\n'))
}

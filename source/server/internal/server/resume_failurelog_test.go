package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/server/internal/failurelog"
	"cercano/source/server/pkg/proto"
)

func TestLogResumeRPCFailureRecordsDBDiagnostics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failures.jsonl")
	w, err := failurelog.NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	s := &Server{failureLog: w}
	s.logResumeRPCFailure(
		"list_conversations",
		&proto.ListConversationsRequest{ProjectDir: "/repo", Limit: 100},
		nil,
		time.Now().Add(-25*time.Millisecond),
		errors.New("database is locked"),
		"db_failure",
	)

	event := readOneFailureEvent(t, path)
	if event["event"] != "resume.rpc_failure" {
		t.Fatalf("event = %v, want resume.rpc_failure", event["event"])
	}
	if event["scope"] != "resume_page" {
		t.Fatalf("scope = %v, want resume_page", event["scope"])
	}
	if event["operation"] != "list_conversations" {
		t.Fatalf("operation = %v", event["operation"])
	}
	if event["reason"] != "db_failure" {
		t.Fatalf("reason = %v", event["reason"])
	}
	if event["error_kind"] != "sqlite_locked" {
		t.Fatalf("error_kind = %v, want sqlite_locked", event["error_kind"])
	}
	if event["project_dir"] != "/repo" {
		t.Fatalf("project_dir = %v", event["project_dir"])
	}
	if event["message"] != "database is locked" {
		t.Fatalf("message = %v", event["message"])
	}
	if _, ok := event["pid"].(float64); !ok {
		t.Fatalf("pid missing or non-numeric: %#v", event["pid"])
	}
	if _, ok := event["duration_ms"].(float64); !ok {
		t.Fatalf("duration_ms missing or non-numeric: %#v", event["duration_ms"])
	}
}

func TestLogResumeRPCFailureRecordsConversationIDAndTimeoutKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failures.jsonl")
	w, err := failurelog.NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	s := &Server{failureLog: w}
	s.logResumeRPCFailure(
		"resume_conversation",
		nil,
		&proto.ResumeConversationRequest{ConversationId: "c123"},
		time.Now(),
		context.DeadlineExceeded,
		"db_failure",
	)

	event := readOneFailureEvent(t, path)
	if event["conversation_id"] != "c123" {
		t.Fatalf("conversation_id = %v", event["conversation_id"])
	}
	if event["error_kind"] != "deadline_exceeded" {
		t.Fatalf("error_kind = %v, want deadline_exceeded", event["error_kind"])
	}
}

func readOneFailureEvent(t *testing.T, path string) map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	if !s.Scan() {
		t.Fatalf("no failure log event written: %v", s.Err())
	}
	var event map[string]any
	if err := json.Unmarshal(s.Bytes(), &event); err != nil {
		t.Fatalf("unmarshal failure event: %v", err)
	}
	return event
}

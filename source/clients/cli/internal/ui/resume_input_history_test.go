package ui

import (
	"reflect"
	"testing"
	"time"

	"cercano/source/server/pkg/agentclient"
)

func turn(role, content string) agentclient.PersistedTurn {
	return agentclient.PersistedTurn{Role: role, Content: content, CreatedAt: time.Unix(0, 0)}
}

// resumeInputHistory must return only user prompts, oldest-first, so ↑ recall
// works right after a resume. This is the regression guard for "prompt replay
// doesn't work on a resumed session".
func TestResumeInputHistory_UserPromptsOldestFirst(t *testing.T) {
	turns := []agentclient.PersistedTurn{
		turn("user", "first"),
		turn("assistant", "reply one"),
		turn("user", "second"),
		turn("assistant", "reply two"),
		turn("user", "/model gpt-5"),
	}
	got := resumeInputHistory(turns)
	want := []string{"first", "second", "/model gpt-5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resumeInputHistory = %v, want %v", got, want)
	}
}

func TestResumeInputHistory_SkipsEmptyAndDedupesConsecutive(t *testing.T) {
	turns := []agentclient.PersistedTurn{
		turn("user", "keep"),
		turn("user", "keep"),  // consecutive dup dropped
		turn("user", "   "),   // whitespace-only dropped
		turn("user", ""),      // empty (tool_result) dropped
		turn("assistant", ""), // non-user dropped
		// This "keep" is separated from the first only by dropped
		// (non-prompt) turns, so it is still a consecutive submitted
		// prompt and is deduped away — mirroring recordSubmittedInput.
		turn("user", "keep"),
		turn("user", "other"), // distinct prompt is kept
	}
	got := resumeInputHistory(turns)
	want := []string{"keep", "other"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resumeInputHistory = %v, want %v", got, want)
	}
}

func TestResumeInputHistory_NormalizesCRLF(t *testing.T) {
	got := resumeInputHistory([]agentclient.PersistedTurn{turn("user", "a\r\nb\rc")})
	want := []string{"a\nb\nc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resumeInputHistory = %v, want %v", got, want)
	}
}

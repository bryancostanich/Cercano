package compaction

import (
	"testing"

	"cercano/source/server/internal/llm"
)

func TestParseSummary_WellFormed(t *testing.T) {
	in := `GOAL: ship compaction
DECISIONS:
- use structured summaries
- elide superseded reads
FILES:
- a.go: finalized
- b.go: added
OPEN:
- wire 2b trigger
STATE: bake-off ready`
	s := ParseSummary(in)
	if s.Goal != "ship compaction" {
		t.Errorf("Goal = %q", s.Goal)
	}
	if len(s.Decisions) != 2 || s.Decisions[0] != "use structured summaries" {
		t.Errorf("Decisions = %v", s.Decisions)
	}
	if s.Files["a.go"] != "finalized" || s.Files["b.go"] != "added" {
		t.Errorf("Files = %v", s.Files)
	}
	if len(s.OpenThreads) != 1 || s.OpenThreads[0] != "wire 2b trigger" {
		t.Errorf("OpenThreads = %v", s.OpenThreads)
	}
	if s.State != "bake-off ready" {
		t.Errorf("State = %q", s.State)
	}
}

func TestParseSummary_MissingSectionsAndPreamble(t *testing.T) {
	in := `Sure! Here is the summary you asked for:

GOAL: fix the pager bug
STATE: fixed`
	s := ParseSummary(in)
	if s.Goal != "fix the pager bug" {
		t.Errorf("Goal = %q (preamble should be ignored)", s.Goal)
	}
	if s.State != "fixed" {
		t.Errorf("State = %q", s.State)
	}
	if len(s.Decisions) != 0 || len(s.Files) != 0 || len(s.OpenThreads) != 0 {
		t.Errorf("absent sections must be empty: %+v", s)
	}
}

func TestParseSummary_GarbageIsEmpty(t *testing.T) {
	s := ParseSummary("the model rambled with no sections at all")
	if s.Goal != "" || s.State != "" || len(s.Decisions) != 0 {
		t.Errorf("garbage must parse to empty summary, got %+v", s)
	}
}

func TestSplitRecent(t *testing.T) {
	msgs := []llm.Message{
		textMsg(llm.RoleUser, "1"), textMsg(llm.RoleAssistant, "2"), textMsg(llm.RoleUser, "3"),
	}
	older, recent := splitRecent(msgs, 1)
	if len(older) != 2 || len(recent) != 1 || recent[0].Blocks[0].Text != "3" {
		t.Errorf("splitRecent(1): older=%d recent=%d", len(older), len(recent))
	}
	older, recent = splitRecent(msgs, 10)
	if len(older) != 0 || len(recent) != 3 {
		t.Errorf("splitRecent(>len) should be all recent")
	}
}

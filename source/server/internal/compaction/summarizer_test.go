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

func TestParseSummary_LenientBulletsAndMalformed(t *testing.T) {
	in := `GOAL:
DECISIONS:
1. numbered decision
2) paren decision
* star decision
FILES:
- a.go: kept
- malformed line with no colon
- 1.txt: numeric leading name
OPEN:
- a thread`
	s := ParseSummary(in)
	if s.Goal != "" {
		t.Errorf("empty GOAL value should parse to empty, got %q", s.Goal)
	}
	if len(s.Decisions) != 3 ||
		s.Decisions[0] != "numbered decision" ||
		s.Decisions[1] != "paren decision" ||
		s.Decisions[2] != "star decision" {
		t.Errorf("numbered/paren/star bullets not stripped: %v", s.Decisions)
	}
	if s.Files["a.go"] != "kept" {
		t.Errorf("well-formed FILES entry lost: %v", s.Files)
	}
	if _, bad := s.Files["malformed line with no colon"]; bad {
		t.Error("a FILES line with no colon should be dropped, not keyed by the whole line")
	}
	if s.Files["1.txt"] != "numeric leading name" {
		t.Errorf("numeric-leading filename mangled by bullet stripping: %v", s.Files)
	}
	if len(s.OpenThreads) != 1 || s.OpenThreads[0] != "a thread" {
		t.Errorf("OpenThreads = %v", s.OpenThreads)
	}
}

func TestParseSummary_BulletBeforeAnyLabelIgnored(t *testing.T) {
	in := `- this bullet precedes every section label
GOAL: real goal`
	s := ParseSummary(in)
	if s.Goal != "real goal" {
		t.Errorf("Goal = %q", s.Goal)
	}
	if len(s.Decisions) != 0 || len(s.OpenThreads) != 0 {
		t.Errorf("a bullet before any section must be ignored: %+v", s)
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

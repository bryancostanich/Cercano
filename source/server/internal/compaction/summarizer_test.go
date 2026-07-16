package compaction

import (
	"strings"
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

func TestParseSummary_Proposals(t *testing.T) {
	// PROPOSALS is its own section — proposals must not be dropped or promoted
	// to DECISIONS. Regression pin for the failure that lost the models×tiers
	// design from conversation 80109e871fba4e18.
	in := `GOAL: design model routing
DECISIONS:
- keep Reduce deterministic
PROPOSALS:
- 3 tiers: most_capable, everyday, fast_light
- models.Resolve(tier, preferredProvider) → (modelID, provider, ok)
STATE: awaiting user approval on tier names`
	s := ParseSummary(in)
	if len(s.Decisions) != 1 || s.Decisions[0] != "keep Reduce deterministic" {
		t.Errorf("Decisions bled into Proposals or vice versa: %v", s.Decisions)
	}
	if len(s.Proposals) != 2 {
		t.Fatalf("Proposals count = %d, want 2: %v", len(s.Proposals), s.Proposals)
	}
	if s.Proposals[0] != "3 tiers: most_capable, everyday, fast_light" {
		t.Errorf("Proposal[0] identifiers not preserved verbatim: %q", s.Proposals[0])
	}
	if s.Proposals[1] != "models.Resolve(tier, preferredProvider) → (modelID, provider, ok)" {
		t.Errorf("Proposal[1] signature not preserved verbatim: %q", s.Proposals[1])
	}
}

func TestBuildSummaryPrompt_ContractInvariants(t *testing.T) {
	// The prompt has to teach the model three things or the summarizer
	// silently drops load-bearing content. Test each requirement so the
	// fix can't regress via a future prompt tweak.
	body := BuildSummaryPrompt([]llm.Message{textMsg(llm.RoleUser, "hi")})

	cases := []struct {
		want string
		why  string
	}{
		{"PROPOSALS:", "PROPOSALS section must be present so unconfirmed designs have a slot"},
		{"verbatim", "prompt must instruct the model to preserve config/code/identifiers verbatim"},
		{"unique", "prompt must instruct the model to deduplicate bullets within a section"},
		{"A DECISION is confirmed", "prompt must distinguish confirmed decisions from proposals"},
	}
	for _, c := range cases {
		if !strings.Contains(body, c.want) {
			t.Errorf("BuildSummaryPrompt missing %q — %s", c.want, c.why)
		}
	}
}

func TestBuildSummaryPrompt_InstructionAfterTranscript(t *testing.T) {
	// With the instructions only at the top, a model reading ~8k tokens of
	// agent transcript pattern-completes the conversation instead of
	// summarizing it (observed live: the summarizer emitted "Perfect. Now
	// I'll update the doc…[tool Write {…}]" — an assistant turn, not a
	// summary — deterministically at temperature 0). The transcript must be
	// explicitly closed and the task restated AFTER it, so the last thing
	// the model reads is the instruction, not the conversation.
	body := BuildSummaryPrompt([]llm.Message{textMsg(llm.RoleUser, "let's rename the config key")})

	end := strings.Index(body, "--- end conversation ---")
	if end < 0 {
		t.Fatal("prompt must close the transcript with an explicit end marker")
	}
	tail := body[end:]
	if !strings.Contains(tail, "summar") {
		t.Errorf("the task must be restated after the transcript; tail: %q", tail)
	}
	if !strings.Contains(tail, "Do not continue the conversation") {
		t.Errorf("the tail must forbid continuing the conversation; tail: %q", tail)
	}
	if strings.Contains(tail, "rename the config key") {
		t.Error("transcript content must not appear after the end marker")
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

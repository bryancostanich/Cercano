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

func TestParseSummary_Findings(t *testing.T) {
	// FINDINGS section should be parsed and stored as historical observations
	in := `GOAL: investigate performance issue
DECISIONS:
- use structured summaries
FINDINGS:
- observed 50% latency increase under load
- database connection pool exhausted
- cache miss rate 85%
STATE: investigation ongoing`
	s := ParseSummary(in)
	if len(s.Findings) != 3 {
		t.Fatalf("Findings count = %d, want 3: %v", len(s.Findings), s.Findings)
	}
	if s.Findings[0] != "observed 50% latency increase under load" {
		t.Errorf("Finding[0] = %q, want exact preservation", s.Findings[0])
	}
	if s.Findings[1] != "database connection pool exhausted" {
		t.Errorf("Finding[1] = %q, want exact preservation", s.Findings[1])
	}
	if s.Findings[2] != "cache miss rate 85%" {
		t.Errorf("Finding[2] = %q, want exact preservation", s.Findings[2])
	}
}

func TestParseSummary_JSONLegacyCompatibility(t *testing.T) {
	// JSON serialization should work with legacy summaries that lack Findings
	// (zero value for Findings should serialize as null or omit field)
	_ = StructuredSummary{
		Goal:        "test goal",
		Decisions:   []string{"decision 1"},
		Proposals:   []string{"proposal 1"},
		Files:       map[string]string{"file1": "state1"},
		OpenThreads: []string{"thread 1"},
		State:       "test state",
		Findings:    nil, // legacy compatibility
	}

	_ = StructuredSummary{
		Goal:        "test goal",
		Decisions:   []string{"decision 1"},
		Proposals:   []string{"proposal 1"},
		Files:       map[string]string{"file1": "state1"},
		OpenThreads: []string{"thread 1"},
		State:       "test state",
		Findings:    []string{"finding 1"}, // new field present
	}

	// Both should parse correctly from text format
	text1 := `GOAL: test goal
DECISIONS:
- decision 1
PROPOSALS:
- proposal 1
FILES:
- file1: state1
OPEN:
- thread 1
STATE: test state`

	text2 := `GOAL: test goal
DECISIONS:
- decision 1
PROPOSALS:
- proposal 1
FILES:
- file1: state1
OPEN:
- thread 1
STATE: test state
FINDINGS:
- finding 1`

	parsed1 := ParseSummary(text1)
	parsed2 := ParseSummary(text2)

	if len(parsed1.Findings) != 0 {
		t.Errorf("Legacy summary should have empty Findings, got %v", parsed1.Findings)
	}
	if len(parsed2.Findings) != 1 || parsed2.Findings[0] != "finding 1" {
		t.Errorf("New summary should preserve Findings, got %v", parsed2.Findings)
	}
}

func TestMergeSummaries_FindingsDeduplication(t *testing.T) {
	// MergeSummaries should deduplicate findings while preserving order
	// and keeping distinct observations from the same file
	s1 := StructuredSummary{
		Findings: []string{
			"file.go: syntax error on line 10",
			"config.yaml: missing required field",
			"file.go: type mismatch in function signature",
		},
	}

	s2 := StructuredSummary{
		Findings: []string{
			"config.yaml: missing required field", // duplicate
			"database: connection timeout",
			"file.go: syntax error on line 10", // duplicate
		},
	}

	merged := MergeSummaries([]StructuredSummary{s1, s2})

	if len(merged.Findings) != 4 {
		t.Fatalf("Merged Findings count = %d, want 4: %v", len(merged.Findings), merged.Findings)
	}

	expected := []string{
		"file.go: syntax error on line 10",
		"config.yaml: missing required field",
		"file.go: type mismatch in function signature",
		"database: connection timeout",
	}

	for i, want := range expected {
		if merged.Findings[i] != want {
			t.Errorf("Findings[%d] = %q, want %q", i, merged.Findings[i], want)
		}
	}
}

func TestMergeSummaries_SameFileDistinctObservations(t *testing.T) {
	// Different observations about the same file should be preserved
	s1 := StructuredSummary{
		Findings: []string{
			"main.go: imports missing",
			"main.go: function signature incorrect",
		},
	}

	s2 := StructuredSummary{
		Findings: []string{
			"main.go: variable naming issue",
			"test.go: assertion failure",
		},
	}

	merged := MergeSummaries([]StructuredSummary{s1, s2})

	if len(merged.Findings) != 4 {
		t.Fatalf("Merged Findings count = %d, want 4: %v", len(merged.Findings), merged.Findings)
	}

	// Check that all distinct observations are preserved
	mergedSet := make(map[string]bool)
	for _, f := range merged.Findings {
		mergedSet[f] = true
	}

	expected := []string{
		"main.go: imports missing",
		"main.go: function signature incorrect",
		"main.go: variable naming issue",
		"test.go: assertion failure",
	}

	for _, exp := range expected {
		if !mergedSet[exp] {
			t.Errorf("Expected finding %q not found in merged result", exp)
		}
	}
}

func TestParseSummary_FindingsOnly(t *testing.T) {
	// Summary with only FINDINGS section should be parseable
	in := `FINDINGS:
- observed intermittent failures
- performance degradation under load
- memory leak suspected`
	s := ParseSummary(in)
	if len(s.Findings) != 3 {
		t.Fatalf("Findings count = %d, want 3: %v", len(s.Findings), s.Findings)
	}
	if s.Findings[0] != "observed intermittent failures" {
		t.Errorf("Finding[0] = %q, want exact preservation", s.Findings[0])
	}
}

func TestStructuredSummary_FindingsOnlyIsNotEmpty(t *testing.T) {
	// Summary with only Findings should not be empty
	s := StructuredSummary{
		Findings: []string{"some observation"},
	}
	if s.IsEmpty() {
		t.Error("Summary with Findings should not be empty")
	}
}

func TestStructuredSummary_FindingsDedupKeyPreservesOriginal(t *testing.T) {
	// Test that findings with different casing/punctuation are deduplicated
	// but the original first occurrence is preserved
	s1 := StructuredSummary{
		Findings: []string{
			"error: file not found",
			"Error: File Not Found", // should be deduplicated (same content)
			"warning: low memory",
		},
	}

	s2 := StructuredSummary{
		Findings: []string{
			"warning: low memory",   // duplicate, should be ignored
			"info: process started", // different content, should be preserved
		},
	}

	merged := MergeSummaries([]StructuredSummary{s1, s2})

	if len(merged.Findings) != 3 {
		t.Fatalf("Merged Findings count = %d, want 3: %v", len(merged.Findings), merged.Findings)
	}

	// Check that original casing is preserved and duplicates removed
	expected := []string{
		"error: file not found", // first occurrence preserved
		"warning: low memory",   // first occurrence preserved
		"info: process started",
	}

	for i, want := range expected {
		if merged.Findings[i] != want {
			t.Errorf("Findings[%d] = %q, want original preserved %q", i, merged.Findings[i], want)
		}
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

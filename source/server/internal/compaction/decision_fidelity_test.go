package compaction

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestSummaryDecisionFidelityContract(t *testing.T) {
	p := BuildSummaryPrompt([]llm.Message{textMsg(llm.RoleAssistant, "I propose changing the architecture.")})
	for _, rule := range []string{"[instruction]", "[approved]", "[proposed]", "[verified]", "[attempted]", "[failed]", "[unverified]", "[superseded]", "[rejected]", "source references", "not user approval", "prior summary", "Do not invent"} {
		if !strings.Contains(p, rule) {
			t.Errorf("missing fidelity rule %q", rule)
		}
	}
	if strings.Contains(p, "confirmed or applied") {
		t.Error("implementation still counts as approval")
	}
}

func TestSummaryTranscriptPreservesToolEvidence(t *testing.T) {
	p := BuildSummaryPrompt([]llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "write-1", ToolName: "Write", ToolInput: []byte(`{"path":"x"}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "write-1", IsError: true, Content: "permission denied"}}},
	})
	for _, want := range []string{"source sha256:", "call=write-1", "is_error=true", "permission denied"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing source evidence %q", want)
		}
	}
}

func sourceRef(t *testing.T, m llm.Message) string {
	t.Helper()
	var b strings.Builder
	writeFidelityTranscript(&b, []llm.Message{m})
	ref := regexp.MustCompile(`source sha256:[0-9a-f]{32}`).FindString(b.String())
	if ref == "" {
		t.Fatal("missing content reference")
	}
	return ref
}

func TestSummarySourceReferencesStableAndEvidenceSensitive(t *testing.T) {
	m := textMsg(llm.RoleUser, "Approve only the bounded probe; no downloads.")
	ref := sourceRef(t, m)
	for _, messages := range [][]llm.Message{{m}, {textMsg(llm.RoleAssistant, "proposal"), m}, {m, textMsg(llm.RoleUser, "next")}} {
		if !strings.Contains(BuildSummaryPrompt(messages), ref) {
			t.Fatal("reference depends on span position")
		}
	}
	other := m
	other.Role = llm.RoleAssistant
	if sourceRef(t, other) == ref {
		t.Fatal("attribution excluded from reference")
	}
	result := llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "test-1", Content: "test output"}}}
	before := sourceRef(t, result)
	result.Blocks[0].IsError = true
	if sourceRef(t, result) == before {
		t.Fatal("failure status excluded from reference")
	}
	result.Blocks[0].IsError = false
	result.Blocks[0].ToolUseRef = "test-2"
	if sourceRef(t, result) == before {
		t.Fatal("call identity excluded from reference")
	}
}

// This is a scripted pipeline test, NOT a claim that a live model makes these
// classifications correctly. It checks that evidence, qualifiers and original
// references reach successive prompts and survive parse/merge/render unchanged.
func TestSummaryFidelityAcrossRepeatedCompactions(t *testing.T) {
	sources := []llm.Message{
		textMsg(llm.RoleUser, "Only phase one. No downloads. Return blockers before architecture changes."),
		textMsg(llm.RoleAssistant, "I propose a full rewrite."),
		textMsg(llm.RoleUser, "Reject the rewrite. Approve only the bounded adapter change."),
		textMsg(llm.RoleAssistant, "All tests pass; it is deployed."),
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "tests-1", ToolName: "RunCommand", ToolInput: []byte(`{"cmd":["go","test","./..."]}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "tests-1", IsError: true, Content: "FAIL adapter test"}}},
	}
	refs := make([]string, len(sources))
	for i, m := range sources {
		refs[i] = sourceRef(t, m)
	}
	wire := fmt.Sprintf(`GOAL: Bounded adapter change only
DECISIONS:
- [instruction] Only phase one; no downloads; report architecture blockers (%s)
- [approved] Bounded adapter change only (%s)
PROPOSALS:
FILES:
- adapter.go: [failed] tests-1: FAIL adapter test (%s)
OPEN:
- [rejected] Full rewrite; bounded adapter is the alternative (%s; %s)
- [unverified] Assistant claimed deployment; no deployment evidence (%s)
STATE: [attempted] tests-1 ran and failed, not verified or deployed (%s; %s)
`, refs[0], refs[2], refs[5], refs[1], refs[2], refs[3], refs[4], refs[5])
	summary := ParseSummary(wire)
	for pass := 0; pass < 3; pass++ {
		input := sources
		if pass > 0 {
			input = renderSummaryMessages(summary)
		}
		correction := textMsg(llm.RoleUser, "Correction: stop implementation; inspect only. The bounded adapter approval is superseded.")
		interruption := textMsg(llm.RoleAssistant, "I propose another probe, but the command was interrupted; no result is available.")
		if pass > 0 {
			input = append(input, correction, interruption)
		}
		nextWire := wire
		if pass > 0 {
			nextWire = fmt.Sprintf(`GOAL: Inspect only
DECISIONS:
- [instruction] Stop implementation; inspect only (%s)
PROPOSALS:
- [proposed] Another probe, not approved (%s)
FILES:
- adapter.go: [failed] tests-1: FAIL adapter test (%s)
OPEN:
- [superseded] Bounded adapter approval replaced by inspect-only (%s; %s)
- [rejected] Full rewrite (%s; %s)
- [unverified] Deployment claim has no supporting result (%s)
- [attempted] Probe interrupted; missing result (%s)
STATE: [unverified] No successful verification or deployment established
`, sourceRef(t, correction), sourceRef(t, interruption), refs[5], refs[2], sourceRef(t, correction), refs[1], refs[2], refs[3], sourceRef(t, interruption))
		}
		if pass == 2 {
			check := llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "tests-2", ToolName: "RunCommand", ToolInput: []byte(`{"cmd":["go","test","-run","TestAdapter"]}`)}}}
			passed := llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "tests-2", Content: "PASS TestAdapter only"}}}
			input = append(input, check, passed)
			nextWire = strings.Replace(nextWire,
				fmt.Sprintf("adapter.go: [failed] tests-1: FAIL adapter test (%s)", refs[5]),
				fmt.Sprintf("adapter.go: [verified] tests-2: TestAdapter only passed, not deployment (%s; %s)", sourceRef(t, check), sourceRef(t, passed)), 1)
			nextWire = strings.Replace(nextWire, "OPEN:\n", fmt.Sprintf("OPEN:\n- [failed] Earlier tests-1 failure (%s)\n", refs[5]), 1)
		}
		calls := 0
		next, stats, err := SummarizeBudgetedLocal(t.Context(), input, 32768, 2048, func(_ context.Context, prompt string, _ int) (StructuredSummary, error) {
			calls++
			if !strings.Contains(prompt, decisionFidelityRules) {
				t.Fatal("shared fidelity rules absent")
			}
			if pass > 0 {
				if !strings.Contains(prompt, summary.RenderBlock().Text) {
					t.Fatal("prior qualifications or references changed before model call")
				}
				if pass == 2 && !strings.Contains(prompt, "call=tests-2 is_error=false") {
					t.Fatal("successful check evidence missing")
				}
				if !strings.Contains(prompt, correction.Blocks[0].Text) {
					t.Fatal("correction missing")
				}
			} else if !strings.Contains(prompt, "call=tests-1 is_error=true") {
				t.Fatal("failure evidence missing")
			}
			return ParseSummary(nextWire), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if calls != 1 || stats.Chunks != 1 {
			t.Fatalf("unexpected chunking: calls=%d stats=%+v", calls, stats)
		}
		want := ParseSummary(nextWire)
		if !reflect.DeepEqual(next, want) {
			t.Fatal("summary transport changed classification")
		}
		merged := MergeSummaries([]StructuredSummary{next, next})
		if !reflect.DeepEqual(merged, want) {
			t.Fatal("dedup changed classifications or citations")
		}
		if !strings.Contains(merged.RenderBlock().Text, refs[5]) {
			t.Fatal("original tool evidence reference lost")
		}
		summary = merged
	}
}

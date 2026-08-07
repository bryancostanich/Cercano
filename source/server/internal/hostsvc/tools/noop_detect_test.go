package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
)

// permStub is a minimal agenttools.Tool used to build test registries with a
// chosen permission tier. The agenttools package's own stub is unexported, so
// we define an equivalent here.
type permStub struct {
	name string
	perm agenttools.Permission
}

func (s permStub) Name() string                     { return s.name }
func (s permStub) Description() string              { return "stub" }
func (s permStub) Permission() agenttools.Permission { return s.perm }
func (s permStub) Schema() json.RawMessage          { return json.RawMessage(`{"type":"object"}`) }
func (s permStub) Execute(_ context.Context, _ json.RawMessage) (*agenttools.Result, error) {
	return nil, nil
}

func regWith(tools ...permStub) *agenttools.Registry {
	r := agenttools.NewRegistry()
	for _, t := range tools {
		r.MustRegister(t)
	}
	return r
}

// historyCalling builds a tool-loop history that invokes each named tool once.
func historyCalling(names ...string) []llm.Message {
	blocks := make([]llm.Block, 0, len(names))
	for _, n := range names {
		blocks = append(blocks, llm.Block{Type: llm.BlockToolUse, ToolName: n})
	}
	return []llm.Message{{Role: llm.RoleAssistant, Blocks: blocks}}
}

// The canonical failure: granted a write tool (Edit) and an exec tool (Bash),
// called only a read tool (Glob), but returned a "done" summary. This is the
// provable contradiction and MUST flag.
func TestDetectSuspiciousNoOp_GrantedWriteCalledNone_Flags(t *testing.T) {
	reg := regWith(
		permStub{"Edit", agenttools.PermW},
		permStub{"Bash", agenttools.PermX},
		permStub{"Glob", agenttools.PermR},
	)
	mutating := mutatingToolNames(reg)
	called := calledToolNames(historyCalling("Glob"))

	suspicious, reason := detectSuspiciousNoOp("I used the granted tools: Glob.", called, mutating)
	if !suspicious {
		t.Fatalf("expected suspicious=true for write-granted no-op; got false")
	}
	if reason == "" {
		t.Fatalf("expected a non-empty suspicion reason")
	}
	// Reason should name the write/exec tools that went unused, both of them.
	for _, want := range []string{"Bash", "Edit"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q should mention unused tool %q", reason, want)
		}
	}
}

// The git-pull valid case: granted Bash, actually called Bash once, answered.
// No contradiction — MUST NOT flag. This is the false-positive guard that
// killed the hard-fail option.
func TestDetectSuspiciousNoOp_GrantedWriteCalledIt_DoesNotFlag(t *testing.T) {
	reg := regWith(permStub{"Bash", agenttools.PermX})
	mutating := mutatingToolNames(reg)
	called := calledToolNames(historyCalling("Bash"))

	suspicious, _ := detectSuspiciousNoOp("Pulled; HEAD is abc123.", called, mutating)
	if suspicious {
		t.Fatalf("expected suspicious=false when the granted write/exec tool was actually used")
	}
}

// Read-only investigation: granted only Read/Grep/Glob, called Grep once.
// Cannot prove no-op — MUST NOT flag (no false positive on a legit trace).
func TestDetectSuspiciousNoOp_ReadOnlyGrant_DoesNotFlag(t *testing.T) {
	reg := regWith(
		permStub{"Read", agenttools.PermR},
		permStub{"Grep", agenttools.PermR},
		permStub{"Glob", agenttools.PermR},
	)
	mutating := mutatingToolNames(reg)
	called := calledToolNames(historyCalling("Grep"))

	suspicious, _ := detectSuspiciousNoOp("The bug is in matchTable.", called, mutating)
	if suspicious {
		t.Fatalf("expected suspicious=false for a read-only grant (no write/exec was possible)")
	}
}

// Empty final text: even with a write grant unused, there is no "done" claim to
// contradict — MUST NOT flag.
func TestDetectSuspiciousNoOp_EmptyText_DoesNotFlag(t *testing.T) {
	reg := regWith(permStub{"Edit", agenttools.PermW})
	mutating := mutatingToolNames(reg)
	called := calledToolNames(historyCalling("Glob"))

	suspicious, _ := detectSuspiciousNoOp("   \n  ", called, mutating)
	if suspicious {
		t.Fatalf("expected suspicious=false when final text is blank (no completion claim)")
	}
}

// mutatingToolNames must derive purely from the registry's permission tiers.
func TestMutatingToolNames_DerivesFromPermissions(t *testing.T) {
	reg := regWith(
		permStub{"Read", agenttools.PermR},
		permStub{"Edit", agenttools.PermW},
		permStub{"rm_file", agenttools.PermX},
	)
	got := mutatingToolNames(reg)
	if got["Read"] {
		t.Errorf("Read (PermR) must not be classified as mutating")
	}
	if !got["Edit"] || !got["rm_file"] {
		t.Errorf("Edit (PermW) and rm_file (PermX) must be classified as mutating; got %v", got)
	}
}

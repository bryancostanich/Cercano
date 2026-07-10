package watchdog

import (
	"encoding/json"
	"testing"

	"cercano/source/server/internal/llm"
)

// standaloneAliases mirrors builtins.AgentAliases (not imported here — that
// would invert the dependency this package deliberately avoids).
func standaloneAliases() map[string]string {
	return map[string]string{
		"read_file":   "Read",
		"list_dir":    "LS",
		"glob":        "Glob",
		"grep":        "Grep",
		"write_file":  "Write",
		"edit_file":   "Edit",
		"run_command": "Bash",
	}
}

// The standalone agent emits display aliases (Bash, Edit, Write); checks match
// canonical names. Without the reverse mapping, worktree-first could literally
// never fire — the registry never emits "run_command".
func TestChecksFireOnDisplayAliasedToolNames(t *testing.T) {
	SetDisplayAliases(standaloneAliases())

	// worktree-first: a Bash-named branch creation must apply.
	args, _ := json.Marshal(map[string]any{"cmd": []string{"git", "checkout", "-b", "feat/x"}})
	if !(worktreeFirstCheck{}).Applies(Action{Kind: "tool_call", ToolName: "Bash", ToolArgs: args}) {
		t.Error("worktree-first must apply to a Bash-named git checkout -b")
	}
	if (worktreeFirstCheck{}).Applies(Action{Kind: "tool_call", ToolName: "Bash", ToolArgs: mustJSON(map[string]any{"cmd": []string{"git", "status"}})}) {
		t.Error("worktree-first must not apply to a plain git status")
	}

	// systematic-debugging: an Edit-named mutation must apply.
	if !(debugLoopCheck{}).Applies(Action{Kind: "tool_call", ToolName: "Edit"}) {
		t.Error("systematic-debugging must apply to an Edit-named mutation")
	}

	// commit-checkpoint: an Edit following an uncommitted Write must apply,
	// and a git_commit in between must clear the running count.
	transcript := []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolName: "Write", ToolInput: json.RawMessage(`{"path":"a.go"}`)}}},
	}
	if !(commitCheckpointCheck{}).Applies(Action{Kind: "tool_call", ToolName: "Edit", Transcript: transcript}) {
		t.Error("commit-checkpoint must apply to Edit after an uncommitted Write")
	}
	committed := append(transcript, llm.Message{
		Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolName: "git_commit", ToolInput: json.RawMessage(`{}`)}},
	})
	if (commitCheckpointCheck{}).Applies(Action{Kind: "tool_call", ToolName: "Edit", Transcript: committed}) {
		t.Error("a git_commit must clear the uncommitted-edit count")
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

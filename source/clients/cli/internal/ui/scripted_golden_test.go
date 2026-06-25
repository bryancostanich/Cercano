package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// frozenTurnStart is a fixed wall-clock so the inline turn-status placeholder
// (when visible) renders a stable elapsed; the script streams a token first so
// the placeholder is replaced, but we freeze for total determinism.
var frozenTurnStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// scriptedTurn is the canned StreamMsg script driven through both the pre-move
// and post-move paths: token → progress → tool lifecycle → token → done.
func scriptedTurn() []agentclient.StreamMsg {
	return []agentclient.StreamMsg{
		{Type: agentclient.TypeRouteSelected, RouteModel: "local-fast", RouteCloud: false},
		{Type: agentclient.TypeToken, Token: "Looking into it. "},
		{Type: agentclient.TypeProgress, Note: "searching files"},
		{Type: agentclient.TypeToolUseStart, ToolUseID: "t1", ToolName: "Bash"},
		{Type: agentclient.TypeToolUseStop, ToolUseID: "t1", ArgsSummary: `{"cmd":["ls","-la"]}`},
		{Type: agentclient.TypeToolExecStart, ToolUseID: "t1"},
		{Type: agentclient.TypeToolExecComplete, ToolUseID: "t1", IsError: false, Summary: "ok", Detail: "12 files"},
		{Type: agentclient.TypeToken, Token: "Here is the answer."},
		{Type: agentclient.TypeDone, Final: "Here is the answer.", TokIn: 12, TokOut: 34, Notice: ""},
	}
}

func newScriptedModel() Model {
	p := theme.Cracker()
	m := Model{
		styles:    theme.NewStyles(p),
		palette:   p,
		width:     80,
		streaming: true,
		chat:      newChatView(theme.NewStyles(p), p, "", "", 78, 20),
	}
	m.chat.AppendEntry(&Entry{Role: RoleUser, Content: "list the files"})
	m.chat.AppendEntry(&Entry{Role: RoleAssistant, Content: "", Streaming: true})
	m.turnStart = frozenTurnStart
	return m
}

// driveNewPath maps each StreamMsg via the driver and routes it exactly as the
// chatStreamMsg case does, then renders the transcript.
func driveNewPath(t *testing.T) string {
	t.Helper()
	m := newScriptedModel()
	for _, sm := range scriptedTurn() {
		m.turnStart = frozenTurnStart // re-freeze (submit would reset it live)
		next, _ := m.Update(chatStreamMsg{ev: streamMsgToEvent(sm)})
		m = next.(Model)
	}
	m.turnStart = frozenTurnStart
	m.refreshViewport()
	return m.renderViewportWithScrollbar()
}

// TestScriptedTurnTranscript asserts the post-move driver+Apply path renders
// byte-identically to the frozen committed golden.
func TestScriptedTurnTranscript(t *testing.T) {
	got := driveNewPath(t)

	path := filepath.Join("testdata", "chatview", "scripted_turn.golden")
	if os.Getenv("UPDATE_SCRIPTED_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (set UPDATE_SCRIPTED_GOLDEN=1 once to create)", path, err)
	}
	if got != string(want) {
		t.Errorf("scripted transcript mismatch vs %s", path)
	}
}

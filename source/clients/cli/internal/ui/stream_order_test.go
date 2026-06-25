package ui

import (
	"reflect"
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
	"cercano/source/clients/cli/internal/theme"
)

// newStreamTestModel builds a Model wired enough to drive the post-move stream
// path (styles + a sized viewport + a user turn and the streaming assistant
// placeholder, exactly as submit() leaves things). streaming is set so the
// chatStreamMsg route is live.
func newStreamTestModel() Model {
	p := theme.Cracker()
	m := Model{
		styles:    theme.NewStyles(p),
		palette:   p,
		width:     80,
		streaming: true,
		chat:      newChatView(theme.NewStyles(p), p, "", "", 79, 20),
	}
	m.chat.AppendEntry(&Entry{Role: RoleUser, Content: "read the readme"})
	m.chat.AppendEntry(&Entry{Role: RoleAssistant, Content: "", Streaming: true})
	return m
}

// drive feeds one StreamMsg through the NEW path: map it via streamMsgToEvent,
// then route it exactly as the chatStreamMsg case does (telemetry → host fields,
// transcript → chatView.Apply, permission → host). Ordering expectations are
// unchanged — this proves the driver+Apply port is behavior-neutral.
func (m *Model) drive(t *testing.T, sm agentclient.StreamMsg) {
	t.Helper()
	next, _ := m.Update(chatStreamMsg{ev: streamMsgToEvent(sm)})
	*m = next.(Model)
}

// Reproduce the reported ordering bug: an agent that runs a tool and THEN emits
// its final answer must render the tool call BEFORE the answer in scrollback.
func TestStreamOrderingToolBeforeFinalText(t *testing.T) {
	m := newStreamTestModel()
	m.drive(t, agentclient.StreamMsg{Type: agentclient.TypeToolUseStart, ToolUseID: "t1", ToolName: "Bash"})
	m.drive(t, agentclient.StreamMsg{Type: agentclient.TypeToolExecComplete, ToolUseID: "t1", Summary: "done"})
	m.drive(t, agentclient.StreamMsg{Type: agentclient.TypeToken, Token: "Here is the answer."})
	m.drive(t, agentclient.StreamMsg{Type: agentclient.TypeDone, Final: "Here is the answer."})

	toolIdx, textIdx := -1, -1
	for i, e := range m.chat.Entries() {
		if e.Tool != nil {
			toolIdx = i
		}
		if e.Role == RoleAssistant && strings.Contains(e.Content, "Here is the answer") {
			textIdx = i
		}
	}
	if toolIdx < 0 || textIdx < 0 {
		t.Fatalf("setup: toolIdx=%d textIdx=%d entries=%d", toolIdx, textIdx, len(m.chat.Entries()))
	}
	if textIdx < toolIdx {
		t.Fatalf("ORDERING BUG: final answer at index %d renders BEFORE the tool call at index %d", textIdx, toolIdx)
	}
}

// Pre-tool prose, a tool call, then the final answer must render in that exact
// chronological order, with no entry left streaming and no orphan empty entry.
func TestStreamOrderingInterleaveNoOrphans(t *testing.T) {
	m := newStreamTestModel()
	m.drive(t, agentclient.StreamMsg{Type: agentclient.TypeToken, Token: "Looking"})
	m.drive(t, agentclient.StreamMsg{Type: agentclient.TypeToolUseStart, ToolUseID: "t1", ToolName: "Bash"})
	m.drive(t, agentclient.StreamMsg{Type: agentclient.TypeToolExecComplete, ToolUseID: "t1", Summary: "ok"})
	m.drive(t, agentclient.StreamMsg{Type: agentclient.TypeToken, Token: "Done"})
	m.drive(t, agentclient.StreamMsg{Type: agentclient.TypeDone, Final: "Done"})

	var got []string
	for _, e := range m.chat.Entries() {
		switch {
		case e.Tool != nil:
			got = append(got, "tool")
		case e.Role == RoleUser:
			got = append(got, "user")
		case e.Role == RoleAssistant:
			got = append(got, "asst:"+strings.TrimSpace(e.Content))
		default:
			got = append(got, "sys:"+strings.TrimSpace(e.Content))
		}
		if e.Streaming {
			t.Errorf("entry left streaming after Done: %+v", e)
		}
		if e.Role == RoleAssistant && e.Tool == nil && e.Content == "" {
			t.Errorf("orphan empty assistant entry left in scrollback")
		}
	}
	want := []string{"user", "asst:Looking", "tool", "asst:Done"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entry order = %v, want %v", got, want)
	}
}

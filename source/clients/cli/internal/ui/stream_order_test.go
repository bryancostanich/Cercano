package ui

import (
	"reflect"
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
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
	}
	m.setMainChat(newChatView(theme.NewStyles(p), p, "", "", 79, 20))
	m.mainChat().AppendEntry(&Entry{Role: RoleUser, Content: "read the readme"})
	m.mainChat().AppendEntry(&Entry{Role: RoleAssistant, Content: "", Streaming: true})
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
	for i, e := range m.mainChat().Entries() {
		if e.Tool != nil {
			toolIdx = i
		}
		if e.Role == RoleAssistant && strings.Contains(e.Content, "Here is the answer") {
			textIdx = i
		}
	}
	if toolIdx < 0 || textIdx < 0 {
		t.Fatalf("setup: toolIdx=%d textIdx=%d entries=%d", toolIdx, textIdx, len(m.mainChat().Entries()))
	}
	if textIdx < toolIdx {
		t.Fatalf("ORDERING BUG: final answer at index %d renders BEFORE the tool call at index %d", textIdx, toolIdx)
	}
}

// Regression for the LUNIE formatting corruption: an out-of-band system notice
// (a title rename) that lands WHILE an assistant message is streaming must NOT
// split that message. AppendNotice inserts the notice above the open stream, so
// continuation tokens keep flowing into the same entry and a fenced code block
// stays intact. A plain AppendEntry here would slot the notice after the open
// entry, forcing the next token to open a fresh assistant entry below it.
func TestStreamNoticeMidStreamDoesNotSplitMessage(t *testing.T) {
	m := newStreamTestModel()
	c := m.mainChat()

	// Stream the first half of a fenced code block into the open assistant entry.
	c.Apply(chatAssistantDeltaMsg{token: "Here is code:\n```go\nfunc main() {\n"})
	// An async title rename lands mid-stream.
	c.AppendNotice(&Entry{Role: RoleSystem, Content: "renamed to: LUNIE FIXES"})
	// The rest of the code block streams in.
	c.Apply(chatAssistantDeltaMsg{token: "\tprintln(\"hi\")\n}\n```"})

	// Exactly one assistant entry, holding the whole code block contiguously.
	var asst []*Entry
	var noticeIdx, asstIdx int = -1, -1
	for i, e := range c.Entries() {
		if e.Role == RoleAssistant {
			asst = append(asst, e)
			asstIdx = i
		}
		if e.Role == RoleSystem && strings.Contains(e.Content, "renamed to") {
			noticeIdx = i
		}
	}
	if len(asst) != 1 {
		t.Fatalf("expected the streamed message to stay in ONE assistant entry, got %d", len(asst))
	}
	full := asst[0].Content
	if !strings.Contains(full, "```go") || !strings.Contains(full, "println") || !strings.Contains(full, "}\n```") {
		t.Fatalf("code block was split; assistant content = %q", full)
	}
	// The notice must sit ABOVE the (still last) streaming assistant entry.
	if noticeIdx < 0 || asstIdx < 0 || noticeIdx > asstIdx {
		t.Fatalf("notice at %d should precede assistant at %d", noticeIdx, asstIdx)
	}
	if !asst[0].Streaming {
		t.Fatalf("assistant entry should still be streaming after the notice insert")
	}
}

// Every out-of-band notice type that can fire mid-stream (title rename, prompt
// color, permission/session mode, /help text, context-regen/elide progress and
// completion) must go through AppendNotice and leave a streamed code block
// intact. Table-driven so a newly-added notice type is one line to cover.
func TestAllMidStreamNoticesPreserveCodeFence(t *testing.T) {
	notices := []string{
		"renamed to: LUNIE FIXES",
		"prompt color set",
		"Permission mode → strict",
		"Mode → off (unrestricted)",
		"context-regen: pass 2/3",
		"context rebuilt: ~9000 → ~4000 tokens",
		"context elided: ~9000 → ~4000 tokens (3 tool results stubbed)",
		"/help long multi-line body\nwith several\nlines of text",
	}
	for _, content := range notices {
		t.Run(content[:min(len(content), 24)], func(t *testing.T) {
			m := newStreamTestModel()
			c := m.mainChat()
			c.Apply(chatAssistantDeltaMsg{token: "```go\nfunc main() {\n"})
			c.AppendNotice(&Entry{Role: RoleSystem, Content: content})
			c.Apply(chatAssistantDeltaMsg{token: "\tprintln(\"hi\")\n}\n```"})

			var asst []*Entry
			for _, e := range c.Entries() {
				if e.Role == RoleAssistant {
					asst = append(asst, e)
				}
			}
			if len(asst) != 1 {
				t.Fatalf("notice %q split the stream into %d assistant entries", content, len(asst))
			}
			if full := asst[0].Content; !strings.Contains(full, "```go") || !strings.Contains(full, "}\n```") {
				t.Fatalf("notice %q tore the code fence: %q", content, full)
			}
		})
	}
}

// With no stream open, AppendNotice must behave as a plain append (notice lands
// last), so it never disturbs a finalized transcript.
func TestAppendNoticeNoStreamAppendsLast(t *testing.T) {
	p := theme.Cracker()
	c := newChatView(theme.NewStyles(p), p, "", "", 79, 20)
	c.AppendEntry(&Entry{Role: RoleAssistant, Content: "done", Streaming: false})
	c.AppendNotice(&Entry{Role: RoleSystem, Content: "renamed to: X"})
	es := c.Entries()
	if len(es) != 2 || es[1].Role != RoleSystem {
		t.Fatalf("expected notice appended last; entries = %+v", es)
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
	for _, e := range m.mainChat().Entries() {
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

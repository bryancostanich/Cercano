package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"cercano/source/server/pkg/agentclient"
)

// ChatDriver plugs an agent into a chat surface. Submit returns a tea.Cmd that
// emits chat events (chatStatusMsg / chatAssistantMsg / chatConfirmMsg /
// chatDoneMsg / chatErrorMsg). The chat surface is agent-agnostic; all agent
// specifics live in the driver.
type ChatDriver interface {
	Name() string
	Submit(ctx context.Context, input string) tea.Cmd
}

// chatDriverMsg is the set of events a driver emits. They are top-level
// tea.Msg values routed by the model to the active surface. Main-chat-only
// events and fields are additive — the /c driver never emits them, so the
// additive fields default-zero on the /c path and are ignored there.
//
// chatStatusMsg's tokOut/model/cloud are main-chat turn telemetry (footer);
// /c sets only activity.
type chatStatusMsg struct {
	activity string
	tokOut   int    // main-chat: live output-token count for the footer
	model    string // main-chat: engine handling the turn (RouteSelected)
	cloud    bool   // main-chat: true when the turn routed to a cloud engine
}
type chatAssistantMsg struct{ text string }

// ── additive main-chat transcript events (F2-A) ─────────────────────────────
// These are emitted ONLY by the main agent driver and consumed by
// chatView.Apply. /c never emits them.

// chatAssistantDeltaMsg carries one streamed token of assistant prose.
type chatAssistantDeltaMsg struct{ token string }

// chatProgressMsg carries a routing/phase note for the open assistant entry's
// inline status line (distinct from chatStatusMsg, which is /c-compatible).
type chatProgressMsg struct{ note string }

// toolEntry* events drive the tool-call lifecycle in scrollback.
type toolEntryStartMsg struct{ id, name string }
type toolEntryStopMsg struct{ id, argsSummary string }
type toolEntryExecStartMsg struct{ id string }
type toolEntryExecCompleteMsg struct {
	id        string
	detail    string
	summary   string
	startLine int // 1-based first line of an edit/write (0 = n/a)
	isError   bool
}

// permissionRequiredMsg is host-routed (raises the confirm gate); it is NOT a
// chatView.Apply event.
type permissionRequiredMsg struct {
	id, name, argsJSON, tier string
	destructive              bool
}

// chatDoneMsg signals the end of a driver turn. text is an optional closing
// line for /c (chatView appends it as a system entry). tokIn/tokOut/notice/model
// carry main-chat turn telemetry; the /c driver never sets them so they
// default to zero and are ignored by chatView.Apply on the /c path.
type chatDoneMsg struct {
	text   string // /c: optional closing system line
	tokIn  int    // main-chat: input tokens for the completed turn
	tokOut int    // main-chat: output tokens for the completed turn
	notice string // main-chat: non-fatal notice (e.g. "cloud not configured")
	model  string // main-chat: local model name reported by the agent
}
type chatErrorMsg struct{ err error }

// watchdogEventMsg is emitted by the main agent driver when the server's
// watchdog fires. kind is "challenge", "block", or "echo"; chatView.Apply
// renders each differently in scrollback.
type watchdogEventMsg struct {
	kind     string // "challenge" | "block" | "echo"
	protocol string // protocol name (empty for echo)
	summary  string // human-readable text (proto.Text)
	thread   string // "watchdog" | "main" (echo only)
}

// subAgentEventMsg is a structured event envelope for an ephemeral sub-agent
// tab. inner is any chatView.Apply-compatible transcript event; lifecycle
// events use kind/tool metadata directly.
type subAgentEventMsg struct {
	id       string
	title    string
	kind     string
	tools    []string
	ignored  []string
	inner    tea.Msg
	toolName string
	text     string
}

func subAgentEventMsgFromStream(sm agentclient.StreamMsg) subAgentEventMsg {
	msg := subAgentEventMsg{
		id:      sm.SubAgentID,
		title:   sm.SubAgentTitle,
		kind:    sm.SubAgentKind,
		tools:   append([]string(nil), sm.GrantedTools...),
		ignored: append([]string(nil), sm.IgnoredTools...),
		text:    sm.SubAgentText,
	}
	switch sm.SubAgentKind {
	case "token":
		msg.inner = chatAssistantDeltaMsg{token: sm.SubAgentText}
	case "tool_use_start":
		msg.inner = toolEntryStartMsg{id: sm.ToolUseID, name: sm.ToolName}
	case "tool_use_stop":
		msg.inner = toolEntryStopMsg{id: sm.ToolUseID, argsSummary: sm.ArgsSummary}
	case "tool_exec_start":
		msg.inner = toolEntryExecStartMsg{id: sm.ToolUseID}
	case "tool_exec_complete":
		msg.inner = toolEntryExecCompleteMsg{id: sm.ToolUseID, detail: sm.Detail, summary: sm.Summary, startLine: sm.StartLine, isError: sm.IsError}
	}
	return msg
}

// chatConfirmMsg asks the host to raise the shared confirm gate. onYes/onNo are
// the driver's follow-up cmds (e.g. perform the delete, or cancel). The pane
// renders `assistant` as the agent's message; the MODEL raises the confirm
// (it owns m.pendingConfirm) — see model.go routing.
type chatConfirmMsg struct {
	assistant string
	onYes     tea.Cmd
	onNo      tea.Cmd
}

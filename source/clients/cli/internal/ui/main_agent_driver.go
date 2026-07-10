package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"cercano/source/server/pkg/agentclient"
)

// mainAgentDriver opens the main chat's StreamChat and emits agent-agnostic
// typed transcript/telemetry/permission events. It mirrors contextManagerDriver:
// the host stays a pure router (no streamCh field) — the channel + cancel travel
// inside the emitted chatStreamMsg so the drain loop re-arms itself.
type mainAgentDriver struct {
	agent   *agentclient.Client
	convID  string
	workDir string
}

func (d *mainAgentDriver) Name() string { return "main agent" }

// chatStreamMsg wraps one routed event plus the cmd that reads the NEXT event
// off the stream. The host routes ev, then returns next to re-arm the loop —
// the same shape as the old streamTickMsg+waitForStream pair, relocated here.
// gen fences the event to the turn that produced it: a canceled turn's late
// events must never be attributed to (or tear down) the turn that replaced it.
type chatStreamMsg struct {
	gen  int
	ev   tea.Msg
	next tea.Cmd
}

// Submit opens StreamChat and returns (waitCmd, cancel, err). On success the
// host stores cancel (so Esc aborts the turn) and issues waitCmd, which drains
// one StreamMsg, maps it via streamMsgToEvent, and wraps it in a chatStreamMsg
// carrying a re-arm of itself. Channel close → streamEndMsg{gen}. Every event
// carries gen — the host's turn generation at submit time — so stale events
// from a canceled turn are identifiable.
func (d *mainAgentDriver) Submit(ctx context.Context, gen int, input string, images []agentclient.InlineImage) (tea.Cmd, context.CancelFunc, error) {
	ctx, cancel := context.WithCancel(ctx)
	ch, err := d.agent.StreamChat(ctx, d.convID, input, d.workDir, images...)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	var wait tea.Cmd
	wait = func() tea.Msg {
		sm, ok := <-ch
		if !ok {
			return streamEndMsg{gen: gen}
		}
		return chatStreamMsg{gen: gen, ev: streamMsgToEvent(sm), next: wait}
	}
	return wait, cancel, nil
}

// streamMsgToEvent maps one StreamMsg to its agent-agnostic event. Pure (no
// I/O), unit-testable — mirrors contextManagerDriver.proposalToMsg.
//
// Telemetry-bearing events (chatStatusMsg from RouteSelected, chatDoneMsg) carry
// their payloads so the host can fold them into the footer; transcript fields
// ride the same events for chatView.Apply. permissionRequiredMsg is host-routed.
func streamMsgToEvent(sm agentclient.StreamMsg) tea.Msg {
	switch sm.Type {
	case agentclient.TypeToken:
		return chatAssistantDeltaMsg{token: sm.Token}
	case agentclient.TypeProgress:
		return chatProgressMsg{note: normalizeProgress(sm.Note)}
	case agentclient.TypeRouteSelected:
		return chatStatusMsg{model: sm.RouteModel, cloud: sm.RouteCloud}
	case agentclient.TypeToolUseStart:
		return toolEntryStartMsg{id: sm.ToolUseID, name: sm.ToolName}
	case agentclient.TypeToolUseStop:
		return toolEntryStopMsg{id: sm.ToolUseID, argsSummary: sm.ArgsSummary}
	case agentclient.TypeToolExecStart:
		return toolEntryExecStartMsg{id: sm.ToolUseID}
	case agentclient.TypeToolExecComplete:
		return toolEntryExecCompleteMsg{
			id:        sm.ToolUseID,
			detail:    sm.Detail,
			summary:   sm.Summary,
			startLine: sm.StartLine,
			isError:   sm.IsError,
		}
	case agentclient.TypeDone:
		return chatDoneMsg{
			text:   sm.Final,
			tokIn:  sm.TokIn,
			tokOut: sm.TokOut,
			notice: sm.Notice,
			model:  sm.Model,
		}
	case agentclient.TypePermissionRequired:
		return permissionRequiredMsg{
			id:          sm.ToolUseID,
			name:        sm.ToolName,
			argsJSON:    sm.ArgsJSON,
			tier:        sm.Tier,
			destructive: sm.Destructive,
		}
	case agentclient.TypeWatchdog:
		return watchdogEventMsg{
			kind:     sm.WatchdogKind,
			protocol: sm.Protocol,
			summary:  sm.Summary,
			thread:   sm.Thread,
		}
	case agentclient.TypeSubAgent:
		return subAgentEventMsgFromStream(sm)
	case agentclient.TypeError:
		return chatErrorMsg{err: sm.Err}
	}
	return nil
}

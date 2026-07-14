package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"cercano/source/server/pkg/agentclient"
)

// permissionModeChangedMsg is delivered when the agent pushes a permission-mode
// change over the standing SubscribeEvents stream — a /strict-style command from
// any client, or an out-of-band edit to permissions.yaml. next re-arms the drain
// loop, mirroring chatStreamMsg so the host stays a pure router.
type permissionModeChangedMsg struct {
	mode string
	next tea.Cmd
}

// openRuntimeStatusChangedMsg is delivered when the agent pushes a
// OpenRuntimeStatusChanged event — the local-runtime detection outcome
// after a config-driven runtime swap. Non-ok statuses drive the chip and
// the install modal; ok statuses clear both.
type openRuntimeStatusChangedMsg struct {
	status *agentclient.OpenRuntimeStatus
	next   tea.Cmd
}

// configChangedMsg is delivered when the agent pushes a ConfigChanged event
// over the standing SubscribeEvents stream. It keeps header/status chips fresh
// after settings/profile changes without waiting for an explicit config reload.
type configChangedMsg struct {
	field string
	value string
	next  tea.Cmd
}

// subscribeEventsCmd opens the standing server->client event stream and returns
// a cmd that drains one event and re-arms itself. This is how the status bar
// learns about mode changes and Meridian state without polling. On stream
// error/close it stops (returns a nil msg) so we don't spin; chips simply
// keep their last value.
func subscribeEventsCmd(ag *agentclient.Client) tea.Cmd {
	return func() tea.Msg {
		ch, err := ag.SubscribeEvents(context.Background())
		if err != nil {
			return nil
		}
		var wait tea.Cmd
		wait = func() tea.Msg {
			ev, ok := <-ch
			if !ok || ev.Err != nil {
				return nil // stream ended; stop draining
			}
			if ev.OpenRuntimeStatus != nil {
				return openRuntimeStatusChangedMsg{status: ev.OpenRuntimeStatus, next: wait}
			}
			if ev.ConfigChanged != nil {
				return configChangedMsg{field: ev.ConfigChanged.Field, value: ev.ConfigChanged.Value, next: wait}
			}
			return permissionModeChangedMsg{mode: ev.Mode, next: wait}
		}
		return wait()
	}
}

// connStateChangedMsg is delivered when the SDK's reconnect loop
// transitions the gRPC connection between healthy / reconnecting /
// terminally-failed. Drives the status-bar chip and the prompt-
// rehydration flow (see Model.handleConnStateChanged).
type connStateChangedMsg struct {
	state        agentclient.ConnState
	attempt      int
	errMsg       string
	crashSummary string // populated on Connected → Reconnecting when the crash log has a fresh entry
	next         tea.Cmd
}

// subscribeConnStateCmd hooks the SDK's connection-state channel into
// the bubbletea event loop. Mirrors the shape of subscribeEventsCmd:
// one message per state transition, with next re-arming the drain.
// When the channel closes (Client.Close) the drain stops.
func subscribeConnStateCmd(ag *agentclient.Client) tea.Cmd {
	return func() tea.Msg {
		ch, _ := ag.ConnStateChanges()
		var wait tea.Cmd
		wait = func() tea.Msg {
			ev, ok := <-ch
			if !ok {
				return nil
			}
			msg := connStateChangedMsg{
				state:        ev.State,
				attempt:      ev.Attempt,
				crashSummary: ev.CrashSummary,
				next:         wait,
			}
			if ev.Err != nil {
				msg.errMsg = ev.Err.Error()
			}
			return msg
		}
		return wait()
	}
}

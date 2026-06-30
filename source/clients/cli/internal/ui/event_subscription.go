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

// meridianStatusChangedMsg is delivered when the agent pushes a Meridian
// proxy state transition. status is nil only on terminal stream error
// (which also returns next=nil); otherwise it carries the new snapshot.
type meridianStatusChangedMsg struct {
	status *agentclient.MeridianStatus
	next   tea.Cmd
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
			if ev.MeridianStatus != nil {
				return meridianStatusChangedMsg{status: ev.MeridianStatus, next: wait}
			}
			return permissionModeChangedMsg{mode: ev.Mode, next: wait}
		}
		return wait()
	}
}

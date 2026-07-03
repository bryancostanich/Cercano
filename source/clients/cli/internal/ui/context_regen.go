package ui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"cercano/source/server/pkg/agentclient"
)

// contextRegenProgressMsg is one progress line from the RegenerateContext
// stream; next re-arms the drain so the following frame is delivered.
type contextRegenProgressMsg struct {
	line string
	next tea.Cmd
}

// contextRegenDoneMsg is the terminal frame of a context regen: ok/err from
// the server plus the before/after send-view token counts.
type contextRegenDoneMsg struct {
	ok   bool
	err  string
	pre  int
	post int
}

// isTransportLoss reports whether a regen error string looks like the stream
// transport died (agent shutdown/restart) rather than the rebuild itself
// failing server-side.
func isTransportLoss(err string) bool {
	for _, marker := range []string{"Unavailable", "graceful_stop", "goaway", "error reading from server: EOF", "connection refused", "transport is closing"} {
		if strings.Contains(err, marker) {
			return true
		}
	}
	return false
}

// startContextRegenCmd opens the RegenerateContext streaming RPC for the
// conversation and drains it one frame per message, mirroring the runtime
// install pattern. The rebuild runs server-side to completion regardless of
// what the UI does with the stream.
func startContextRegenCmd(ag *agentclient.Client, convID string) tea.Cmd {
	return func() tea.Msg {
		ch, err := ag.RegenerateContext(context.Background(), convID)
		if err != nil {
			return contextRegenDoneMsg{err: err.Error()}
		}
		var drain tea.Cmd
		drain = func() tea.Msg {
			frame, ok := <-ch
			if !ok {
				return contextRegenDoneMsg{err: "regen stream ended unexpectedly"}
			}
			if frame.Err != nil {
				return contextRegenDoneMsg{err: frame.Err.Error()}
			}
			if frame.Done {
				return contextRegenDoneMsg{ok: frame.Ok, err: frame.Error, pre: frame.PreTokens, post: frame.PostTokens}
			}
			return contextRegenProgressMsg{line: frame.Line, next: drain}
		}
		return contextRegenProgressMsg{line: "rebuilding context from raw turns…", next: drain}
	}
}

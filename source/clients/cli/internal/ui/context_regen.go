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
// the server plus the before/after send-view token counts. line carries the
// server's summary wording ("context rebuilt/compacted: ~X → ~Y tokens"),
// which knows whether the run was a full rebuild or incremental.
type contextRegenDoneMsg struct {
	ok   bool
	err  string
	line string
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
// install pattern. incremental=false is the full /context-regen rebuild;
// incremental=true is /compact (digest backlog, keep summaries). The work
// runs server-side to completion regardless of what the UI does with the
// stream.
func startContextRegenCmd(ag *agentclient.Client, convID string, incremental bool) tea.Cmd {
	return func() tea.Msg {
		ch, err := ag.RegenerateContext(context.Background(), convID, incremental)
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
				return contextRegenDoneMsg{ok: frame.Ok, err: frame.Error, line: frame.Line, pre: frame.PreTokens, post: frame.PostTokens}
			}
			return contextRegenProgressMsg{line: frame.Line, next: drain}
		}
		first := "rebuilding context from raw turns…"
		if incremental {
			first = "compacting context backlog…"
		}
		return contextRegenProgressMsg{line: first, next: drain}
	}
}

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

// elideContextDoneMsg is the result of the unary /elide-context RPC.
type elideContextDoneMsg struct {
	err     string
	pre     int
	post    int
	stubbed int
}

// startElideContextCmd runs /elide-context: ask the agent to stub every
// tool-result body in the conversation's context up to now (in-memory,
// send-view only — raw turns untouched, resets on agent restart).
func startElideContextCmd(ag *agentclient.Client, convID string) tea.Cmd {
	return func() tea.Msg {
		pre, post, stubbed, err := ag.ElideContext(context.Background(), convID)
		if err != nil {
			return elideContextDoneMsg{err: err.Error()}
		}
		return elideContextDoneMsg{pre: pre, post: post, stubbed: stubbed}
	}
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
// install pattern. incremental=false is the full /context-regen foreground
// rebuild; incremental=true is /compact, which now schedules background
// compaction and returns immediately.
func startContextRegenCmd(ag *agentclient.Client, convID string, incremental bool) tea.Cmd {
	first := "rebuilding context from raw turns…"
	if incremental {
		first = "compacting context backlog…"
	}
	return startContextStreamCmd(func(ctx context.Context) (<-chan agentclient.RegenProgress, error) {
		return ag.RegenerateContext(ctx, convID, incremental)
	}, first)
}

// startClearCompactedContextCmd is /clear-compacted-context: drop the derived
// compaction state server-side (no re-summarization) so the next send-view is
// the full raw history. Same streaming/drain contract as a regen.
func startClearCompactedContextCmd(ag *agentclient.Client, convID string) tea.Cmd {
	return startContextStreamCmd(func(ctx context.Context) (<-chan agentclient.RegenProgress, error) {
		return ag.ClearCompactedContext(ctx, convID)
	}, "clearing compacted context — rehydrating from raw turns…")
}

// startContextStreamCmd opens a RegenerateContext-shaped stream via open and
// drains it one frame per message; first is the immediate feedback line shown
// before the first server frame arrives.
func startContextStreamCmd(open func(context.Context) (<-chan agentclient.RegenProgress, error), first string) tea.Cmd {
	return func() tea.Msg {
		ch, err := open(context.Background())
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
		return contextRegenProgressMsg{line: first, next: drain}
	}
}

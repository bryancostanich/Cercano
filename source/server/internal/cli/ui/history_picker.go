package ui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"cercano/source/server/internal/cli/agentclient"
	"cercano/source/server/internal/cli/overlay"
	"cercano/source/server/internal/cli/theme"
)

// resumeRequestedMsg is fired by the history picker when the user selects a
// conversation. The root model handles it by rehydrating scrollback +
// switching the active conversation id. Title is the picker's display title
// at selection time so the header shows the right name immediately.
type resumeRequestedMsg struct {
	ConversationID string
	Title          string
}

// historyPicker wraps overlay.RowList with conversation rows and a select hook
// that emits resumeRequestedMsg.
type historyPicker struct {
	width, height int
	palette       theme.Palette
	styles        theme.Styles
	agent         *agentclient.Client
	currentID     string // currently active conversation, marked as "current"
	list          overlay.RowList
}

func newHistoryPicker(ag *agentclient.Client, p theme.Palette, s theme.Styles, w, h int, currentID string) (historyPicker, tea.Cmd) {
	rows := buildHistoryRows(ag, currentID)
	hooks := overlay.Hooks{
		OnSelect: func(row overlay.Row) (string, bool, tea.Cmd) {
			// Close overlay and fire the resume message; the root model
			// handles the actual rehydration. Carry the displayed title
			// through so the header reflects the right name immediately.
			return "resuming " + row.Label, true, func() tea.Msg {
				return resumeRequestedMsg{ConversationID: row.Key, Title: row.Label}
			}
		},
	}
	return historyPicker{
		palette:   p,
		styles:    s,
		agent:     ag,
		width:     w,
		height:    h,
		currentID: currentID,
		list:      overlay.New("history", rows, hooks),
	}, nil
}

func (h historyPicker) Update(msg tea.KeyMsg) (historyPicker, tea.Cmd, bool) {
	next, cmd, closed := h.list.Update(msg, h.styles)
	h.list = next
	return h, cmd, closed
}

func (h historyPicker) View() string {
	return h.list.View(h.width, h.palette, h.styles)
}

// buildHistoryRows snapshots conversations into overlay rows. Empty state is
// represented as a single read-only row so the picker still renders.
func buildHistoryRows(ag *agentclient.Client, currentID string) []overlay.Row {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	convs, err := ag.ListConversations(ctx, "", 100)
	if err != nil {
		return []overlay.Row{{Label: "error", Value: err.Error(), ReadOnly: true}}
	}
	if len(convs) == 0 {
		return []overlay.Row{{Label: "(no saved conversations)", ReadOnly: true}}
	}
	rows := make([]overlay.Row, 0, len(convs))
	for _, c := range convs {
		title := c.Title
		if title == "" {
			title = "(untitled)"
		}
		summary := fmt.Sprintf("%d turn", c.TurnCount)
		if c.TurnCount != 1 {
			summary += "s"
		}
		summary += " · " + relativeTime(c.LastTurnAt)
		if c.Model != "" {
			summary += " · " + c.Model
		}
		hint := ""
		if c.ID == currentID {
			hint = "current"
		}
		rows = append(rows, overlay.Row{
			Key:   c.ID,
			Label: title,
			Value: summary,
			Hint:  hint,
		})
	}
	return rows
}

// relativeTime renders a coarse "5m ago" / "3h ago" / "2d ago" string. Cheap
// and good enough for the picker; no time formatting library needed.
func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < 60*time.Second:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2026-01-02")
	}
}

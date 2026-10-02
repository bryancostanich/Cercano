package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cercano/source/server/pkg/agentclient"
	tea "charm.land/bubbletea/v2"
)

type sessionModelMsg struct {
	convID string
	quiet  bool
	status agentclient.SessionModelStatus
	err    error
}

func sessionModelCmd(ag *agentclient.Client, convID, workDir, action, profile, model string, quiet bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if ag == nil {
			return sessionModelMsg{convID: convID, quiet: quiet, err: fmt.Errorf("agent unavailable")}
		}
		status, err := ag.SessionModel(ctx, convID, workDir, action, profile, model)
		return sessionModelMsg{convID: convID, quiet: quiet, status: status, err: err}
	}
}
func sessionModelText(status agentclient.SessionModelStatus, profiles bool) string {
	text := "Session main chat: normal routing (no override)."
	if r := status.Override; r != nil {
		text = fmt.Sprintf("Session main chat: %s / %s (no fallback).", r.Profile, r.Model)
	}
	text += "\n" + status.Note
	if profiles && len(status.Profiles) > 0 {
		var lines []string
		for _, p := range status.Profiles {
			lines = append(lines, fmt.Sprintf("  %s (%s; configured model: %s)", p.Name, p.Provider, p.Model))
		}
		text += "\nSaved profiles:\n" + strings.Join(lines, "\n")
	}
	if len(status.Models) > 0 {
		text += "\nModels:"
		for _, model := range status.Models {
			text += "\n  " + model.ID
		}
	}
	return text
}

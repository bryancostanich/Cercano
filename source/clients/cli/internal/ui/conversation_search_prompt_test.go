package ui

import (
	tea "charm.land/bubbletea/v2"
	"testing"
)

func TestConversationSearchPromptClickClosesSearch(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "normal"
		if pending {
			name = "pending_approval"
		}
		t.Run(name, func(t *testing.T) {
			m := searchModel(t)
			m.input.SetValue("draft")
			if pending {
				m.pendingConfirm = &confirmRequest{}
			}
			approval := m.pendingConfirm
			m.openConversationSearch("needle")
			settleSearch(t, &m)
			top := m.promptTop()
			m = sendSearch(t, m, tea.MouseClickMsg{X: 4, Y: top, Button: tea.MouseLeft})
			if m.search != nil || m.mainChat().search != nil {
				t.Fatal("prompt click must close search and remove highlights")
			}
			if m.input.Value() != "draft" {
				t.Fatal("prompt click changed draft")
			}
			if m.pendingConfirm != approval {
				t.Fatal("prompt click resolved pending approval")
			}
			m = sendSearch(t, m, tea.MouseReleaseMsg{X: 4, Y: m.promptTop(), Button: tea.MouseLeft})
			m = sendSearch(t, m, tea.KeyPressMsg{Code: 'z', Text: "z"})
			if pending {
				if m.input.Value() != "draft" || m.pendingConfirm != approval {
					t.Fatal("approval input guard lost")
				}
			} else if m.input.Value() == "draft" {
				t.Fatal("typing must return to regular prompt")
			}
		})
	}
}

func TestConversationSearchNonPromptClickKeepsSearch(t *testing.T) {
	m := searchModel(t)
	m.openConversationSearch("needle")
	settleSearch(t, &m)
	for _, click := range []tea.MouseClickMsg{
		{X: 4, Y: m.promptTop(), Button: tea.MouseRight},
		{X: 4, Y: m.scrollbarTop, Button: tea.MouseLeft},
	} {
		m = sendSearch(t, m, click)
		if m.search == nil {
			t.Fatal("only left clicking the prompt should close search")
		}
	}
}

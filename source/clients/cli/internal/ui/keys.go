package ui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// keyMap holds the cercano-cli key bindings matched via key.Matches in Update.
type keyMap struct {
	NavUp      key.Binding
	NavDown    key.Binding
	ToggleTool key.Binding
	Back       key.Binding
	ScrollKeys key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		NavUp:      key.NewBinding(key.WithKeys("up")),
		NavDown:    key.NewBinding(key.WithKeys("down")),
		ToggleTool: key.NewBinding(key.WithKeys("enter", "tab")),
		Back:       key.NewBinding(key.WithKeys("esc")),
		ScrollKeys: key.NewBinding(key.WithKeys(
			"pgup", "pgdown",
			"ctrl+u", "ctrl+d", "ctrl+b", "ctrl+f")),
	}
}

var keys = newKeyMap()

func isRuntimeDashboardKey(msg tea.KeyPressMsg) bool {
	k := msg.Key()
	return keyIs(k, 'm') && promptCommandMod(k)
}

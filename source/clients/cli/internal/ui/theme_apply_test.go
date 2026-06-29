package ui

import (
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

func TestChatSetStylesReplacesMarkdownRenderer(t *testing.T) {
	c := newChatView(theme.NewStyles(theme.Cracker()), theme.Cracker(), ".", ".", 80, 24)
	before := c.md
	c.SetStyles(theme.NewStyles(theme.BuiltinThemes()[1].Palette), theme.BuiltinThemes()[1].Palette)
	if c.md == before {
		t.Fatal("SetStyles must replace the markdown renderer (flush cache)")
	}
}

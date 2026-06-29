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

func TestRegistryIncludesCustomAndBuiltins(t *testing.T) {
	r := theme.NewRegistry(theme.BuiltinThemes())
	_ = r.Add(theme.Theme{Name: "mine", Palette: theme.Cracker()})
	names := r.Names()
	if names[0] != "cracker" || names[len(names)-1] != "mine" {
		t.Fatalf("registry order = %v", names)
	}
}

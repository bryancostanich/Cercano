package theme

import (
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
)

// Cracker palette hex — keep in sync with Cracker() in palette.go.
const (
	mdAmber  = "#EA8212"
	mdBright = "#FFB84D"
	mdLime   = "#BDF000"
	mdCyan   = "#00C8E8"
	mdMuted  = "#888888"
)

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }
func uintp(u uint) *uint    { return &u }

// CrackerMarkdownStyle returns a Glamour StyleConfig themed to the cracker
// palette. It starts from the bundled Dracula dark style (sane margins, code
// block layout, chroma theme) and recolors the leaf elements to amber/lime/cyan.
// Document margin is zeroed because renderEntry adds its own left indent.
func CrackerMarkdownStyle() ansi.StyleConfig {
	sc := styles.DraculaStyleConfig // struct copy; we replace leaf pointers, never mutate pointees

	sc.Document.Margin = uintp(0)
	sc.Document.Color = strp(mdAmber)

	sc.Heading.Color = strp(mdBright)
	sc.Heading.Bold = boolp(true)
	sc.H1.Color = strp(mdBright)
	sc.H1.Bold = boolp(true)
	sc.H2.Color = strp(mdBright)
	sc.H3.Color = strp(mdBright)

	// Drop the leading "#" markers — the bundled style encodes them as heading
	// prefixes. Clear prefix/suffix on every level so headings show as styled
	// text only.
	for _, h := range []*ansi.StyleBlock{&sc.Heading, &sc.H1, &sc.H2, &sc.H3, &sc.H4, &sc.H5, &sc.H6} {
		h.Prefix = ""
		h.Suffix = ""
		h.BlockPrefix = ""
		h.BlockSuffix = ""
	}

	sc.Strong.Color = strp(mdBright)
	sc.Strong.Bold = boolp(true)
	sc.Emph.Italic = boolp(true)

	sc.Code.Color = strp(mdCyan)

	sc.Item.Color = strp(mdLime)
	sc.Enumeration.Color = strp(mdLime)

	sc.Link.Color = strp(mdCyan)
	sc.Link.Underline = boolp(true)
	sc.LinkText.Color = strp(mdCyan)

	sc.HorizontalRule.Color = strp(mdMuted)

	return sc
}

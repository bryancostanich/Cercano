package theme

import (
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
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
	sc.Document.Color = strp(hexPrimary)

	sc.Heading.Color = strp(hexBright)
	sc.Heading.Bold = boolp(true)
	sc.H1.Color = strp(hexBright)
	sc.H1.Bold = boolp(true)
	sc.H2.Color = strp(hexBright)
	sc.H3.Color = strp(hexBright)

	// Drop the leading "#" markers — the bundled style encodes them as heading
	// prefixes. Clear prefix/suffix on every level so headings show as styled
	// text only.
	for _, h := range []*ansi.StyleBlock{&sc.Heading, &sc.H1, &sc.H2, &sc.H3, &sc.H4, &sc.H5, &sc.H6} {
		h.Prefix = ""
		h.Suffix = ""
		h.BlockPrefix = ""
		h.BlockSuffix = ""
	}

	sc.Strong.Color = strp(hexBright)
	sc.Strong.Bold = boolp(true)
	sc.Emph.Italic = boolp(true)

	// Inline code uses a darker, desaturated teal (bufCodeHex) — distinct from
	// links and calmer than the vivid chrome cyan (hexInfo).
	sc.Code.Color = strp(bufCodeHex)
	// Zero the code-block margin so the body aligns flush-left under the
	// horizontal rules we draw around it (see codeRule in the ui package).
	sc.CodeBlock.Margin = uintp(0)

	// List items (bullets and numbers) match normal paragraph text. Dracula
	// colors the whole list via List.Color (white) and ignores Item/Enumeration
	// .Color for the rendered text, so the block color is the only lever.
	sc.List.Color = strp(hexPrimary)

	sc.Link.Color = strp(bufLinkHex)
	sc.Link.Underline = boolp(true)
	sc.LinkText.Color = strp(bufLinkHex)

	sc.HorizontalRule.Color = strp(hexMuted)

	return sc
}

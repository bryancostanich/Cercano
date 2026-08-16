package theme

import (
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
)

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }
func uintp(u uint) *uint    { return &u }

// MarkdownStyle returns a Glamour StyleConfig themed to the given palette.
// It starts from the bundled Dracula dark style (sane margins, code block layout,
// chroma theme) and recolors the leaf elements to match the palette.
// Document margin is zeroed because renderEntry adds its own left indent.
func MarkdownStyle(p Palette) ansi.StyleConfig {
	sc := styles.DraculaStyleConfig // struct copy; we replace leaf pointers, never mutate pointees

	sc.Document.Margin = uintp(0)
	sc.Document.Color = strp(Hex(p.Primary))

	sc.Heading.Color = strp(Hex(p.Bright))
	sc.Heading.Bold = boolp(true)
	sc.H1.Color = strp(Hex(p.Bright))
	sc.H1.Bold = boolp(true)
	sc.H2.Color = strp(Hex(p.Bright))
	sc.H3.Color = strp(Hex(p.Bright))

	// Drop the leading "#" markers — the bundled style encodes them as heading
	// prefixes. Clear prefix/suffix on every level so headings show as styled
	// text only.
	for _, h := range []*ansi.StyleBlock{&sc.Heading, &sc.H1, &sc.H2, &sc.H3, &sc.H4, &sc.H5, &sc.H6} {
		h.Prefix = ""
		h.Suffix = ""
		h.BlockPrefix = ""
		h.BlockSuffix = ""
	}

	sc.Strong.Color = strp(Hex(p.Bright))
	sc.Strong.Bold = boolp(true)
	sc.Emph.Italic = boolp(true)
	sc.Emph.Color = strp(Hex(p.Primary))

	// Inline code uses the buffer code color — high enough contrast on the
	// charcoal background, and hue-distinct from both the amber prose and the
	// cyan links so code spans read as obviously different.
	sc.Code.Color = strp(Hex(p.BufferCode))
	// Zero the code-block margin so the body aligns flush-left under the
	// horizontal rules we draw around it (see codeRule in the ui package).
	// Chroma's bundled Dracula token colors are tuned for a dark canvas; make
	// that canvas explicit so light themes do not wash out code fences.
	sc.CodeBlock.Margin = uintp(0)
	sc.CodeBlock.BackgroundColor = strp(Hex(p.CodeBlockBg))

	// List items (bullets and numbers) match normal paragraph text. Dracula
	// colors the whole list via List.Color (white) and ignores Item/Enumeration
	// .Color for the rendered text, so the block color is the only lever.
	sc.List.Color = strp(Hex(p.Primary))

	sc.Link.Color = strp(Hex(p.BufferLink))
	sc.Link.Underline = boolp(true)
	sc.LinkText.Color = strp(Hex(p.BufferLink))

	sc.HorizontalRule.Color = strp(Hex(p.Muted))

	return sc
}

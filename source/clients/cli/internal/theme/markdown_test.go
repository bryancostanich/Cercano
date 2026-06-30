package theme

import "testing"

func TestMarkdownStyleUsesPaletteColors(t *testing.T) {
	sc := MarkdownStyle(Cracker())
	if sc.Document.Color == nil || *sc.Document.Color != "#ea8212" {
		t.Fatalf("Document.Color = %v, want #ea8212", sc.Document.Color)
	}
	if sc.Code.Color == nil || *sc.Code.Color != "#b7a6e0" {
		t.Fatalf("Code.Color = %v, want #b7a6e0", sc.Code.Color)
	}
}

func TestCrackerMarkdownStyle_KeyColorsSet(t *testing.T) {
	sc := MarkdownStyle(Cracker())
	if sc.Heading.Color == nil || *sc.Heading.Color != "#ffb84d" {
		t.Fatalf("heading color = %v, want #ffb84d", sc.Heading.Color)
	}
	// Inline code uses the darker desaturated teal — distinct from links and
	// calmer than the vivid chrome cyan (see palette.go).
	if sc.Code.Color == nil || *sc.Code.Color != "#b7a6e0" {
		t.Fatalf("inline code color = %v, want #b7a6e0", sc.Code.Color)
	}
	// Links keep the brighter muted cyan — a different named color from code.
	if sc.Link.Color == nil || *sc.Link.Color != "#2ea8bc" {
		t.Fatalf("link color = %v, want #2ea8bc", sc.Link.Color)
	}
	if sc.Link.Underline == nil || !*sc.Link.Underline {
		t.Fatalf("link should be underlined")
	}
	if sc.Document.Margin == nil || *sc.Document.Margin != 0 {
		t.Fatalf("document margin = %v, want 0 (we add our own indent)", sc.Document.Margin)
	}
}

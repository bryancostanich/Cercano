package theme

import "testing"

func TestCrackerMarkdownStyle_KeyColorsSet(t *testing.T) {
	sc := CrackerMarkdownStyle()
	if sc.Heading.Color == nil || *sc.Heading.Color != "#FFB84D" {
		t.Fatalf("heading color = %v, want #FFB84D", sc.Heading.Color)
	}
	// Inline code uses the darker desaturated teal — distinct from links and
	// calmer than the vivid chrome cyan (see palette.go).
	if sc.Code.Color == nil || *sc.Code.Color != bufCodeHex {
		t.Fatalf("inline code color = %v, want %s", sc.Code.Color, bufCodeHex)
	}
	// Links keep the brighter muted cyan — a different named color from code.
	if sc.Link.Color == nil || *sc.Link.Color != bufLinkHex {
		t.Fatalf("link color = %v, want %s", sc.Link.Color, bufLinkHex)
	}
	if sc.Link.Underline == nil || !*sc.Link.Underline {
		t.Fatalf("link should be underlined")
	}
	if sc.Document.Margin == nil || *sc.Document.Margin != 0 {
		t.Fatalf("document margin = %v, want 0 (we add our own indent)", sc.Document.Margin)
	}
}

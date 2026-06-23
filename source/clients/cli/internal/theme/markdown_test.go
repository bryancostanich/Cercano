package theme

import "testing"

func TestCrackerMarkdownStyle_KeyColorsSet(t *testing.T) {
	sc := CrackerMarkdownStyle()
	if sc.Heading.Color == nil || *sc.Heading.Color != "#FFB84D" {
		t.Fatalf("heading color = %v, want #FFB84D", sc.Heading.Color)
	}
	if sc.Code.Color == nil || *sc.Code.Color != "#00C8E8" {
		t.Fatalf("inline code color = %v, want #00C8E8", sc.Code.Color)
	}
	if sc.Link.Underline == nil || !*sc.Link.Underline {
		t.Fatalf("link should be underlined")
	}
	if sc.Document.Margin == nil || *sc.Document.Margin != 0 {
		t.Fatalf("document margin = %v, want 0 (we add our own indent)", sc.Document.Margin)
	}
}

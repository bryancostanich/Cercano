package slash

import "testing"

func TestResolveColorToken(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		// Hex forms.
		{"#EA8212", "#EA8212", true},
		{"#ea8212", "#EA8212", true},
		{"ea8212", "#EA8212", true},
		{"EA8212", "#EA8212", true},
		// Palette aliases.
		{"primary", "palette:primary", true},
		{"amber", "palette:primary", true},
		{"accent", "palette:accent", true},
		{"lime", "palette:accent", true},
		{"cyan", "palette:info", true},
		{"info", "palette:info", true},
		{"green", "palette:success", true},
		{"red", "palette:error", true},
		{"yellow", "palette:warn", true},
		{"gray", "palette:muted", true},
		{"grey", "palette:muted", true},
		{"bright", "palette:bright", true},
		{"border", "palette:border", true},
		{"border-dim", "palette:border_dim", true},
		// Case insensitivity.
		{"LIME", "palette:accent", true},
		{"  Amber  ", "palette:primary", true},
		// Rejections.
		{"", "", false},
		{"banana", "", false},
		{"#XYZ123", "", false},
		{"#EA821", "", false},   // 5 digits
		{"#EA82122", "", false}, // 7 digits
	}
	for _, c := range cases {
		got, ok := ResolveColorToken(c.in)
		if ok != c.ok {
			t.Errorf("ResolveColorToken(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if got != c.want {
			t.Errorf("ResolveColorToken(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlash_Color_NoArgs_ShowsUsage(t *testing.T) {
	r := New()
	RegisterColor(r)
	res, _ := r.Dispatch("/color")
	if res.Kind != ResultText {
		t.Fatalf("kind: got %v want ResultText", res.Kind)
	}
	if res.Text == "" {
		t.Errorf("expected usage hint")
	}
}

func TestSlash_Color_PaletteName_DispatchesSetColor(t *testing.T) {
	r := New()
	RegisterColor(r)
	res, _ := r.Dispatch("/color lime")
	if res.Kind != ResultSetPromptColor {
		t.Errorf("kind: got %v want ResultSetPromptColor", res.Kind)
	}
	if res.Text != "palette:accent" {
		t.Errorf("text: got %q want palette:accent", res.Text)
	}
}

func TestSlash_Color_Hex_DispatchesSetColor(t *testing.T) {
	r := New()
	RegisterColor(r)
	res, _ := r.Dispatch("/color #ff8800")
	if res.Kind != ResultSetPromptColor {
		t.Errorf("kind: got %v want ResultSetPromptColor", res.Kind)
	}
	if res.Text != "#FF8800" {
		t.Errorf("text: got %q want #FF8800", res.Text)
	}
}

func TestSlash_Color_UnknownToken_ReturnsError(t *testing.T) {
	r := New()
	RegisterColor(r)
	res, _ := r.Dispatch("/color banana")
	if res.Kind != ResultText {
		t.Errorf("kind: got %v want ResultText", res.Kind)
	}
	if res.Text == "" {
		t.Errorf("expected an error message")
	}
}

package theme

import "testing"

func TestThemeYAMLRoundTrip(t *testing.T) {
	in := Theme{Name: "mine", Palette: Cracker()}
	in.Palette.Accent = mustHex("#123456")
	data, err := MarshalTheme(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := UnmarshalTheme("mine", data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if HexOf(out.Palette.Accent) != "#123456" {
		t.Fatalf("accent round-trip = %s", HexOf(out.Palette.Accent))
	}
	if HexOf(out.Palette.Primary) != HexOf(Cracker().Primary) {
		t.Fatalf("primary should survive round-trip")
	}
}

func TestUnmarshalMissingKeyFallsBack(t *testing.T) {
	out, err := UnmarshalTheme("partial", []byte("colors:\n  accent: \"#abcdef\"\n"))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if HexOf(out.Palette.Accent) != "#abcdef" {
		t.Fatalf("accent = %s", HexOf(out.Palette.Accent))
	}
	if HexOf(out.Palette.Primary) != HexOf(Cracker().Primary) {
		t.Fatalf("missing primary should fall back to cracker")
	}
}

func mustHex(s string) (c interface {
	RGBA() (uint32, uint32, uint32, uint32)
}) {
	col, err := ParseHex(s)
	if err != nil {
		panic(err)
	}
	return col
}

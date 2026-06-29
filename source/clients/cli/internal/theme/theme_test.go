package theme

import "testing"

func TestHexRoundTrip(t *testing.T) {
	c, err := ParseHex("#ea8212")
	if err != nil {
		t.Fatalf("ParseHex error: %v", err)
	}
	if got := HexOf(c); got != "#ea8212" {
		t.Fatalf("HexOf round-trip = %q, want #ea8212", got)
	}
}

func TestParseHexRejectsBad(t *testing.T) {
	for _, bad := range []string{"", "ea8212", "#xyzxyz", "#12345", "#1234567"} {
		if _, err := ParseHex(bad); err == nil {
			t.Fatalf("ParseHex(%q) should error", bad)
		}
	}
}

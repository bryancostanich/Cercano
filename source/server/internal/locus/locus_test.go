package locus

import "testing"

func TestParseMode(t *testing.T) {
	cases := map[string]Mode{
		"":              LocalPrimary,
		"local_primary": LocalPrimary,
		"cloud_only":    CloudOnly,
		"cloud_primary": CloudPrimary,
		"local_only":    LocalOnly,
	}
	for in, want := range cases {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseMode("nonsense"); err == nil {
		t.Error("expected error for invalid mode")
	}
}

func TestMainResolution(t *testing.T) {
	cases := []struct {
		mode  Mode
		pref  Tier
		fall  Tier
		cross bool
	}{
		{CloudOnly, TierCloud, TierCloud, false},
		{CloudPrimary, TierCloud, TierLocal, true},
		{LocalPrimary, TierLocal, TierCloud, true},
		{LocalOnly, TierLocal, TierLocal, false},
	}
	for _, c := range cases {
		r := c.mode.Main()
		if r.Preferred != c.pref || r.Fallback != c.fall || r.CrossAllowed != c.cross {
			t.Errorf("%s.Main() = %+v; want pref=%v fall=%v cross=%v", c.mode, r, c.pref, c.fall, c.cross)
		}
	}
}

func TestCoprocResolution(t *testing.T) {
	cases := []struct {
		mode  Mode
		pref  Tier
		fall  Tier
		cross bool
	}{
		{CloudOnly, TierCloud, TierCloud, false},
		{CloudPrimary, TierLocal, TierCloud, true}, // differs from Main(): local-preferred
		{LocalPrimary, TierLocal, TierCloud, true},
		{LocalOnly, TierLocal, TierLocal, false},
	}
	for _, c := range cases {
		r := c.mode.Coproc()
		if r.Preferred != c.pref || r.Fallback != c.fall || r.CrossAllowed != c.cross {
			t.Errorf("%s.Coproc() = %+v; want pref=%v fall=%v cross=%v", c.mode, r, c.pref, c.fall, c.cross)
		}
	}
}

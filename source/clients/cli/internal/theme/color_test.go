package theme

import (
	"image/color"
	"math"
	"testing"
)

func TestSelectionBackgroundSGRUsesPaletteToken(t *testing.T) {
	p := Cracker()
	p.SelectionBg = mustHex("#123456")
	if got := SelectionBackgroundSGR(p); got != "\x1b[48;2;18;52;86m" {
		t.Fatalf("SelectionBackgroundSGR = %q", got)
	}
}

func TestMeterLabelForegroundChoosesReadableContrastPerCell(t *testing.T) {
	daylight := paletteByNameForColorTest("daylight")
	filledBg := daylight.ActivityPeak
	if got := Hex(MeterLabelForeground(daylight, filledBg, true)); got != "#fbf3e0" {
		t.Fatalf("daylight meter label over dark filled cell = %s, want #fbf3e0", got)
	}
	emptyBg := Fade(daylight.Dim, 0.45)
	if got := Hex(MeterLabelForeground(daylight, emptyBg, false)); got != "#fbf3e0" {
		t.Fatalf("daylight meter label over dark empty cell = %s, want #fbf3e0", got)
	}
}

func paletteByNameForColorTest(name string) Palette {
	for _, builtin := range BuiltinThemes() {
		if builtin.Name == name {
			return builtin.Palette
		}
	}
	return Cracker()
}

func TestBuiltinThemeTextColorsMeetContrastBaseline(t *testing.T) {
	type role struct {
		name        string
		color       func(Palette) color.Color
		minContrast float64
	}
	roles := []role{
		{name: "primary", color: func(p Palette) color.Color { return p.Primary }, minContrast: 4.5},
		{name: "bright", color: func(p Palette) color.Color { return p.Bright }, minContrast: 4.5},
		{name: "accent", color: func(p Palette) color.Color { return p.Accent }, minContrast: 4.5},
		{name: "info", color: func(p Palette) color.Color { return p.Info }, minContrast: 4.5},
		{name: "success", color: func(p Palette) color.Color { return p.Success }, minContrast: 4.5},
		{name: "warn", color: func(p Palette) color.Color { return p.Warn }, minContrast: 4.5},
		{name: "error", color: func(p Palette) color.Color { return p.Error }, minContrast: 4.5},
		{name: "buffer_link", color: func(p Palette) color.Color { return p.BufferLink }, minContrast: 4.5},
		{name: "buffer_code", color: func(p Palette) color.Color { return p.BufferCode }, minContrast: 4.5},
		{name: "buffer_lime", color: func(p Palette) color.Color { return p.BufferLime }, minContrast: 4.5},
		{name: "buffer_error", color: func(p Palette) color.Color { return p.BufferError }, minContrast: 4.5},
		{name: "selection_caret", color: func(p Palette) color.Color { return p.SelectionCaret }, minContrast: 4.5},
		{name: "muted", color: func(p Palette) color.Color { return p.Muted }, minContrast: 3.0},
	}
	for _, builtin := range BuiltinThemes() {
		for _, r := range roles {
			got := contrastRatio(r.color(builtin.Palette), builtin.Palette.BgDeep)
			if got < r.minContrast {
				t.Fatalf("%s %s contrast on BgDeep = %.2f, want >= %.2f", builtin.Name, r.name, got, r.minContrast)
			}
		}
	}
}

func TestDaylightTextTokensAvoidSaturatedYellowHue(t *testing.T) {
	daylight := paletteByNameForColorTest("daylight")
	tokens := []struct {
		name  string
		color color.Color
	}{
		{name: "primary", color: daylight.Primary},
		{name: "bright", color: daylight.Bright},
		{name: "warn", color: daylight.Warn},
		{name: "wordmark_peak", color: daylight.WordmarkPeak},
		{name: "spinner_peak", color: daylight.SpinnerPeak},
		{name: "meter_label_on_fill", color: daylight.MeterLabelOnFill},
	}
	for _, token := range tokens {
		hue, saturation := hueSaturation(token.color)
		if hue >= 30 && hue <= 65 && saturation >= 0.5 {
			t.Fatalf("daylight %s is saturated yellow/orange (hue %.1f sat %.2f): %s", token.name, hue, saturation, Hex(token.color))
		}
	}
}

func hueSaturation(c color.Color) (hue, saturation float64) {
	rgb := RGB(c)
	r := float64(rgb[0]) / 255.0
	g := float64(rgb[1]) / 255.0
	b := float64(rgb[2]) / 255.0
	maxc := math.Max(r, math.Max(g, b))
	minc := math.Min(r, math.Min(g, b))
	delta := maxc - minc
	if maxc == 0 {
		return 0, 0
	}
	saturation = delta / maxc
	if delta == 0 {
		return 0, saturation
	}
	switch maxc {
	case r:
		hue = 60 * math.Mod((g-b)/delta, 6)
	case g:
		hue = 60 * ((b-r)/delta + 2)
	case b:
		hue = 60 * ((r-g)/delta + 4)
	}
	if hue < 0 {
		hue += 360
	}
	return hue, saturation
}

func TestActivityAndSpinnerColorsUsePaletteTokens(t *testing.T) {
	p := Cracker()
	p.ActivityBase = mustHex("#010203")
	p.ActivityPeak = mustHex("#111213")
	if got := Hex(ActivityColorAt(p, 10, 0, 4)); got != "#010203" {
		t.Fatalf("ActivityColorAt outside sweep = %s", got)
	}
	if got := Hex(ActivityColorAt(p, 0, 0, 4)); got != "#111213" {
		t.Fatalf("ActivityColorAt at peak = %s", got)
	}

	p.SpinnerBase = mustHex("#202122")
	p.SpinnerPeak = mustHex("#303132")
	if got := Hex(SpinnerColorAt(p, 0)); got != "#202122" {
		t.Fatalf("SpinnerColorAt base = %s", got)
	}
	if got := Hex(SpinnerColorAt(p, 1)); got != "#303132" {
		t.Fatalf("SpinnerColorAt peak = %s", got)
	}
}

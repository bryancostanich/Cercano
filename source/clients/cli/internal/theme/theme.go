package theme

import (
	"fmt"
	"image/color"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

// Theme is a named complete color set. Palette holds every cercano-cli color.
type Theme struct {
	Name    string
	Palette Palette
}

var hexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ParseHex converts "#RRGGBB" to a color, rejecting any other shape.
func ParseHex(s string) (color.Color, error) {
	s = strings.TrimSpace(s)
	if !hexRe.MatchString(s) {
		return nil, fmt.Errorf("invalid hex color %q (want #RRGGBB)", s)
	}
	return lipgloss.Color(strings.ToLower(s)), nil
}

// HexOf renders a color back to lowercase "#RRGGBB".
func HexOf(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

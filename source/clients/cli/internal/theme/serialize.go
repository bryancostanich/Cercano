package theme

import (
	"image/color"

	"gopkg.in/yaml.v3"
)

type themeFile struct {
	Colors map[string]string `yaml:"colors"`
}

// colorFields maps a stable yaml key to a pointer into a Palette. Used by both
// marshal and unmarshal so the two can never drift.
func colorFields(p *Palette) map[string]*color.Color {
	return map[string]*color.Color{
		"bg_deep": &p.BgDeep, "surface": &p.Surface, "border_dim": &p.BorderDim, "border": &p.Border,
		"primary": &p.Primary, "bright": &p.Bright, "dim_amber": &p.DimAmber, "accent": &p.Accent,
		"info": &p.Info, "muted": &p.Muted, "success": &p.Success, "warn": &p.Warn, "error": &p.Error,
		"buffer_link": &p.BufferLink, "buffer_code": &p.BufferCode, "buffer_lime": &p.BufferLime,
		"buffer_error": &p.BufferError, "buffer_user_bg": &p.BufferUserBg,
	}
}

// FieldPtr returns a pointer to the palette color for a yaml key, or nil.
func FieldPtr(p *Palette, key string) *color.Color { return colorFields(p)[key] }

// MarshalTheme serializes a theme's colors to YAML.
func MarshalTheme(t Theme) ([]byte, error) {
	p := t.Palette
	fields := colorFields(&p)
	out := themeFile{Colors: make(map[string]string, len(fields))}
	for key, ptr := range fields {
		out.Colors[key] = HexOf(*ptr)
	}
	return yaml.Marshal(out)
}

// UnmarshalTheme parses YAML into a Theme. Missing or invalid color keys fall
// back to the cracker value for that field, so a partial/old file still loads.
func UnmarshalTheme(name string, data []byte) (Theme, error) {
	var tf themeFile
	if err := yaml.Unmarshal(data, &tf); err != nil {
		return Theme{}, err
	}
	p := Cracker() // defaults
	fields := colorFields(&p)
	for key, ptr := range fields {
		if hex, ok := tf.Colors[key]; ok {
			if c, err := ParseHex(hex); err == nil {
				*ptr = c
			}
		}
	}
	return Theme{Name: name, Palette: p}, nil
}

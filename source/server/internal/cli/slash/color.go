package slash

import (
	"strings"
)

// RegisterColor wires /color — sets the color of the lines above and below
// the prompt area. Accepts palette names (primary, accent/lime, info/cyan,
// success/green, warn/yellow, error/red, muted/gray, bright, bordered hot/
// dim) or hex codes (#RRGGBB or RRGGBB).
//
// Algorithmic — no LLM involved in parsing.
func RegisterColor(r *Registry) {
	r.Register(Command{
		Name: "color",
		Help: "Set the color of the prompt-area border lines. Usage: /color <name|#hex>. Names: primary, accent, lime, cyan, info, success, green, warn, yellow, error, red, muted, gray, bright, border, border-dim.",
		Handler: func(args []string) Result {
			if len(args) == 0 {
				return Result{Kind: ResultText, Text: "usage: /color <name|#hex>  e.g. /color lime, /color #FF8800"}
			}
			raw := strings.TrimSpace(args[0])
			parsed, ok := ResolveColorToken(raw)
			if !ok {
				return Result{Kind: ResultText, Text: "unrecognised color: " + raw + " (try a palette name or #RRGGBB)"}
			}
			return Result{Kind: ResultSetPromptColor, Text: parsed}
		},
	})
}

// ResolveColorToken normalizes a user-supplied color token into a value the
// model knows how to use:
//
//   - a leading `#` followed by 6 hex digits → returned as `#RRGGBB`
//   - a bare 6-hex string → prefixed with `#`
//   - a recognised palette key → returned as `palette:<key>`
//
// Returns false on unknown input. The model side reads the prefix to decide
// whether to use a literal hex or a palette lookup.
func ResolveColorToken(raw string) (string, bool) {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "" {
		return "", false
	}
	// Hex form.
	hex := strings.TrimPrefix(s, "#")
	if len(hex) == 6 && allHex(hex) {
		return "#" + strings.ToUpper(hex), true
	}
	// Palette keys — alias common color words to the palette field that best
	// represents them.
	switch s {
	case "primary", "amber":
		return "palette:primary", true
	case "accent", "lime":
		return "palette:accent", true
	case "info", "cyan":
		return "palette:info", true
	case "success", "green":
		return "palette:success", true
	case "warn", "warning", "yellow":
		return "palette:warn", true
	case "error", "red":
		return "palette:error", true
	case "muted", "gray", "grey":
		return "palette:muted", true
	case "bright":
		return "palette:bright", true
	case "border":
		return "palette:border", true
	case "border-dim", "dim", "border_dim":
		return "palette:border_dim", true
	}
	return "", false
}

func allHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

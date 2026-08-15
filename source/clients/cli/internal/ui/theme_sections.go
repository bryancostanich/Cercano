package ui

import (
	"cercano/source/clients/cli/internal/form"
	"cercano/source/clients/cli/internal/theme"
)

// chromeColorRows / contentColorRows list the editable palette fields in display
// order, paired with their yaml key (matching theme.colorFields) and label.
var chromeColorRows = []struct{ key, label string }{
	{"bg_deep", "background"}, {"surface", "surface"}, {"border_dim", "border-dim"}, {"border", "border"},
	{"primary", "primary"}, {"bright", "bright"}, {"dim", "dim"}, {"accent", "accent"},
	{"info", "info"}, {"muted", "muted"}, {"success", "success"}, {"warn", "warn"}, {"error", "error"},
}

var contentColorRows = []struct{ key, label string }{
	{"buffer_link", "link"}, {"buffer_code", "code"}, {"buffer_lime", "tool-ok"},
	{"buffer_error", "tool-err"}, {"buffer_user_bg", "user-bg"},
}

var semanticColorRows = []struct{ key, label string }{
	{"selection_bg", "selection-bg"}, {"code_block_bg", "code-bg"}, {"bypass_text", "bypass-text"},
	{"activity_base", "activity-base"}, {"activity_peak", "activity-peak"},
	{"spinner_base", "spinner-base"}, {"spinner_peak", "spinner-peak"},
	{"meter_label_on_fill", "meter-label"},
}

// paletteHex returns the #RRGGBB hex for a yaml color key of a palette.
func paletteHex(p theme.Palette, key string) string {
	pc := p
	if ptr := theme.FieldPtr(&pc, key); ptr != nil {
		return theme.HexOf(*ptr)
	}
	return ""
}

// buildThemeSections builds the four Theme-* sections for the settings form.
func buildThemeSections(working theme.Theme, names []string, builtin, dirty bool) []form.Section {
	editable := !builtin
	chrome := make([]form.Field, 0, len(chromeColorRows))
	for _, r := range chromeColorRows {
		chrome = append(chrome, form.NewColor("color:"+r.key, r.label, paletteHex(working.Palette, r.key), editable))
	}
	content := make([]form.Field, 0, len(contentColorRows))
	for _, r := range contentColorRows {
		content = append(content, form.NewColor("color:"+r.key, r.label, paletteHex(working.Palette, r.key), editable))
	}
	semantic := make([]form.Field, 0, len(semanticColorRows))
	for _, r := range semanticColorRows {
		semantic = append(semantic, form.NewColor("color:"+r.key, r.label, paletteHex(working.Palette, r.key), editable))
	}
	themeRow := form.NewSelect("theme-select", "theme", optionsFromNames(names), working.Name)
	actions := []form.Field{
		form.NewButton("theme-save", "Save", editable && dirty),
		form.NewText("theme-save-as", "save as", "", "type a name, enter to clone"),
		form.NewButton("theme-delete", "Delete", editable),
		form.NewText("theme-import", "import", "", "path to a .yaml theme"),
	}
	return []form.Section{
		{Title: "Theme", Fields: []form.Field{themeRow}},
		{Title: "Theme · Chrome", Fields: chrome},
		{Title: "Theme · Content", Fields: content},
		{Title: "Theme · Semantic", Fields: semantic},
		{Title: "Theme · Actions", Fields: actions},
	}
}

func optionsFromNames(names []string) []form.Option {
	out := make([]form.Option, len(names))
	for i, n := range names {
		out[i] = form.Option{Label: n, Value: n}
	}
	return out
}

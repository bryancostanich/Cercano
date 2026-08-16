package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

type subAgentStartEntry struct {
	Kind     string
	Title    string
	ConvID   string
	Route    string
	Provider string
	Model    string
	Tier     string
	Tools    []string
	Task     string
}

func updateSubAgentStartCard(card *subAgentStartEntry, ev subAgentEventMsg) bool {
	if card == nil {
		return false
	}
	switch ev.kind {
	case "started":
		fields := parseLifecycleFields(ev.text)
		if v := fields["conv"]; v != "" {
			card.ConvID = v
		}
		if v := fields["route"]; v != "" {
			card.Route = v
		}
		if v := fields["provider"]; v != "" {
			card.Provider = v
		}
		if v := fields["model"]; v != "" {
			card.Model = v
		}
		if v := fields["tier"]; v != "" {
			card.Tier = v
		}
		if v := fields["tools"]; v != "" && len(card.Tools) == 0 {
			card.Tools = splitToolList(v)
		}
		return true
	case "prompt":
		card.Task = strings.TrimSpace(ev.text)
		return true
	default:
		return false
	}
}

func parseLifecycleFields(text string) map[string]string {
	out := map[string]string{}
	text = strings.TrimSpace(text)
	if i := strings.Index(text, ":"); i >= 0 {
		text = strings.TrimSpace(text[i+1:])
	}
	for _, part := range strings.Fields(text) {
		k, v, ok := strings.Cut(part, "=")
		if !ok || k == "" {
			continue
		}
		out[k] = strings.Trim(v, `"`)
	}
	return out
}

func splitToolList(s string) []string {
	parts := strings.Split(s, ",")
	tools := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			tools = append(tools, p)
		}
	}
	return tools
}

func (c *chatView) renderSubAgentStartCard(card *subAgentStartEntry, maxWidth int) string {
	if card == nil {
		return ""
	}
	outerW := maxWidth
	if outerW > 100 {
		outerW = 100
	}
	if outerW < 28 {
		outerW = 28
	}
	innerW := outerW - 4 // borders plus one space of padding on each side.
	border := c.styles.Bright
	labelStyle := c.styles.Info
	valueStyle := c.styles.Primary
	accent := c.styles.Accent
	muted := c.styles.Muted

	var rows []string
	row := func(content string) {
		pad := innerW - lipgloss.Width(content)
		if pad < 0 {
			pad = 0
		}
		rows = append(rows, border.Render("│")+" "+content+strings.Repeat(" ", pad)+" "+border.Render("│"))
	}
	blank := func() { row("") }
	kv := func(label, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		labelW := 8
		prefix := labelStyle.Render(padVisible(label, labelW)) + " "
		wrapW := innerW - labelW - 1
		if wrapW < 8 {
			wrapW = 8
		}
		for i, line := range wrapPlain(value, wrapW) {
			if i == 0 {
				row(prefix + valueStyle.Render(line))
			} else {
				row(strings.Repeat(" ", labelW+1) + valueStyle.Render(line))
			}
		}
	}

	title := strings.TrimSpace(card.Kind + " " + card.Title + " started")
	if title == "started" {
		title = "Sub-agent started"
	}
	topTitle := "─ " + title + " "
	if lipgloss.Width(topTitle) > outerW-2 {
		topTitle = "─ " + ellipsizeVisible(title, outerW-5) + " "
	}
	topFill := outerW - 2 - lipgloss.Width(topTitle)
	if topFill < 0 {
		topFill = 0
	}
	rows = append(rows, border.Render("╭"+topTitle+strings.Repeat("─", topFill)+"╮"))

	if card.Route != "" || card.Provider != "" {
		route := card.Route
		if card.Provider != "" {
			if route != "" {
				route += " / "
			}
			route += card.Provider
		}
		kv("Route", route)
	}
	kv("Model", card.Model)
	kv("Tier", card.Tier)
	if len(card.Tools) > 0 {
		kv("Tools", strings.Join(card.Tools, ", "))
	}
	if card.Task != "" {
		blank()
		row(labelStyle.Render("Task"))
		taskW := innerW - 2
		if taskW < 8 {
			taskW = 8
		}
		rail := accent.Render("│")
		for _, line := range wrapPlain(card.Task, taskW) {
			row(rail + " " + valueStyle.Render(line))
		}
	} else {
		blank()
		row(muted.Render("waiting for delegated task…"))
	}

	rows = append(rows, border.Render("╰"+strings.Repeat("─", outerW-2)+"╯"))
	return strings.Join(rows, "\n")
}

func padVisible(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func ellipsizeVisible(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	var b strings.Builder
	for _, r := range s {
		if lipgloss.Width(b.String()+string(r)+"…") > width {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + "…"
}

func wrapPlain(text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{""}
	}
	var out []string
	for _, para := range strings.Split(text, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, word := range words {
			for lipgloss.Width(word) > width {
				cut := takeVisible(word, width)
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, cut)
				word = strings.TrimPrefix(word, cut)
			}
			if line == "" {
				line = word
			} else if lipgloss.Width(line)+1+lipgloss.Width(word) <= width {
				line += " " + word
			} else {
				out = append(out, line)
				line = word
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func takeVisible(s string, width int) string {
	var b strings.Builder
	for _, r := range s {
		if lipgloss.Width(b.String()+string(r)) > width {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

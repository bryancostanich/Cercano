package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/theme"
)

// mcpAddSubmit is the parsed payload emitted when the add-server form is
// submitted successfully.
type mcpAddSubmit struct {
	name    string
	command string
	args    []string
	env     map[string]string
}

// mcpAddForm is a small centered popover for registering a new MCP server.
// It uses a minimal local single-line field editor rather than pulling in the
// textinput widget — four short fields don't need cursor movement, selection,
// or scrolling.
type mcpAddForm struct {
	palette theme.Palette
	styles  theme.Styles

	labels []string
	values []string
	focus  int

	errMsg string
}

const (
	mcpFieldName = iota
	mcpFieldCommand
	mcpFieldArgs
	mcpFieldEnv
	mcpFieldCount
)

func newMcpAddForm(p theme.Palette, s theme.Styles) *mcpAddForm {
	return &mcpAddForm{
		palette: p,
		styles:  s,
		labels:  []string{"name", "command", "args", "env"},
		values:  make([]string, mcpFieldCount),
		focus:   mcpFieldName,
	}
}

// Update handles a key. Returns (cmd, closed, submit):
//   - closed==true  → the caller should drop the form (cancel).
//   - submit!=nil   → the caller should add the server and drop the form.
func (f *mcpAddForm) Update(msg tea.KeyPressMsg) (tea.Cmd, bool, *mcpAddSubmit) {
	switch msg.String() {
	case "esc":
		return nil, true, nil
	case "tab", "down":
		f.focus = (f.focus + 1) % mcpFieldCount
		return nil, false, nil
	case "shift+tab", "up":
		f.focus = (f.focus - 1 + mcpFieldCount) % mcpFieldCount
		return nil, false, nil
	case "enter":
		sub, ok := f.submit()
		if !ok {
			return nil, false, nil
		}
		return nil, false, sub
	case "backspace":
		v := f.values[f.focus]
		if v != "" {
			f.values[f.focus] = v[:len(v)-1]
		}
		return nil, false, nil
	}
	// Printable text: append the literal typed characters. msg.Text carries
	// the actual runes (including space); msg.String() word-names special keys
	// like "space"/"enter", so it must not be the source here.
	if t := msg.Text; t != "" {
		f.values[f.focus] += t
	}
	return nil, false, nil
}

// paste inserts bracketed-paste text into the focused field. The fields are
// single-line, so ANSI is stripped and any newlines/tabs are flattened to
// spaces to keep the value on one row (and to keep args/env token-splitting
// sane). Reports whether anything was inserted.
func (f *mcpAddForm) paste(text string) bool {
	clean := ansi.Strip(text)
	clean = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ").Replace(clean)
	if clean == "" {
		return false
	}
	f.values[f.focus] += clean
	return true
}

// submit validates and parses the fields. Returns ok=false (setting errMsg)
// when required fields are missing.
func (f *mcpAddForm) submit() (*mcpAddSubmit, bool) {
	name := strings.TrimSpace(f.values[mcpFieldName])
	command := strings.TrimSpace(f.values[mcpFieldCommand])
	if name == "" {
		f.errMsg = "name is required"
		f.focus = mcpFieldName
		return nil, false
	}
	if command == "" {
		f.errMsg = "command is required"
		f.focus = mcpFieldCommand
		return nil, false
	}
	f.errMsg = ""

	// Forgiving round-trip: the details popover's "copy command" flattens
	// command+args into one shell-style line, and users naturally paste that
	// whole line back into the command field. If the command field holds a
	// full command line (has whitespace) and args is empty, split it — first
	// token is the executable, the rest are args. Without this, the whole line
	// lands in `command` and exec() fails looking for a file named after it.
	// Only auto-split when args is empty so anyone who filled both fields
	// deliberately is left untouched.
	args := parseMcpArgs(f.values[mcpFieldArgs])
	if len(args) == 0 {
		if fields := strings.Fields(command); len(fields) > 1 {
			command = fields[0]
			args = fields[1:]
		}
	}

	return &mcpAddSubmit{
		name:    name,
		command: command,
		args:    args,
		env:     parseMcpEnv(f.values[mcpFieldEnv]),
	}, true
}

// parseMcpArgs splits on whitespace, dropping empties.
func parseMcpArgs(s string) []string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// parseMcpEnv parses whitespace-separated K=V tokens into a map. Malformed
// tokens (no '=' or empty key) are skipped.
func parseMcpEnv(s string) map[string]string {
	out := map[string]string{}
	for _, tok := range strings.Fields(s) {
		k, v, ok := strings.Cut(tok, "=")
		if !ok || strings.TrimSpace(k) == "" {
			continue
		}
		out[strings.TrimSpace(k)] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (f *mcpAddForm) View() string {
	const boxW = 52
	fieldW := boxW - 4

	var b strings.Builder
	b.WriteString(f.styles.Bright.Render("Add MCP server"))
	b.WriteString("\n\n")

	for i := range f.labels {
		label := f.labels[i]
		val := f.values[i]
		labelStyle := f.styles.Muted
		if i == f.focus {
			labelStyle = f.styles.Accent
		}
		b.WriteString(labelStyle.Render(padRightPlain(label, 8)))
		b.WriteString(" ")
		shown := val
		if i == f.focus {
			shown += "▏"
		}
		valStyle := f.styles.Primary
		if i == f.focus {
			valStyle = f.styles.Bright
		}
		b.WriteString(valStyle.Render(truncatePlain(shown, fieldW-9)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if f.errMsg != "" {
		b.WriteString(f.styles.Error.Render(truncatePlain(f.errMsg, fieldW)))
		b.WriteString("\n")
	}
	hint := f.styles.Accent.Render("enter") + f.styles.Dim.Render(" add · ") +
		f.styles.Accent.Render("tab") + f.styles.Dim.Render(" next · ") +
		f.styles.Accent.Render("esc") + f.styles.Dim.Render(" cancel")
	b.WriteString(hint)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(f.palette.Border).
		Padding(0, 1).
		Width(boxW).
		Render(b.String())
	return box
}

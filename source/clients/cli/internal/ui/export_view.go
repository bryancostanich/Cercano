package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/render"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

type exportStep int

const (
	exportPickConversation exportStep = iota
	exportPickDestination
	exportRunning
	exportDone
)

type trajectoryExportView struct {
	width, height int
	styles        theme.Styles
	agent         *agentclient.Client
	currentConvID string
	rows          []histRow
	allRows       []histRow
	filter        string
	filtering     bool
	cursor        int
	scrollOffset  int
	md            *render.Markdown

	step        exportStep
	selected    histRow
	outPath     string
	editingPath bool
	zip         bool
	redactNone  bool
	includeLogs bool
	focus       int // 0 path, 1 format, 2 redaction, 3 logs, 4 export

	progress []string
	warnings []string
	result   agentclient.ExportTrajectoryEvent
	err      string
}

type trajectoryExportEventMsg struct {
	event  agentclient.ExportTrajectoryEvent
	events <-chan agentclient.ExportTrajectoryEvent
	errs   <-chan error
}
type trajectoryExportErrMsg struct{ err error }

func newTrajectoryExportView(ag *agentclient.Client, p theme.Palette, s theme.Styles, w, h int, currentConvID, prefill string) (*trajectoryExportView, tea.Cmd) {
	v := &trajectoryExportView{styles: s, agent: ag, currentConvID: currentConvID, width: w, height: h, filtering: true, md: render.NewMarkdown(theme.MarkdownStyle(p)), zip: true}
	v.allRows = loadHistoryRows(ag)
	v.applyFilter()
	if currentConvID != "" {
		for i, r := range v.rows {
			if r.id == currentConvID {
				v.cursor = i
				break
			}
		}
	}
	if prefill != "" {
		v.outPath = prefill
	}
	return v, nil
}

func (v *trajectoryExportView) ID() contentPageID { return contentPageExport }
func (v *trajectoryExportView) SetSize(w, h int)  { v.width, v.height = w, h; v.clampScroll() }

func (v *trajectoryExportView) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch v.step {
	case exportPickConversation:
		return v.updateConversation(msg)
	case exportPickDestination:
		return v.updateDestination(msg)
	case exportRunning:
		if msg.String() == "esc" || msg.String() == "q" {
			return nil, true
		}
	case exportDone:
		switch msg.String() {
		case "enter", "esc", "q":
			return nil, true
		}
	}
	return nil, false
}

func (v *trajectoryExportView) updateConversation(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if v.filtering {
		if handled := v.updateFilter(msg); handled {
			return nil, false
		}
	}
	switch msg.String() {
	case "esc", "q":
		return nil, true
	case "/":
		v.filtering = true
	case "up", "k":
		if v.cursor <= 0 {
			v.filtering = true
		} else {
			v.moveCursor(-1)
		}
	case "down", "j":
		v.moveCursor(1)
	case "enter":
		if v.cursor >= 0 && v.cursor < len(v.rows) {
			v.selected = v.rows[v.cursor]
			if v.outPath == "" {
				v.outPath = defaultTrajectoryExportPath(v.selected)
			}
			v.step = exportPickDestination
			v.filtering = false
			v.focus = 0
			v.editingPath = true
		}
	case "pgup", "ctrl+b":
		v.ScrollBy(-dashboardContentHeight(v.height))
	case "pgdown", "ctrl+f":
		v.ScrollBy(dashboardContentHeight(v.height))
	}
	return nil, false
}

func (v *trajectoryExportView) updateDestination(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if v.editingPath && v.focus == 0 {
		switch msg.String() {
		case "enter", "tab":
			v.editingPath = false
			v.focus = 1
			return nil, false
		case "esc":
			v.step = exportPickConversation
			return nil, false
		case "backspace":
			if v.outPath != "" {
				r := []rune(v.outPath)
				v.outPath = string(r[:len(r)-1])
			}
			return nil, false
		case "ctrl+u":
			v.outPath = ""
			return nil, false
		}
		if text := printableKeyText(msg); text != "" {
			v.outPath += text
			return nil, false
		}
	}
	switch msg.String() {
	case "esc":
		v.step = exportPickConversation
	case "tab", "down", "j":
		v.focus = (v.focus + 1) % 5
	case "shift+tab", "up", "k":
		v.focus = (v.focus + 4) % 5
	case "enter", " ":
		switch v.focus {
		case 0:
			v.editingPath = true
		case 1:
			v.zip = !v.zip
			v.outPath = toggleZipPath(v.outPath, v.zip)
		case 2:
			v.redactNone = !v.redactNone
		case 3:
			v.includeLogs = !v.includeLogs
		case 4:
			return v.startExport(), false
		}
	}
	return nil, false
}

func (v *trajectoryExportView) startExport() tea.Cmd {
	if strings.TrimSpace(v.outPath) == "" {
		v.err = "destination path is required"
		return nil
	}
	if v.agent == nil {
		v.err = "agent client unavailable"
		v.step = exportDone
		return nil
	}
	v.step = exportRunning
	v.progress = []string{"starting export…"}
	v.warnings = nil
	v.err = ""
	v.result = agentclient.ExportTrajectoryEvent{}
	format := "directory"
	if v.zip {
		format = "zip"
	}
	redaction := "default"
	if v.redactNone {
		redaction = "none"
	}
	events, errs := v.agent.ExportTrajectory(context.Background(), agentclient.ExportTrajectoryOptions{ConversationID: v.selected.id, OutPath: expandHome(v.outPath), Format: format, RedactionMode: redaction, IncludeLogs: v.includeLogs, Overwrite: false})
	return waitTrajectoryExportEventCmd(events, errs)
}

func waitTrajectoryExportEventCmd(events <-chan agentclient.ExportTrajectoryEvent, errs <-chan error) tea.Cmd {
	return func() tea.Msg {
		select {
		case ev, ok := <-events:
			if !ok {
				if err, ok := <-errs; ok && err != nil {
					return trajectoryExportErrMsg{err: err}
				}
				return trajectoryExportEventMsg{event: agentclient.ExportTrajectoryEvent{Kind: "completed"}}
			}
			return trajectoryExportEventMsg{event: ev, events: events, errs: errs}
		case err, ok := <-errs:
			if ok && err != nil {
				return trajectoryExportErrMsg{err: err}
			}
			return trajectoryExportEventMsg{event: agentclient.ExportTrajectoryEvent{Kind: "completed"}}
		}
	}
}

func (v *trajectoryExportView) applyExportEvent(msg trajectoryExportEventMsg) tea.Cmd {
	ev := msg.event
	done := false
	switch ev.Kind {
	case "progress":
		v.progress = append(v.progress, fmt.Sprintf("%s: %s", ev.Phase, ev.Message))
	case "warning":
		v.warnings = append(v.warnings, ev.Message)
	case "failed":
		v.err = ev.Message
		v.step = exportDone
		done = true
	case "completed":
		if ev.OutputPath != "" {
			v.result = ev
			v.step = exportDone
			done = true
		}
	}
	if done || msg.events == nil {
		return nil
	}
	return waitTrajectoryExportEventCmd(msg.events, msg.errs)
}

func (v *trajectoryExportView) updateFilter(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "enter", "tab", "down", "up":
		v.filtering = false
		return true
	case "esc":
		v.filtering = false
		return true
	case "backspace":
		if v.filter != "" {
			r := []rune(v.filter)
			v.filter = string(r[:len(r)-1])
			v.applyFilter()
		}
		return true
	case "ctrl+c", "ctrl+u":
		v.filter = ""
		v.applyFilter()
		return true
	}
	if text := printableKeyText(msg); text != "" {
		v.filter += text
		v.applyFilter()
		return true
	}
	return false
}

func printableKeyText(msg tea.KeyPressMsg) string {
	if msg.Mod&tea.ModCtrl != 0 {
		return ""
	}
	if msg.Text != "" && !strings.Contains(msg.Text, "\n") && !containsNonPrintable(msg.Text) {
		return msg.Text
	}
	if msg.Code >= 32 && msg.Code != 127 && utf8.ValidRune(msg.Code) && unicode.IsPrint(msg.Code) {
		return string(msg.Code)
	}
	key := msg.String()
	if len([]rune(key)) == 1 && !containsNonPrintable(key) {
		return key
	}
	return ""
}

func (v *trajectoryExportView) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(v.filter))
	if q == "" {
		v.rows = append([]histRow(nil), v.allRows...)
	} else {
		terms := strings.Fields(q)
		v.rows = v.rows[:0]
		for _, r := range v.allRows {
			hay := strings.ToLower(r.name + "\n" + r.recap + "\n" + r.meta + "\n" + r.id)
			if historyRowMatchesAllTerms(hay, terms) {
				v.rows = append(v.rows, r)
			}
		}
	}
	if len(v.rows) == 0 {
		v.cursor = 0
	} else {
		v.cursor = clampInt(v.cursor, 0, len(v.rows)-1)
	}
	v.scrollOffset = 0
	v.clampScroll()
}
func (v *trajectoryExportView) moveCursor(dir int) {
	if len(v.rows) == 0 {
		return
	}
	v.cursor = clampInt(v.cursor+dir, 0, len(v.rows)-1)
	v.scrollToCursor()
}
func (v *trajectoryExportView) scrollToCursor() {
	first := 5 + v.cursor*2
	h := dashboardContentHeight(v.height)
	if first < v.scrollOffset {
		v.scrollOffset = first
	} else if first >= v.scrollOffset+h {
		v.scrollOffset = first - h + 1
	}
	v.clampScroll()
}

func (v *trajectoryExportView) View() string {
	var lines []string
	panelW := dashboardPanelWidth(v.width)
	for _, hl := range strings.Split(v.md.Render("# Export trajectory", panelW), "\n") {
		lines = append(lines, hl)
	}
	switch v.step {
	case exportPickConversation:
		lines = append(lines, v.searchLines(panelW)...)
		lines = append(lines, "")
		if len(v.rows) == 0 {
			lines = append(lines, v.styles.Muted.Render("  (no conversations match)"))
		} else {
			for i := range v.rows {
				lines = append(lines, v.renderRow(i, panelW)...)
			}
		}
	case exportPickDestination:
		lines = append(lines, v.destinationLines(panelW)...)
	case exportRunning:
		lines = append(lines, v.styles.Info.Render("Exporting ")+v.styles.Bright.Render(v.selected.name), "")
		start := maxInt(0, len(v.progress)-10)
		for _, p := range v.progress[start:] {
			lines = append(lines, "✓ "+v.styles.Muted.Render(p))
		}
	case exportDone:
		if v.err != "" {
			lines = append(lines, v.styles.Error.Render("Export failed: ")+v.err)
		} else {
			lines = append(lines, v.styles.Success.Render("Export complete"), "", v.styles.Primary.Render(expandHome(v.result.OutputPath)), fmt.Sprintf("%d artifacts · %d subagents", v.result.ArtifactCount, v.result.SubagentCount))
		}
		for _, w := range v.warnings {
			lines = append(lines, v.styles.Warn.Render("warning: ")+w)
		}
		lines = append(lines, "", v.styles.Muted.Render("enter/esc done"))
	}
	height := dashboardContentHeight(v.height)
	v.scrollOffset = clampInt(v.scrollOffset, 0, maxInt(0, len(lines)-height))
	return renderScrollable(lines, height, panelW, v.scrollOffset, v.styles)
}

func (v *trajectoryExportView) searchLines(width int) []string {
	line := v.styles.UserPrompt.Render(strings.Repeat("─", width))
	label := "search: " + v.filter
	if v.filtering {
		label += "█"
	}
	label += fmt.Sprintf("  (%d/%d)", len(v.rows), len(v.allRows))
	return []string{line, v.styles.UserPrompt.Render("▶ ") + v.styles.Primary.Render(ansi.Truncate(label, maxInt(1, width-2), "…")), line}
}
func (v *trajectoryExportView) renderRow(i, w int) []string {
	r := v.rows[i]
	arrow := v.styles.Muted.Render("▸ ")
	if i == v.cursor {
		arrow = v.styles.Accent.Render("▸ ")
	}
	meta := v.styles.Muted.Render(r.meta)
	metaW := lipgloss.Width(meta)
	nameW := maxInt(8, w-5-metaW)
	title := v.styles.Bright.Bold(true).Render(ansi.Truncate(r.name, nameW, "…"))
	recap := r.recap
	if strings.TrimSpace(recap) == "" {
		recap = "(no recap)"
	}
	return []string{" " + arrow + title + strings.Repeat(" ", maxInt(0, nameW-lipgloss.Width(title))) + "  " + meta, "      " + v.styles.Primary.Render(ansi.Truncate(recap, maxInt(8, w-6), "…"))}
}
func (v *trajectoryExportView) destinationLines(w int) []string {
	selected := v.selected.name
	mark := func(i int) string {
		if v.focus == i {
			return v.styles.Accent.Render("› ")
		}
		return "  "
	}
	path := v.outPath
	if v.focus == 0 && v.editingPath {
		path += "█"
	}
	format := "Zip bundle"
	if !v.zip {
		format = "Directory bundle"
	}
	redact := "Default redaction"
	if v.redactNone {
		redact = "No redaction"
	}
	logs := "No"
	if v.includeLogs {
		logs = "Yes"
	}
	return []string{v.styles.Muted.Render("Conversation:"), "  " + v.styles.Bright.Render(selected), "", mark(0) + "Destination: " + v.styles.Primary.Render(ansi.Truncate(path, maxInt(10, w-15), "…")), mark(1) + "Format: " + format, mark(2) + "Redaction: " + redact, mark(3) + "Include logs: " + logs, "", mark(4) + v.styles.Success.Render("Export"), "", v.styles.Muted.Render("tab move · enter edit/toggle/export · esc back")}
}

func (v *trajectoryExportView) ScrollBy(delta int)  { v.scrollOffset += delta; v.clampScroll() }
func (v *trajectoryExportView) ScrollTo(offset int) { v.scrollOffset = offset; v.clampScroll() }
func (v *trajectoryExportView) ScrollState() contentPageScrollState {
	return contentPageScrollState{Total: 0, Height: dashboardContentHeight(v.height), Offset: v.scrollOffset}
}
func (v *trajectoryExportView) clampScroll() {
	if v.scrollOffset < 0 {
		v.scrollOffset = 0
	}
}

func defaultTrajectoryExportPath(r histRow) string {
	dir := filepath.Join(userHomeDir(), "Downloads")
	slug := slugify(r.name)
	if slug == "" {
		slug = "conversation"
	}
	return filepath.Join(dir, fmt.Sprintf("cercano-trajectory-%s-%s.zip", slug, time.Now().Format("20060102-150405")))
}
func userHomeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}
func expandHome(p string) string {
	if p == "~" {
		return userHomeDir()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(userHomeDir(), p[2:])
	}
	return p
}
func toggleZipPath(p string, zip bool) string {
	if p == "" {
		return p
	}
	if zip {
		if !strings.EqualFold(filepath.Ext(p), ".zip") {
			return p + ".zip"
		}
		return p
	}
	if strings.EqualFold(filepath.Ext(p), ".zip") {
		return strings.TrimSuffix(p, filepath.Ext(p))
	}
	return p
}
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 48 {
		out = out[:48]
	}
	return strings.Trim(out, "-")
}

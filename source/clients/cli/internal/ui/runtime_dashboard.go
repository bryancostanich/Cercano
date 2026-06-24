package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/overlay"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

const (
	runtimeActionStart    = "start"
	runtimeActionStop     = "stop"
	runtimeActionRestart  = "restart"
	runtimeActionDownload = "download"
	runtimeActionCancel   = "cancel_download"
	runtimeActionDelete   = "delete_model"
	runtimeActionSep      = "\x1f"

	maxDashboardEndpoints = 8
	maxDashboardModels    = 12
	maxDashboardInstances = 8
	maxDashboardLogs      = 10
	maxCatalogRows        = 6
)

type runtimeDashboardFocus int

const (
	runtimeFocusCatalog runtimeDashboardFocus = iota
	runtimeFocusActions
)

// runtimeDashboard wraps overlay.RowList with runtime status rows and
// start/stop/restart actions backed by the Cercano server.
type runtimeDashboard struct {
	width, height  int
	palette        theme.Palette
	styles         theme.Styles
	agent          *agentclient.Client
	snapshot       runtimeDashboardSnapshot
	list           overlay.RowList
	focus          runtimeDashboardFocus
	catalogSearch  textinput.Model
	catalogCursor  int
	catalogMessage string
	scrollOffset   int
}

type runtimeDashboardSnapshot struct {
	Config    *agentclient.Config
	ConfigErr error
	Status    *agentclient.RuntimeStatus
	StatusErr error
}

type runtimeDashboardAction struct {
	Kind       string
	Runtime    string
	ModelID    string
	InstanceID string
}

type runtimeDashboardActionMsg struct {
	Status         string
	CatalogMessage string
}

type runtimeDashboardRefreshMsg struct{}

func runtimeDashboardRefreshTick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return runtimeDashboardRefreshMsg{} })
}

func newRuntimeDashboard(ag *agentclient.Client, p theme.Palette, s theme.Styles, w, h int) (*runtimeDashboard, tea.Cmd) {
	search := textinput.New()
	search.Prompt = ""
	search.Placeholder = "Search catalog models"
	search.CharLimit = 0
	search.SetWidth(32)
	blinkCmd := search.Focus()
	dashboard := &runtimeDashboard{
		palette:       p,
		styles:        s,
		agent:         ag,
		width:         w,
		height:        h,
		focus:         runtimeFocusCatalog,
		catalogSearch: search,
	}
	hooks := overlay.Hooks{
		OnSelect: func(row overlay.Row) (string, bool, tea.Cmd) {
			action, err := parseRuntimeDashboardAction(row.Key)
			if err != nil {
				return "action failed: " + err.Error(), false, nil
			}
			return runtimeDashboardPendingStatus(action), false, runtimeDashboardActionCmd(ag, action)
		},
		OnReload: func() []overlay.Row {
			dashboard.snapshot = loadRuntimeDashboardSnapshot(ag)
			return runtimeActionRowsFromSnapshot(dashboard.snapshot)
		},
	}
	dashboard.snapshot = loadRuntimeDashboardSnapshot(ag)
	list := overlay.New("models and processes", runtimeActionRowsFromSnapshot(dashboard.snapshot), hooks)
	list.SetStatus("tab focus; enter action rows; esc closes")
	dashboard.list = list
	return dashboard, blinkCmd
}

func (d *runtimeDashboard) ID() contentPageID {
	return contentPageModels
}

func (d *runtimeDashboard) SetSize(w, h int) {
	d.width = w
	d.height = h
	d.clampScroll()
}

func (d *runtimeDashboard) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "tab":
		d.toggleFocus()
		return nil, false
	case "pgup", "ctrl+b":
		d.ScrollBy(-dashboardContentHeight(d.height))
		return nil, false
	case "pgdown", "ctrl+f":
		d.ScrollBy(dashboardContentHeight(d.height))
		return nil, false
	case "ctrl+u":
		d.ScrollBy(-maxInt(1, dashboardContentHeight(d.height)/2))
		return nil, false
	case "ctrl+d":
		d.ScrollBy(maxInt(1, dashboardContentHeight(d.height)/2))
		return nil, false
	}
	if d.focus == runtimeFocusCatalog {
		return d.updateCatalog(msg)
	}
	next, cmd, closed := d.list.Update(msg, d.styles)
	d.list = next
	return cmd, closed
}

func (d *runtimeDashboard) View() string {
	full, contentH := d.fullContent()
	return d.renderScrollableContent(full, contentH)
}

func (d *runtimeDashboard) fullContent() (string, int) {
	configBlock := d.renderConfigBlocks()
	listBlock := d.list.ViewPanel(dashboardPanelWidth(d.width), d.palette, d.styles)
	contentH := dashboardContentHeight(d.height)
	catalogRows := d.catalogRowsForHeight(contentH, countLines([]string{configBlock, listBlock}))
	catalogBlock := d.renderCatalogBlock(catalogRows)
	parts := []string{
		configBlock,
		catalogBlock,
		listBlock,
	}
	logRows := contentH - countLines(parts)
	parts = append(parts, d.renderLocalServerLogBlock(logRows))
	return strings.Join(parts, "\n"), contentH
}

func (d *runtimeDashboard) ScrollBy(delta int) {
	d.scrollOffset += delta
	d.clampScroll()
}

func (d *runtimeDashboard) ScrollTo(offset int) {
	d.scrollOffset = offset
	d.clampScroll()
}

func (d *runtimeDashboard) ScrollState() contentPageScrollState {
	return d.scrollState()
}

func (d *runtimeDashboard) scrollState() contentPageScrollState {
	full, contentH := d.fullContent()
	total := countLines([]string{full})
	return contentPageScrollState{
		Total:  total,
		Height: contentH,
		Offset: clampInt(d.scrollOffset, 0, maxInt(0, total-contentH)),
	}
}

func (d *runtimeDashboard) clampScroll() {
	state := d.scrollState()
	d.scrollOffset = state.Offset
}

func (d *runtimeDashboard) renderScrollableContent(full string, height int) string {
	if height < 1 {
		height = 1
	}
	lines := strings.Split(full, "\n")
	d.scrollOffset = clampInt(d.scrollOffset, 0, maxInt(0, len(lines)-height))
	panelW := dashboardPanelWidth(d.width)
	col := scrollbarColumn(len(lines), height, d.scrollOffset)

	var b strings.Builder
	for i := 0; i < height; i++ {
		line := ""
		src := d.scrollOffset + i
		if src >= 0 && src < len(lines) {
			line = lines[src]
		}
		line = ansi.Truncate(line, panelW, "")
		b.WriteString(line)
		b.WriteString(" ")
		if i < len(col) {
			switch col[i] {
			case '█':
				b.WriteString(d.styles.Border.Render("█"))
			case '░':
				b.WriteString(d.styles.BorderDim.Render("░"))
			default:
				b.WriteString(" ")
			}
		} else {
			b.WriteString(" ")
		}
		if i < height-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (d *runtimeDashboard) applyActionMsg(msg runtimeDashboardActionMsg) tea.Cmd {
	d.list.Reload()
	d.list.SetStatus(msg.Status)
	if msg.CatalogMessage != "" {
		d.catalogMessage = msg.CatalogMessage
	}
	if d.hasActiveDownloads() {
		return runtimeDashboardRefreshTick()
	}
	return nil
}

func (d *runtimeDashboard) refreshSnapshot() tea.Cmd {
	d.snapshot = loadRuntimeDashboardSnapshot(d.agent)
	d.list.Reload()
	if d.hasActiveDownloads() {
		return runtimeDashboardRefreshTick()
	}
	return nil
}

func (d *runtimeDashboard) hasActiveDownloads() bool {
	for _, model := range runtimeStatusModels(d.snapshot.Status) {
		if strings.EqualFold(model.DownloadState, "downloading") {
			return true
		}
	}
	return false
}

func (d *runtimeDashboard) toggleFocus() {
	if d.focus == runtimeFocusCatalog {
		d.focus = runtimeFocusActions
		d.catalogSearch.Blur()
		d.catalogMessage = ""
		d.list.SetStatus("tab search; enter action rows; esc closes")
		return
	}
	d.focus = runtimeFocusCatalog
	_ = d.catalogSearch.Focus()
	d.list.SetStatus("tab actions")
}

func (d *runtimeDashboard) updateCatalog(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		if d.catalogSearch.Value() != "" {
			d.catalogSearch.SetValue("")
			d.catalogCursor = 0
			d.catalogMessage = "filter cleared"
			return nil, false
		}
		return nil, true
	case "up":
		if d.catalogCursor > 0 {
			d.catalogCursor--
		}
		return nil, false
	case "down":
		models := filteredCatalogModels(runtimeStatusModels(d.snapshot.Status), d.catalogSearch.Value())
		if d.catalogCursor < len(models)-1 {
			d.catalogCursor++
		}
		return nil, false
	case "home":
		d.catalogCursor = 0
		return nil, false
	case "end":
		models := filteredCatalogModels(runtimeStatusModels(d.snapshot.Status), d.catalogSearch.Value())
		if len(models) > 0 {
			d.catalogCursor = len(models) - 1
		}
		return nil, false
	case "enter":
		models := filteredCatalogModels(runtimeStatusModels(d.snapshot.Status), d.catalogSearch.Value())
		if len(models) == 0 {
			d.catalogMessage = "no model selected"
			return nil, false
		}
		selected := models[clampIndex(d.catalogCursor, len(models))]
		if strings.EqualFold(selected.DownloadState, "downloaded") {
			d.catalogMessage = "already downloaded"
			return nil, false
		}
		if strings.EqualFold(selected.DownloadState, "downloading") {
			d.catalogMessage = "already downloading"
			return nil, false
		}
		d.catalogMessage = "starting download..."
		if strings.EqualFold(selected.DownloadState, "failed") || strings.EqualFold(selected.DownloadState, "cancelled") {
			d.catalogMessage = "retrying download..."
		}
		return runtimeDashboardDownloadCmd(d.agent, selected), false
	}
	prev := d.catalogSearch.Value()
	var cmd tea.Cmd
	d.catalogSearch, cmd = d.catalogSearch.Update(msg)
	if d.catalogSearch.Value() != prev {
		d.catalogCursor = 0
		d.catalogMessage = ""
	}
	return cmd, false
}

func buildRuntimeRows(ag *agentclient.Client) []overlay.Row {
	if ag == nil {
		return []overlay.Row{{Label: "runtime", Value: "agent client unavailable", ReadOnly: true}}
	}
	return runtimeRowsFromSnapshot(loadRuntimeDashboardSnapshot(ag))
}

func loadRuntimeDashboardSnapshot(ag *agentclient.Client) runtimeDashboardSnapshot {
	var snap runtimeDashboardSnapshot
	if ag == nil {
		err := errors.New("agent client unavailable")
		snap.ConfigErr = err
		snap.StatusErr = err
		return snap
	}

	cfgCtx, cfgCancel := context.WithTimeout(context.Background(), 3*time.Second)
	snap.Config, snap.ConfigErr = ag.GetConfig(cfgCtx)
	cfgCancel()

	statusCtx, statusCancel := context.WithTimeout(context.Background(), 3*time.Second)
	snap.Status, snap.StatusErr = ag.GetRuntimeStatus(statusCtx)
	statusCancel()

	return snap
}

func runtimeRowsFromSnapshot(s runtimeDashboardSnapshot) []overlay.Row {
	rows := make([]overlay.Row, 0, 48)
	appendRuntimeConfigRows(&rows, s)

	if s.StatusErr != nil {
		rows = append(rows, overlay.Row{
			Label:    "runtime status",
			Value:    s.StatusErr.Error(),
			Hint:     "error",
			ReadOnly: true,
		})
		return rows
	}
	status := s.Status
	if status == nil {
		status = &agentclient.RuntimeStatus{}
	}

	appendRuntimeEndpointRows(&rows, status.Endpoints)
	appendRuntimeModelRows(&rows, status.Models, status.Instances)
	appendRuntimeDownloadRows(&rows, status.Models)
	appendRuntimeInstanceRows(&rows, status.Instances)
	appendRuntimeLogRows(&rows, status.Logs)

	if len(rows) == 0 {
		rows = append(rows, overlay.Row{Label: "runtime", Value: "no runtime data", ReadOnly: true})
	}
	return rows
}

func runtimeActionRowsFromSnapshot(s runtimeDashboardSnapshot) []overlay.Row {
	rows := make([]overlay.Row, 0, 32)
	if s.StatusErr != nil {
		rows = append(rows, overlay.Row{
			Label:    "runtime status",
			Value:    s.StatusErr.Error(),
			Hint:     "error",
			ReadOnly: true,
		})
		return rows
	}
	status := s.Status
	if status == nil {
		status = &agentclient.RuntimeStatus{}
	}
	appendRuntimeModelRows(&rows, status.Models, status.Instances)
	appendRuntimeDownloadRows(&rows, status.Models)
	appendRuntimeInstanceRows(&rows, status.Instances)
	if len(rows) == 0 {
		rows = append(rows, overlay.Row{Label: "runtime", Value: "no runtime data", ReadOnly: true})
	}
	return rows
}

func appendRuntimeConfigRows(rows *[]overlay.Row, s runtimeDashboardSnapshot) {
	if s.ConfigErr != nil {
		*rows = append(*rows, overlay.Row{
			Label:    "config",
			Value:    s.ConfigErr.Error(),
			Hint:     "error",
			ReadOnly: true,
		})
		return
	}
	if s.Config == nil {
		return
	}
	cfg := s.Config
	*rows = append(*rows,
		overlay.Row{Label: "local runtime", Value: cfg.LocalRuntime, Hint: "active", ReadOnly: true},
		overlay.Row{Label: "local model", Value: cfg.LocalModel, ReadOnly: true},
		overlay.Row{Label: "embedding model", Value: cfg.EmbeddingModel, ReadOnly: true},
	)
	if cfg.CloudProvider != "" || cfg.CloudModel != "" || cfg.CloudBaseURL != "" {
		parts := nonEmptyParts(cfg.CloudProvider, cfg.CloudModel, cfg.CloudBaseURL)
		*rows = append(*rows, overlay.Row{
			Label:    "cloud endpoint",
			Value:    strings.Join(parts, " | "),
			Hint:     "configured",
			ReadOnly: true,
		})
	}
}

type runtimeDashboardField struct {
	Label string
	Value string
	Hint  string
}

func (d *runtimeDashboard) renderConfigBlocks() string {
	totalW := dashboardPanelWidth(d.width)
	leftW, rightW := dashboardConfigBlockWidths(totalW)
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		renderRuntimeDashboardBlock("local config", localConfigFields(d.snapshot), leftW, d.palette, d.styles),
		renderRuntimeDashboardBlock("cloud / external", cloudConfigFields(d.snapshot), rightW, d.palette, d.styles),
	)
}

func (d *runtimeDashboard) renderLocalServerLogBlock(height int) string {
	totalW := dashboardPanelWidth(d.width)
	contentW := dashboardBlockContentWidth(totalW)
	logs := localModelServerLogs(runtimeStatusLogs(d.snapshot.Status))
	lines := make([]string, 0, maxDashboardLogs)
	if len(logs) == 0 {
		lines = append(lines, d.styles.Dim.Render("no local model server logs yet"))
	} else {
		start := 0
		if len(logs) > maxDashboardLogs {
			start = len(logs) - maxDashboardLogs
		}
		for _, entry := range logs[start:] {
			lines = append(lines, renderRuntimeLogLine(entry, contentW, d.styles))
		}
	}
	return renderRuntimeDashboardTextBlock("local model server log", lines, totalW, height, d.palette, d.styles)
}

func (d *runtimeDashboard) renderCatalogBlock(rowLimit int) string {
	if rowLimit < 1 {
		rowLimit = 1
	}
	if rowLimit > maxCatalogRows {
		rowLimit = maxCatalogRows
	}
	totalW := dashboardPanelWidth(d.width)
	contentW := dashboardBlockContentWidth(totalW)
	models := filteredCatalogModels(runtimeStatusModels(d.snapshot.Status), d.catalogSearch.Value())
	if len(models) > 0 {
		d.catalogCursor = clampIndex(d.catalogCursor, len(models))
	} else {
		d.catalogCursor = 0
	}

	listW := contentW / 2
	if listW < 34 {
		listW = 34
	}
	if listW > contentW-28 {
		listW = contentW - 28
	}
	detailW := contentW - listW - 3
	if detailW < 20 {
		detailW = 20
	}

	queryLabel := d.styles.Muted.Render("filter ")
	if d.focus == runtimeFocusCatalog {
		queryLabel = d.styles.Bright.Render("filter ")
	}
	d.catalogSearch.SetWidth(maxInt(8, contentW-lipgloss.Width(queryLabel)-2))
	searchLine := queryLabel + d.styles.Primary.Render(d.catalogSearch.View())
	if d.catalogMessage != "" {
		remaining := contentW - lipgloss.Width(searchLine) - 2
		if remaining > 8 {
			searchLine += d.styles.BorderDim.Render("  " + truncatePlain(d.catalogMessage, remaining))
		}
	}

	selected := agentclient.RuntimeModel{}
	if len(models) > 0 {
		selected = models[d.catalogCursor]
	}
	details := catalogDetailLines(selected, detailW, d.styles)

	lines := []string{searchLine}
	for i := 0; i < rowLimit; i++ {
		left := ""
		if i < len(models) {
			absolute := i
			left = d.renderCatalogModelRow(models[i], absolute == d.catalogCursor, listW)
		} else if i == 0 && len(models) == 0 {
			left = d.styles.Dim.Render(catalogEmptyMessage(runtimeStatusModels(d.snapshot.Status), d.catalogSearch.Value()))
		}
		right := ""
		if i < len(details) {
			right = details[i]
		}
		lines = append(lines,
			padRightStyled(left, listW)+
				d.styles.BorderDim.Render(" │ ")+
				padRightStyled(right, detailW),
		)
	}
	if len(models) > rowLimit && d.catalogMessage == "" {
		remaining := contentW - lipgloss.Width(searchLine) - 2
		if remaining > 10 {
			lines[0] = searchLine + d.styles.BorderDim.Render(fmt.Sprintf("  %d more", len(models)-rowLimit))
		}
	}
	return renderRuntimeDashboardTextBlock("download catalog", lines, totalW, 0, d.palette, d.styles)
}

func (d *runtimeDashboard) renderCatalogModelRow(model agentclient.RuntimeModel, selected bool, width int) string {
	marker := "  "
	nameStyle := d.styles.Primary
	if selected {
		marker = d.styles.Accent.Render("▶ ")
		nameStyle = d.styles.Bright
	}
	name := firstNonEmpty(model.DisplayName, shortModelName(model.ID), model.ID, "unknown")
	metadata := strings.Join(nonEmptyParts(model.Family, model.Quantization, formatBytes(model.SizeBytes), compactDownloadStatusText(model)), " · ")
	row := marker + nameStyle.Render(name)
	if metadata != "" {
		row += d.styles.BorderDim.Render("  " + metadata)
	}
	if lipgloss.Width(row) > width {
		row = ansi.Truncate(row, width, "")
	}
	return row
}

func renderRuntimeDashboardBlock(title string, fields []runtimeDashboardField, totalW int, palette theme.Palette, styles theme.Styles) string {
	contentW := dashboardBlockContentWidth(totalW)
	if len(fields) == 0 {
		fields = []runtimeDashboardField{{Label: "status", Value: "unavailable"}}
	}
	labelW := 0
	for _, field := range fields {
		if w := lipgloss.Width(field.Label); w > labelW {
			labelW = w
		}
	}
	if labelW > 14 {
		labelW = 14
	}
	valueW := contentW - labelW - 2
	if valueW < 8 {
		valueW = 8
	}
	lines := make([]string, 0, len(fields)+1)
	lines = append(lines, styles.Accent.Render(title))
	for _, field := range fields {
		label := padRightPlain(truncatePlain(field.Label, labelW), labelW)
		value := field.Value
		if strings.TrimSpace(value) == "" {
			value = "(unset)"
		}
		value = truncatePlain(value, valueW)
		line := styles.Muted.Render(label) + styles.BorderDim.Render("  ")
		if value == "(unset)" {
			line += styles.Dim.Render(value)
		} else {
			line += styles.Primary.Render(value)
		}
		if field.Hint != "" && lipgloss.Width(line) < contentW {
			remaining := contentW - lipgloss.Width(line) - 2
			if remaining > 4 {
				line += styles.BorderDim.Render("  " + truncatePlain(field.Hint, remaining))
			}
		}
		lines = append(lines, line)
	}
	return renderRuntimeDashboardTextBlock("", lines, totalW, 0, palette, styles)
}

func renderRuntimeDashboardTextBlock(title string, lines []string, totalW, height int, palette theme.Palette, styles theme.Styles) string {
	contentW := dashboardBlockContentWidth(totalW)
	if title != "" {
		lines = append([]string{styles.Accent.Render(title)}, lines...)
	}
	minHeight := len(lines) + 2 // content lines plus border.
	if height < minHeight {
		height = minHeight
	}
	for len(lines) < height-2 {
		lines = append(lines, "")
	}
	for i, line := range lines {
		if lipgloss.Width(line) > contentW {
			lines[i] = ansi.Truncate(line, contentW, "")
		}
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(palette.BorderDim).
		Padding(0, 1).
		Width(totalW).
		Render(strings.Join(lines, "\n"))
}

func localConfigFields(s runtimeDashboardSnapshot) []runtimeDashboardField {
	if s.ConfigErr != nil {
		return []runtimeDashboardField{{Label: "config", Value: s.ConfigErr.Error(), Hint: "error"}}
	}
	cfg := s.Config
	if cfg == nil {
		return []runtimeDashboardField{{Label: "config", Value: "unavailable"}}
	}
	return []runtimeDashboardField{
		{Label: "runtime", Value: firstNonEmpty(cfg.LocalRuntime, "ollama")},
		{Label: "chat model", Value: cfg.LocalModel},
		{Label: "embedding", Value: cfg.EmbeddingModel},
		{Label: "ollama URL", Value: cfg.OllamaURL},
		{Label: "llama-server", Value: localServerSummary(s.Status, cfg.LocalRuntime)},
	}
}

func cloudConfigFields(s runtimeDashboardSnapshot) []runtimeDashboardField {
	if s.ConfigErr != nil {
		return []runtimeDashboardField{{Label: "config", Value: s.ConfigErr.Error(), Hint: "error"}}
	}
	cfg := s.Config
	if cfg == nil {
		return []runtimeDashboardField{{Label: "config", Value: "unavailable"}}
	}
	keyState := "missing"
	if cfg.CloudAPIKeySet {
		keyState = "configured"
	}
	endpointValue, endpointHint := externalEndpointSummary(runtimeStatusEndpoints(s.Status))
	return []runtimeDashboardField{
		{Label: "provider", Value: cfg.CloudProvider},
		{Label: "model", Value: cfg.CloudModel},
		{Label: "base URL", Value: cfg.CloudBaseURL},
		{Label: "API key", Value: keyState, Hint: cfg.CloudState},
		{Label: "endpoints", Value: endpointValue, Hint: endpointHint},
	}
}

func localServerSummary(status *agentclient.RuntimeStatus, configuredRuntime string) string {
	if status == nil {
		return "status unavailable"
	}
	runtimeName := firstNonEmpty(configuredRuntime, "llama_server")
	var running []string
	for _, instance := range status.Instances {
		if instance.Runtime != "llama_server" && instance.Runtime != runtimeName {
			continue
		}
		parts := nonEmptyParts(instance.State, shortModelName(instance.ModelID))
		if instance.PID > 0 {
			parts = append(parts, fmt.Sprintf("pid:%d", instance.PID))
		}
		if instance.Endpoint != "" {
			parts = append(parts, instance.Endpoint)
		}
		running = append(running, strings.Join(parts, " | "))
	}
	if len(running) == 0 {
		return "not running"
	}
	return strings.Join(running, "; ")
}

func externalEndpointSummary(endpoints []agentclient.RuntimeEndpoint) (string, string) {
	var external []agentclient.RuntimeEndpoint
	for _, endpoint := range endpoints {
		if isExternalEndpoint(endpoint) {
			external = append(external, endpoint)
		}
	}
	if len(external) == 0 {
		return "none configured", ""
	}
	first := external[0]
	value := countLabel(len(external), "endpoint")
	hint := strings.Join(nonEmptyParts(firstNonEmpty(first.DisplayName, first.ID), first.State, first.Scope), " | ")
	if len(external) > 1 {
		hint += fmt.Sprintf(" | +%d", len(external)-1)
	}
	return value, hint
}

func isExternalEndpoint(endpoint agentclient.RuntimeEndpoint) bool {
	id := strings.ToLower(endpoint.ID)
	scope := strings.ToLower(endpoint.Scope)
	kind := strings.ToLower(endpoint.Kind)
	if scope == "local" {
		return false
	}
	return strings.HasPrefix(id, "cloud:") ||
		scope == "cloud" ||
		scope == "remote" ||
		scope == "lan" ||
		kind == "cloud" ||
		strings.Contains(kind, "openai") ||
		strings.Contains(kind, "anthropic") ||
		strings.Contains(kind, "google")
}

func runtimeStatusEndpoints(status *agentclient.RuntimeStatus) []agentclient.RuntimeEndpoint {
	if status == nil {
		return nil
	}
	return status.Endpoints
}

func runtimeStatusLogs(status *agentclient.RuntimeStatus) []agentclient.RuntimeLogEntry {
	if status == nil {
		return nil
	}
	return status.Logs
}

func runtimeStatusModels(status *agentclient.RuntimeStatus) []agentclient.RuntimeModel {
	if status == nil {
		return nil
	}
	return status.Models
}

func filteredCatalogModels(models []agentclient.RuntimeModel, query string) []agentclient.RuntimeModel {
	query = strings.ToLower(strings.TrimSpace(query))
	out := make([]agentclient.RuntimeModel, 0, len(models))
	for _, model := range models {
		if !isCatalogDownloadModel(model) {
			continue
		}
		if query == "" || catalogModelMatches(model, query) {
			out = append(out, model)
		}
	}
	return out
}

func isCatalogDownloadModel(model agentclient.RuntimeModel) bool {
	source := strings.ToLower(model.Source)
	state := strings.ToLower(model.DownloadState)
	if source == "catalog" || source == "recommended" {
		return true
	}
	switch state {
	case "not_downloaded", "downloading", "failed":
		return true
	default:
		return false
	}
}

func catalogModelMatches(model agentclient.RuntimeModel, query string) bool {
	haystack := strings.ToLower(strings.Join(nonEmptyParts(
		model.ID,
		model.DisplayName,
		model.Runtime,
		model.Source,
		model.Format,
		model.Family,
		model.Quantization,
		model.DownloadState,
		model.RuntimeState,
	), " "))
	return strings.Contains(haystack, query)
}

func catalogEmptyMessage(allModels []agentclient.RuntimeModel, query string) string {
	hasCatalog := false
	for _, model := range allModels {
		if isCatalogDownloadModel(model) {
			hasCatalog = true
			break
		}
	}
	if !hasCatalog {
		return "catalog models will appear here"
	}
	if strings.TrimSpace(query) != "" {
		return "no catalog models match"
	}
	return "no catalog models available"
}

func catalogDetailLines(model agentclient.RuntimeModel, width int, styles theme.Styles) []string {
	if model.ID == "" {
		return []string{
			styles.Dim.Render("Select a catalog model to see details."),
		}
	}
	caps := runtimeCapabilitiesHint(model.SupportsChat, model.SupportsEmbed, model.SupportsTools)
	lines := []string{
		detailLine("name", firstNonEmpty(model.DisplayName, shortModelName(model.ID)), width, styles),
		detailLine("family", strings.Join(nonEmptyParts(model.Family, model.Quantization), " · "), width, styles),
		detailLine("size", formatBytes(model.SizeBytes), width, styles),
		detailLine("runtime", strings.Join(nonEmptyParts(model.Runtime, model.Source), " · "), width, styles),
		detailLine("state", strings.Join(nonEmptyParts(downloadStatusText(model), model.RuntimeState), " · "), width, styles),
		detailLine("supports", caps, width, styles),
	}
	if progress := downloadProgressBar(model, 18); progress != "" {
		lines = append(lines, detailLine("progress", progress+" "+downloadByteStatus(model), width, styles))
	}
	if model.DownloadError != "" {
		lines = append(lines, detailLine("error", model.DownloadError, width, styles))
	}
	if model.Path != "" {
		lines = append(lines, detailLine("path", model.Path, width, styles))
	}
	idLine := detailLine("id", model.ID, width, styles)
	if idLine != "" {
		lines = append(lines, idLine)
	}
	return lines
}

func detailLine(label, value string, width int, styles theme.Styles) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "(unknown)"
	}
	line := styles.Muted.Render(label+": ") + styles.Primary.Render(value)
	if lipgloss.Width(line) > width {
		line = ansi.Truncate(line, width, "")
	}
	return line
}

func localModelServerLogs(logs []agentclient.RuntimeLogEntry) []agentclient.RuntimeLogEntry {
	out := make([]agentclient.RuntimeLogEntry, 0, len(logs))
	for _, entry := range logs {
		source := strings.ToLower(entry.Source)
		runtimeID := strings.ToLower(entry.RuntimeID)
		if strings.Contains(source, "llama") || strings.Contains(source, "download") || strings.Contains(runtimeID, "llama") {
			out = append(out, entry)
		}
	}
	return out
}

func renderRuntimeLogLine(entry agentclient.RuntimeLogEntry, width int, styles theme.Styles) string {
	stamp := "--:--:--"
	if !entry.Timestamp.IsZero() {
		stamp = entry.Timestamp.Format("15:04:05")
	}
	level := strings.ToUpper(firstNonEmpty(entry.Level, "info"))
	model := shortModelName(entry.ModelID)
	prefix := strings.Join(nonEmptyParts(stamp, "["+level+"]", model), " ")
	message := strings.TrimSpace(entry.Message)
	if message == "" {
		message = "(empty log line)"
	}
	line := styles.Muted.Render(prefix) + styles.BorderDim.Render("  ") + styles.Primary.Render(message)
	if lipgloss.Width(line) > width {
		return ansi.Truncate(line, width, "")
	}
	return line
}

func dashboardPanelWidth(width int) int {
	totalW := width - 2
	if totalW < 40 {
		totalW = 40
	}
	return totalW
}

func dashboardContentHeight(height int) int {
	// Root chrome around content pages is: top bar, divider, prompt top rule,
	// one-line prompt, prompt bottom rule, and status bar.
	contentH := height - 6
	if contentH < 1 {
		return 1
	}
	return contentH
}

func (d *runtimeDashboard) catalogRowsForHeight(contentHeight, fixedWithoutCatalog int) int {
	const minLogBlockHeight = 4
	// Catalog block height = title + search line + N rows + border.
	rows := contentHeight - fixedWithoutCatalog - minLogBlockHeight - 4
	if rows < 1 {
		return 1
	}
	if rows > maxCatalogRows {
		return maxCatalogRows
	}
	return rows
}

func dashboardConfigBlockWidths(totalW int) (int, int) {
	leftW := totalW / 2
	return leftW, totalW - leftW
}

func dashboardBlockContentWidth(totalW int) int {
	contentW := totalW - 4
	if contentW < 12 {
		contentW = 12
	}
	return contentW
}

func appendRuntimeEndpointRows(rows *[]overlay.Row, endpoints []agentclient.RuntimeEndpoint) {
	*rows = append(*rows, overlay.Row{
		Label:    "external endpoints",
		Value:    countLabel(len(endpoints), "endpoint"),
		ReadOnly: true,
	})
	if len(endpoints) == 0 {
		*rows = append(*rows, overlay.Row{Label: "endpoint", Value: "none configured", ReadOnly: true})
		return
	}
	for i, endpoint := range endpoints {
		if i >= maxDashboardEndpoints {
			appendMoreRow(rows, "endpoints", len(endpoints)-i)
			break
		}
		*rows = append(*rows, overlay.Row{
			Label:    endpointLabel(endpoint),
			Value:    endpointValue(endpoint),
			Hint:     endpointHint(endpoint),
			ReadOnly: true,
		})
	}
}

func appendRuntimeModelRows(rows *[]overlay.Row, models []agentclient.RuntimeModel, instances []agentclient.RuntimeInstance) {
	models = downloadedRuntimeModels(models)
	*rows = append(*rows, overlay.Row{
		Label:    "downloaded models",
		Value:    countLabel(len(models), "model"),
		ReadOnly: true,
	})
	if len(models) == 0 {
		*rows = append(*rows, overlay.Row{Label: "model", Value: "no downloaded GGUF models found", ReadOnly: true})
		return
	}
	active := activeInstancesByModel(instances)
	for i, model := range models {
		if i >= maxDashboardModels {
			appendMoreRow(rows, "models", len(models)-i)
			break
		}
		running := active[runtimeModelKey(model.Runtime, model.ID)]
		hint := modelHint(model, running)
		*rows = append(*rows, overlay.Row{
			Label:    modelLabel(model),
			Value:    modelValue(model, running),
			Hint:     hint,
			ReadOnly: true,
		})
		if canStartRuntimeModel(model, running) {
			*rows = append(*rows, overlay.Row{
				Key:   encodeRuntimeDashboardAction(runtimeDashboardAction{Kind: runtimeActionStart, Runtime: model.Runtime, ModelID: model.ID}),
				Label: "start " + shortModelName(model.ID),
				Value: model.Runtime,
				Hint:  "enter",
			})
		}
		if canDeleteRuntimeModel(model) {
			*rows = append(*rows, overlay.Row{
				Key:   encodeRuntimeDashboardAction(runtimeDashboardAction{Kind: runtimeActionDelete, Runtime: model.Runtime, ModelID: model.ID}),
				Label: "delete " + shortModelName(model.ID),
				Value: "remove local file",
				Hint:  "enter",
			})
		}
	}
}

func appendRuntimeDownloadRows(rows *[]overlay.Row, models []agentclient.RuntimeModel) {
	downloads := downloadJobModels(models)
	*rows = append(*rows, overlay.Row{
		Label:    "downloads",
		Value:    countLabel(len(downloads), "job"),
		ReadOnly: true,
	})
	if len(downloads) == 0 {
		*rows = append(*rows, overlay.Row{Label: "download", Value: "none active", ReadOnly: true})
		return
	}
	for i, model := range downloads {
		if i >= maxDashboardModels {
			appendMoreRow(rows, "downloads", len(downloads)-i)
			break
		}
		*rows = append(*rows, overlay.Row{
			Label:    "download " + firstNonEmpty(model.DisplayName, shortModelName(model.ID), model.ID),
			Value:    downloadActionValue(model),
			Hint:     downloadActionHint(model),
			ReadOnly: true,
		})
		switch strings.ToLower(model.DownloadState) {
		case "downloading":
			*rows = append(*rows, overlay.Row{
				Key:   encodeRuntimeDashboardAction(runtimeDashboardAction{Kind: runtimeActionCancel, Runtime: model.Runtime, ModelID: model.ID}),
				Label: "cancel " + shortModelName(model.ID),
				Value: downloadStatusText(model),
				Hint:  "enter",
			})
		case "failed", "cancelled":
			*rows = append(*rows, overlay.Row{
				Key:   encodeRuntimeDashboardAction(runtimeDashboardAction{Kind: runtimeActionDownload, Runtime: model.Runtime, ModelID: model.ID}),
				Label: "retry " + shortModelName(model.ID),
				Value: model.Runtime,
				Hint:  "enter",
			})
			if canDeleteRuntimeModel(model) {
				*rows = append(*rows, overlay.Row{
					Key:   encodeRuntimeDashboardAction(runtimeDashboardAction{Kind: runtimeActionDelete, Runtime: model.Runtime, ModelID: model.ID}),
					Label: "delete " + shortModelName(model.ID),
					Value: "remove partial file",
					Hint:  "enter",
				})
			}
		}
	}
}

func appendRuntimeInstanceRows(rows *[]overlay.Row, instances []agentclient.RuntimeInstance) {
	*rows = append(*rows, overlay.Row{
		Label:    "runtime processes",
		Value:    countLabel(len(instances), "process"),
		ReadOnly: true,
	})
	if len(instances) == 0 {
		*rows = append(*rows, overlay.Row{Label: "process", Value: "none running", ReadOnly: true})
		return
	}
	for i, instance := range instances {
		if i >= maxDashboardInstances {
			appendMoreRow(rows, "processes", len(instances)-i)
			break
		}
		*rows = append(*rows, overlay.Row{
			Label:    instanceLabel(instance),
			Value:    instanceValue(instance),
			Hint:     instanceHint(instance),
			ReadOnly: true,
		})
		if instance.ID == "" {
			continue
		}
		if instance.State != "stopped" {
			*rows = append(*rows, overlay.Row{
				Key:   encodeRuntimeDashboardAction(runtimeDashboardAction{Kind: runtimeActionStop, InstanceID: instance.ID}),
				Label: "stop " + shortModelName(instance.ModelID),
				Value: instance.Runtime,
				Hint:  "enter",
			})
		}
		if instance.Runtime != "" && instance.ModelID != "" {
			*rows = append(*rows, overlay.Row{
				Key: encodeRuntimeDashboardAction(runtimeDashboardAction{
					Kind:       runtimeActionRestart,
					Runtime:    instance.Runtime,
					ModelID:    instance.ModelID,
					InstanceID: instance.ID,
				}),
				Label: "restart " + shortModelName(instance.ModelID),
				Value: instance.Runtime,
				Hint:  "enter",
			})
		}
	}
}

func appendRuntimeLogRows(rows *[]overlay.Row, logs []agentclient.RuntimeLogEntry) {
	*rows = append(*rows, overlay.Row{
		Label:    "recent logs",
		Value:    countLabel(len(logs), "entry"),
		ReadOnly: true,
	})
	if len(logs) == 0 {
		*rows = append(*rows, overlay.Row{Label: "log", Value: "no runtime logs yet", ReadOnly: true})
		return
	}
	start := 0
	if len(logs) > maxDashboardLogs {
		start = len(logs) - maxDashboardLogs
	}
	for _, entry := range logs[start:] {
		*rows = append(*rows, overlay.Row{
			Label:    logLabel(entry),
			Value:    shorten(entry.Message, 96),
			Hint:     logHint(entry),
			ReadOnly: true,
		})
	}
}

func runtimeDashboardActionCmd(ag *agentclient.Client, action runtimeDashboardAction) tea.Cmd {
	return func() tea.Msg {
		if ag == nil {
			return runtimeDashboardActionMsg{Status: "action failed: agent client unavailable"}
		}
		switch action.Kind {
		case runtimeActionDownload:
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			model, err := ag.DownloadRuntimeModel(ctx, action.Runtime, action.ModelID)
			if err != nil {
				return runtimeDashboardActionMsg{Status: "download failed: " + err.Error(), CatalogMessage: "download failed"}
			}
			return runtimeDashboardActionMsg{
				Status:         "downloading " + shortModelName(firstNonEmpty(model.DisplayName, model.ID, action.ModelID)),
				CatalogMessage: downloadStatusText(*model),
			}
		case runtimeActionStart:
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			instance, err := ag.StartRuntimeModel(ctx, action.Runtime, action.ModelID)
			if err != nil {
				return runtimeDashboardActionMsg{Status: "start failed: " + err.Error()}
			}
			return runtimeDashboardActionMsg{Status: "started " + shortModelName(instance.ModelID)}
		case runtimeActionStop:
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := ag.StopRuntimeModel(ctx, action.InstanceID); err != nil {
				return runtimeDashboardActionMsg{Status: "stop failed: " + err.Error()}
			}
			return runtimeDashboardActionMsg{Status: "stopped " + shortID(action.InstanceID)}
		case runtimeActionRestart:
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			instance, err := ag.RestartRuntime(ctx, action.InstanceID, action.Runtime, action.ModelID)
			if err != nil {
				return runtimeDashboardActionMsg{Status: "restart failed: " + err.Error()}
			}
			return runtimeDashboardActionMsg{Status: "restarted " + shortModelName(instance.ModelID)}
		case runtimeActionCancel:
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			model, err := ag.CancelRuntimeModelDownload(ctx, action.Runtime, action.ModelID)
			if err != nil {
				return runtimeDashboardActionMsg{Status: "cancel failed: " + err.Error(), CatalogMessage: "cancel failed"}
			}
			return runtimeDashboardActionMsg{
				Status:         "cancelled " + shortModelName(firstNonEmpty(model.DisplayName, model.ID, action.ModelID)),
				CatalogMessage: downloadStatusText(*model),
			}
		case runtimeActionDelete:
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := ag.DeleteRuntimeModel(ctx, action.Runtime, action.ModelID); err != nil {
				return runtimeDashboardActionMsg{Status: "delete failed: " + err.Error(), CatalogMessage: "delete failed"}
			}
			return runtimeDashboardActionMsg{
				Status:         "deleted " + shortModelName(action.ModelID),
				CatalogMessage: "deleted " + shortModelName(action.ModelID),
			}
		default:
			return runtimeDashboardActionMsg{Status: "action failed: unsupported action " + action.Kind}
		}
	}
}

func runtimeDashboardDownloadCmd(ag *agentclient.Client, model agentclient.RuntimeModel) tea.Cmd {
	return func() tea.Msg {
		if ag == nil {
			return runtimeDashboardActionMsg{
				Status:         "download failed: agent client unavailable",
				CatalogMessage: "agent client unavailable",
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		download, err := ag.DownloadRuntimeModel(ctx, model.Runtime, model.ID)
		if err != nil {
			return runtimeDashboardActionMsg{
				Status:         "download failed: " + err.Error(),
				CatalogMessage: "download failed",
			}
		}
		name := shortModelName(firstNonEmpty(download.DisplayName, download.ID, model.DisplayName, model.ID))
		switch strings.ToLower(download.DownloadState) {
		case "downloaded":
			return runtimeDashboardActionMsg{
				Status:         "downloaded " + name,
				CatalogMessage: "downloaded " + name,
			}
		case "downloading":
			return runtimeDashboardActionMsg{
				Status:         "downloading " + name,
				CatalogMessage: downloadStatusText(*download),
			}
		default:
			return runtimeDashboardActionMsg{
				Status:         "download queued " + name,
				CatalogMessage: downloadStatusText(*download),
			}
		}
	}
}

func runtimeDashboardPendingStatus(action runtimeDashboardAction) string {
	switch action.Kind {
	case runtimeActionDownload:
		return "starting download for " + shortModelName(action.ModelID) + "..."
	case runtimeActionStart:
		return "starting " + shortModelName(action.ModelID) + "..."
	case runtimeActionStop:
		return "stopping " + shortID(action.InstanceID) + "..."
	case runtimeActionRestart:
		return "restarting " + shortModelName(action.ModelID) + "..."
	case runtimeActionCancel:
		return "cancelling download for " + shortModelName(action.ModelID) + "..."
	case runtimeActionDelete:
		return "deleting " + shortModelName(action.ModelID) + "..."
	default:
		return "running action..."
	}
}

func downloadStatusText(model agentclient.RuntimeModel) string {
	state := strings.TrimSpace(model.DownloadState)
	if state == "" {
		return ""
	}
	switch strings.ToLower(state) {
	case "downloaded":
		return "complete"
	case "not_downloaded":
		return "available"
	case "downloading":
		if percent, ok := downloadPercent(model); ok {
			return fmt.Sprintf("downloading %.0f%% (%s)", percent, downloadByteStatus(model))
		}
		if bytes := downloadByteStatus(model); bytes != "" {
			return "downloading " + bytes
		}
		return "downloading"
	case "failed":
		return "failed"
	case "cancelled":
		return "cancelled"
	default:
		return state
	}
}

func compactDownloadStatusText(model agentclient.RuntimeModel) string {
	switch strings.ToLower(strings.TrimSpace(model.DownloadState)) {
	case "downloaded":
		return "complete"
	case "downloading":
		if percent, ok := downloadPercent(model); ok {
			return fmt.Sprintf("%.0f%%", percent)
		}
		return "downloading"
	case "failed":
		return "failed"
	case "cancelled":
		return "cancelled"
	case "not_downloaded":
		return "available"
	default:
		return strings.TrimSpace(model.DownloadState)
	}
}

func downloadPercent(model agentclient.RuntimeModel) (float64, bool) {
	total := model.DownloadTotalBytes
	if total == 0 {
		total = model.SizeBytes
	}
	if total <= 0 {
		return 0, false
	}
	downloaded := model.DownloadedBytes
	if strings.EqualFold(model.DownloadState, "downloaded") && downloaded == 0 {
		downloaded = total
	}
	if downloaded < 0 {
		downloaded = 0
	}
	if downloaded > total {
		downloaded = total
	}
	return float64(downloaded) / float64(total) * 100, true
}

func downloadByteStatus(model agentclient.RuntimeModel) string {
	total := model.DownloadTotalBytes
	if total == 0 {
		total = model.SizeBytes
	}
	downloaded := model.DownloadedBytes
	if strings.EqualFold(model.DownloadState, "downloaded") && downloaded == 0 {
		downloaded = total
	}
	if total > 0 {
		return formatDownloadBytes(downloaded) + "/" + formatDownloadBytes(total)
	}
	if downloaded > 0 {
		return formatDownloadBytes(downloaded)
	}
	return ""
}

func downloadProgressBar(model agentclient.RuntimeModel, width int) string {
	if width < 4 {
		return ""
	}
	percent, ok := downloadPercent(model)
	if !ok {
		return ""
	}
	inner := width - 2
	filled := int(percent/100*float64(inner) + 0.5)
	filled = clampInt(filled, 0, inner)
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", inner-filled) + "]"
}

func formatDownloadBytes(size int64) string {
	if size <= 0 {
		return "0 B"
	}
	return formatBytes(size)
}

func encodeRuntimeDashboardAction(action runtimeDashboardAction) string {
	return strings.Join([]string{action.Kind, action.Runtime, action.ModelID, action.InstanceID}, runtimeActionSep)
}

func parseRuntimeDashboardAction(key string) (runtimeDashboardAction, error) {
	parts := strings.Split(key, runtimeActionSep)
	if len(parts) != 4 {
		return runtimeDashboardAction{}, fmt.Errorf("invalid action key")
	}
	action := runtimeDashboardAction{
		Kind:       parts[0],
		Runtime:    parts[1],
		ModelID:    parts[2],
		InstanceID: parts[3],
	}
	switch action.Kind {
	case runtimeActionDownload:
		if action.Runtime == "" || action.ModelID == "" {
			return runtimeDashboardAction{}, fmt.Errorf("download needs runtime and model")
		}
	case runtimeActionStart:
		if action.Runtime == "" || action.ModelID == "" {
			return runtimeDashboardAction{}, fmt.Errorf("start needs runtime and model")
		}
	case runtimeActionStop:
		if action.InstanceID == "" {
			return runtimeDashboardAction{}, fmt.Errorf("stop needs instance")
		}
	case runtimeActionRestart:
		if action.Runtime == "" || action.ModelID == "" || action.InstanceID == "" {
			return runtimeDashboardAction{}, fmt.Errorf("restart needs instance, runtime, and model")
		}
	case runtimeActionCancel:
		if action.Runtime == "" || action.ModelID == "" {
			return runtimeDashboardAction{}, fmt.Errorf("cancel needs runtime and model")
		}
	case runtimeActionDelete:
		if action.Runtime == "" || action.ModelID == "" {
			return runtimeDashboardAction{}, fmt.Errorf("delete needs runtime and model")
		}
	default:
		return runtimeDashboardAction{}, fmt.Errorf("unsupported action %q", action.Kind)
	}
	return action, nil
}

func canStartRuntimeModel(model agentclient.RuntimeModel, running agentclient.RuntimeInstance) bool {
	return model.Runtime == "llama_server" &&
		model.ID != "" &&
		strings.EqualFold(model.DownloadState, "downloaded") &&
		running.ID == ""
}

func canDeleteRuntimeModel(model agentclient.RuntimeModel) bool {
	return model.Runtime != "" &&
		model.ID != "" &&
		model.DownloadURL != "" &&
		!strings.EqualFold(model.DownloadState, "downloading")
}

func downloadedRuntimeModels(models []agentclient.RuntimeModel) []agentclient.RuntimeModel {
	out := make([]agentclient.RuntimeModel, 0, len(models))
	for _, model := range models {
		if strings.EqualFold(model.DownloadState, "downloaded") {
			out = append(out, model)
		}
	}
	return out
}

func downloadJobModels(models []agentclient.RuntimeModel) []agentclient.RuntimeModel {
	out := make([]agentclient.RuntimeModel, 0, len(models))
	for _, model := range models {
		switch strings.ToLower(model.DownloadState) {
		case "downloading", "failed", "cancelled":
			out = append(out, model)
		}
	}
	return out
}

func downloadActionValue(model agentclient.RuntimeModel) string {
	status := downloadStatusText(model)
	bar := downloadProgressBar(model, 16)
	return strings.Join(nonEmptyParts(bar, status), " ")
}

func downloadActionHint(model agentclient.RuntimeModel) string {
	if model.DownloadError != "" {
		return "error: " + shorten(model.DownloadError, 56)
	}
	return strings.Join(nonEmptyParts(model.Runtime, model.Family, model.Quantization, formatBytes(model.SizeBytes)), " | ")
}

func activeInstancesByModel(instances []agentclient.RuntimeInstance) map[string]agentclient.RuntimeInstance {
	active := make(map[string]agentclient.RuntimeInstance, len(instances))
	for _, instance := range instances {
		if instance.Runtime == "" || instance.ModelID == "" || instance.State == "stopped" {
			continue
		}
		active[runtimeModelKey(instance.Runtime, instance.ModelID)] = instance
	}
	return active
}

func runtimeModelKey(runtimeName, modelID string) string {
	return runtimeName + "\x00" + modelID
}

func endpointLabel(endpoint agentclient.RuntimeEndpoint) string {
	name := firstNonEmpty(endpoint.DisplayName, endpoint.ID, endpoint.Kind, "endpoint")
	return "endpoint " + name
}

func endpointValue(endpoint agentclient.RuntimeEndpoint) string {
	parts := nonEmptyParts(endpoint.State, endpoint.Kind, endpoint.Scope, endpoint.BaseURL)
	if endpoint.LatencyMS > 0 {
		parts = append(parts, fmt.Sprintf("%dms", endpoint.LatencyMS))
	}
	if endpoint.LastError != "" {
		parts = append(parts, "error: "+shorten(endpoint.LastError, 48))
	}
	return strings.Join(parts, " | ")
}

func endpointHint(endpoint agentclient.RuntimeEndpoint) string {
	parts := make([]string, 0, 3)
	if endpoint.AuthState != "" {
		parts = append(parts, "auth:"+endpoint.AuthState)
	}
	if len(endpoint.ActiveRoles) > 0 {
		parts = append(parts, strings.Join(endpoint.ActiveRoles, ","))
	}
	if len(endpoint.Models) > 0 {
		parts = append(parts, "models:"+shorten(strings.Join(endpoint.Models, ","), 36))
	}
	return strings.Join(parts, " | ")
}

func modelLabel(model agentclient.RuntimeModel) string {
	return "model " + firstNonEmpty(model.DisplayName, shortModelName(model.ID), model.ID, "unknown")
}

func modelValue(model agentclient.RuntimeModel, running agentclient.RuntimeInstance) string {
	parts := nonEmptyParts(model.Runtime, model.DownloadState, model.RuntimeState, model.Family, model.Quantization, formatBytes(model.SizeBytes))
	if running.ID != "" {
		parts = append(parts, "process:"+running.State)
	}
	return strings.Join(parts, " | ")
}

func modelHint(model agentclient.RuntimeModel, running agentclient.RuntimeInstance) string {
	parts := make([]string, 0, 4)
	if model.Active {
		parts = append(parts, "active config")
	}
	caps := runtimeCapabilitiesHint(model.SupportsChat, model.SupportsEmbed, model.SupportsTools)
	if caps != "" {
		parts = append(parts, caps)
	}
	if running.ID != "" {
		parts = append(parts, "pid:"+fmt.Sprint(running.PID))
	}
	return strings.Join(parts, " | ")
}

func instanceLabel(instance agentclient.RuntimeInstance) string {
	return "process " + shortModelName(instance.ModelID)
}

func instanceValue(instance agentclient.RuntimeInstance) string {
	parts := nonEmptyParts(instance.State, instance.Runtime)
	if instance.PID > 0 {
		parts = append(parts, fmt.Sprintf("pid:%d", instance.PID))
	}
	if instance.Endpoint != "" {
		parts = append(parts, instance.Endpoint)
	}
	if instance.RestartCount > 0 {
		parts = append(parts, fmt.Sprintf("restarts:%d", instance.RestartCount))
	}
	if instance.LastError != "" {
		parts = append(parts, "error: "+shorten(instance.LastError, 48))
	}
	return strings.Join(parts, " | ")
}

func instanceHint(instance agentclient.RuntimeInstance) string {
	parts := nonEmptyParts(shortID(instance.ID))
	if !instance.StartedAt.IsZero() {
		parts = append(parts, "started "+relativeTime(instance.StartedAt))
	}
	if instance.LogPath != "" {
		parts = append(parts, filepath.Base(instance.LogPath))
	}
	return strings.Join(parts, " | ")
}

func logLabel(entry agentclient.RuntimeLogEntry) string {
	source := firstNonEmpty(entry.Source, "log")
	if entry.Timestamp.IsZero() {
		return source
	}
	return entry.Timestamp.Format("15:04:05") + " " + source
}

func logHint(entry agentclient.RuntimeLogEntry) string {
	return strings.Join(nonEmptyParts(entry.Level, entry.RuntimeID, shortModelName(entry.ModelID)), " | ")
}

func runtimeCapabilitiesHint(chat, embed, tools bool) string {
	var caps []string
	if chat {
		caps = append(caps, "chat")
	}
	if embed {
		caps = append(caps, "embed")
	}
	if tools {
		caps = append(caps, "tools")
	}
	return strings.Join(caps, ",")
}

func appendMoreRow(rows *[]overlay.Row, label string, count int) {
	*rows = append(*rows, overlay.Row{
		Label:    label,
		Value:    fmt.Sprintf("%d more hidden", count),
		ReadOnly: true,
	})
}

func countLabel(count int, singular string) string {
	if count == 1 {
		return "1 " + singular
	}
	plural := singular + "s"
	switch singular {
	case "entry":
		plural = "entries"
	case "process":
		plural = "processes"
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func nonEmptyParts(values ...string) []string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, value)
		}
	}
	return parts
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func shortModelName(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return "unknown"
	}
	base := filepath.Base(modelID)
	if base == "." || base == string(filepath.Separator) {
		base = modelID
	}
	return shorten(base, 44)
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func clampIndex(idx, length int) int {
	if length <= 0 {
		return 0
	}
	if idx < 0 {
		return 0
	}
	if idx >= length {
		return length - 1
	}
	return idx
}

func padRightPlain(value string, width int) string {
	pad := width - lipgloss.Width(value)
	if pad <= 0 {
		return value
	}
	return value + strings.Repeat(" ", pad)
}

func padRightStyled(value string, width int) string {
	pad := width - lipgloss.Width(value)
	if pad <= 0 {
		return value
	}
	return value + strings.Repeat(" ", pad)
}

func truncatePlain(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || lipgloss.Width(value) <= max {
		return value
	}
	if max <= 3 {
		runes := []rune(value)
		if len(runes) <= max {
			return value
		}
		return string(runes[:max])
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max-3]) + "..."
}

func shorten(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}

func formatBytes(size int64) string {
	if size <= 0 {
		return ""
	}
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	units := []string{"KB", "MB", "GB", "TB"}
	for _, suffix := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f PB", value/unit)
}

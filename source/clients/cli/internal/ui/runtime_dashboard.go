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

	maxDashboardModels    = 12
	maxDashboardInstances = 8
	maxDashboardLogs      = 10
	maxCatalogRows        = 12
	minCatalogRows        = 10
)

type runtimeDashboardFocus int

const (
	runtimeFocusCatalog runtimeDashboardFocus = iota
	runtimeFocusActions
)

// runtimeDashboard owns the model-management content page. It keeps the global
// chrome in the root model and renders native dashboard sections in the page
// body so each section can get its own width and height budget.
type runtimeDashboard struct {
	width, height int
	palette       theme.Palette
	styles        theme.Styles
	agent         *agentclient.Client
	snapshot      runtimeDashboardSnapshot
	// estimates caches resolved RAM estimates by estimateKey;
	// estimatePending tracks in-flight fetches so cursor movement
	// doesn't double-dispatch. See runtime_estimate.go.
	estimates       map[string]agentclient.ModelRAMEstimate
	estimatePending map[string]bool
	focus           runtimeDashboardFocus
	catalogSearch   textinput.Model
	catalogCursor   int
	catalogTop      int
	catalogMessage  string
	catalogBusy     bool
	operationCursor int
	// renderActionOrdinal is render-pass state: the running count of
	// actionable rows emitted so far across all action blocks, reset
	// by fullContent. Lets renderActionBlock map the flat
	// operationCursor onto whichever block the cursor's row lives in.
	renderActionOrdinal int
	// blockStartLine / selectedActionLine are render-pass state too:
	// fullContent records where each action block starts, and
	// renderActionBlock records the absolute line of the selected
	// action row — what scrollFollowAction uses to keep the selection
	// on screen while arrowing through blocks below the fold (the
	// tiers section was unreachable-by-sight without this).
	blockStartLine     int
	selectedActionLine int
	actionMessage      string
	scrollOffset       int
	// tierPicker, when non-nil, is the floating model picker for a taxonomy
	// slot; it captures all key input until closed.
	tierPicker *overlay.RowList
}

type runtimeDashboardSnapshot struct {
	Config     *agentclient.Config
	ConfigErr  error
	Status     *agentclient.RuntimeStatus
	StatusErr  error
	Catalog    agentclient.RuntimeModelCatalog
	CatalogErr error
}

type runtimeDashboardAction struct {
	Kind       string
	Runtime    string
	ModelID    string
	InstanceID string
	// TierKey names the model-taxonomy slot for tier-pick actions
	// ("default_provider" or "<tier>.<provider>").
	TierKey string
}

type runtimeDashboardActionMsg struct {
	Status         string
	CatalogMessage string
}

type runtimeDashboardActionRow struct {
	Label  string
	Value  string
	Hint   string
	Action runtimeDashboardAction
}

type runtimeDashboardRefreshMsg struct{}

// refreshTick schedules the next snapshot reload: fast while a
// download is streaming so progress feels live, relaxed otherwise.
// The loop must ALWAYS reschedule while the page is open — gating its
// survival on hasActiveDownloads meant a single failed or momentarily
// inconsistent status fetch killed live updates until the page was
// reopened.
func (d *runtimeDashboard) refreshTick() tea.Cmd {
	interval := 2 * time.Second
	if d.hasActiveDownloads() {
		interval = 500 * time.Millisecond
	}
	return tea.Tick(interval, func(time.Time) tea.Msg { return runtimeDashboardRefreshMsg{} })
}

func newRuntimeDashboard(ag *agentclient.Client, p theme.Palette, s theme.Styles, w, h int) (*runtimeDashboard, tea.Cmd) {
	search := textinput.New()
	search.Prompt = ""
	search.Placeholder = "Search catalog models"
	search.CharLimit = 0
	search.SetWidth(32)
	blinkCmd := search.Focus()
	dashboard := &runtimeDashboard{
		palette:         p,
		styles:          s,
		agent:           ag,
		width:           w,
		height:          h,
		focus:           runtimeFocusCatalog,
		catalogSearch:   search,
		estimates:       make(map[string]agentclient.ModelRAMEstimate),
		estimatePending: make(map[string]bool),
	}
	dashboard.snapshot = loadRuntimeDashboardSnapshot(ag)
	return dashboard, tea.Batch(blinkCmd, dashboard.maybeFetchEstimate())
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
	if d.tierPicker != nil {
		next, cmd, closed := d.tierPicker.Update(msg, d.styles)
		if closed {
			d.tierPicker = nil
			// Refresh so the tiers section reflects a just-applied change;
			// batch the picker's own cmd so a switch bubbling up (e.g. the
			// open-runtime install modal) isn't dropped.
			return tea.Batch(cmd, d.refreshSnapshot()), false
		}
		d.tierPicker = &next
		return cmd, false
	}
	switch msg.String() {
	case "tab":
		d.advanceSection(1)
		return nil, false
	case "shift+tab":
		d.advanceSection(-1)
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
	return d.updateOperations(msg)
}

func (d *runtimeDashboard) View() string {
	if d.tierPicker != nil {
		// The picker floats over the dashboard as a modal instead of
		// replacing the page — the user keeps their bearings (the tier
		// row they came from stays visible underneath).
		full, contentH := d.fullContent()
		base := d.renderScrollableContent(full, contentH)
		boxW := d.width - 8
		if boxW > 72 {
			boxW = 72
		}
		if boxW < 40 {
			boxW = 40
		}
		box := d.tierPicker.ViewPanel(boxW, d.palette, d.styles)
		x := (d.width - boxW) / 2
		if x < 0 {
			x = 0
		}
		y := (contentH - countLines([]string{box})) / 2
		if y < 1 {
			y = 1
		}
		return composeOverlay(base, box, x, y)
	}
	full, contentH := d.fullContent()
	return d.renderScrollableContent(full, contentH)
}

func (d *runtimeDashboard) fullContent() (string, int) {
	// The action blocks below (downloads, installed models, processes,
	// tiers) share one flat cursor space — operationRows() — so the
	// selection ordinal must run continuously across them rather than
	// restarting per block. Reset once per render pass.
	d.renderActionOrdinal = 0
	d.selectedActionLine = -1
	configBlock := d.renderConfigBlocks()
	contentH := dashboardContentHeight(d.height)
	parts := []string{
		configBlock,
		d.renderRuntimeStatusBlock(),
		d.renderCatalogBlock(d.catalogRowBudget()),
	}
	// Action blocks render one at a time so renderActionBlock can
	// translate its block-local selected row into an absolute line.
	for _, render := range []func() string{
		d.renderOpenModelBlock,
		d.renderDownloadsBlock,
		d.renderInstalledModelsBlock,
		d.renderProcessesBlock,
		d.renderTiersBlock,
	} {
		d.blockStartLine = countLines(parts)
		parts = append(parts, render())
	}
	logRows := contentH - countLines(parts)
	parts = append(parts, d.renderOpenServerLogBlock(logRows))
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
	d.snapshot = loadRuntimeDashboardSnapshot(d.agent)
	d.clampOperationCursor()
	if msg.CatalogMessage != "" {
		d.catalogMessage = msg.CatalogMessage
	}
	d.actionMessage = msg.Status
	return d.refreshTick()
}

func (d *runtimeDashboard) refreshSnapshot() tea.Cmd {
	d.snapshot = loadRuntimeDashboardSnapshot(d.agent)
	d.clampOperationCursor()
	return d.refreshTick()
}

func (d *runtimeDashboard) hasActiveDownloads() bool {
	for _, model := range runtimeStatusModels(d.snapshot.Status) {
		if strings.EqualFold(model.DownloadState, "downloading") {
			return true
		}
	}
	return false
}

// sectionStarts returns the flat operations-cursor index of the first
// actionable row in each action section (downloads, installed models,
// processes, model tiers), skipping sections that currently have no
// actionable rows. Must enumerate in the same order as operationRows.
func (d *runtimeDashboard) sectionStarts() []int {
	blocks := [][]runtimeDashboardActionRow{
		openModelRows(d.snapshot.Config),
		d.downloadRows(),
		d.installedModelRows(),
		d.processRows(),
		tierRows(d.snapshot.Config),
	}
	var starts []int
	ordinal := 0
	for _, rows := range blocks {
		n := 0
		for _, row := range rows {
			if row.Action.Kind != "" {
				n++
			}
		}
		if n > 0 {
			starts = append(starts, ordinal)
		}
		ordinal += n
	}
	return starts
}

// advanceSection moves focus one SECTION forward (dir=+1, tab) or
// backward (dir=-1, shift+tab): catalog → each action section's first
// row → wraps back to catalog. This is what makes the model-tiers
// section directly tabbable instead of requiring a dozen arrow
// presses through the sections above it.
func (d *runtimeDashboard) advanceSection(dir int) {
	starts := d.sectionStarts()
	if d.focus == runtimeFocusCatalog {
		if len(starts) == 0 {
			return
		}
		d.focus = runtimeFocusActions
		d.catalogSearch.Blur()
		d.catalogMessage = ""
		if dir > 0 {
			d.operationCursor = starts[0]
		} else {
			// shift+tab from the catalog wraps to the LAST section —
			// one gesture straight to model tiers.
			d.operationCursor = starts[len(starts)-1]
		}
		d.scrollFollowAction()
		return
	}
	current := 0
	for i, s := range starts {
		if d.operationCursor >= s {
			current = i
		}
	}
	next := current + dir
	if next < 0 || next >= len(starts) {
		d.focus = runtimeFocusCatalog
		_ = d.catalogSearch.Focus()
		return
	}
	d.operationCursor = starts[next]
	d.scrollFollowAction()
}

// scrollFollowAction scrolls the page so the selected action row is
// visible. fullContent recomputes selectedActionLine as a side effect
// of rendering, so a state-only recompute is a full render — cheap at
// terminal sizes, and identical to what View would produce.
func (d *runtimeDashboard) scrollFollowAction() {
	_, contentH := d.fullContent()
	if d.selectedActionLine < 0 || contentH <= 0 {
		return
	}
	if d.selectedActionLine < d.scrollOffset {
		d.scrollOffset = maxInt(0, d.selectedActionLine-1)
	} else if d.selectedActionLine >= d.scrollOffset+contentH {
		d.scrollOffset = d.selectedActionLine - contentH + 2
	}
	d.clampScroll()
}

func (d *runtimeDashboard) updateOperations(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	actions := d.operationActions()
	switch msg.String() {
	case "esc":
		return nil, true
	case "up":
		if d.operationCursor > 0 {
			d.operationCursor--
		}
		d.scrollFollowAction()
		return nil, false
	case "down":
		if d.operationCursor < len(actions)-1 {
			d.operationCursor++
		}
		d.scrollFollowAction()
		return nil, false
	case "home":
		d.operationCursor = 0
		d.scrollFollowAction()
		return nil, false
	case "end":
		if len(actions) > 0 {
			d.operationCursor = len(actions) - 1
		}
		d.scrollFollowAction()
		return nil, false
	case "enter":
		if len(actions) == 0 {
			d.actionMessage = "no action selected"
			return nil, false
		}
		d.operationCursor = clampIndex(d.operationCursor, len(actions))
		action := actions[d.operationCursor]
		switch action.Kind {
		case runtimeActionTierPick:
			d.openTierPicker(action.TierKey)
			return nil, false
		case runtimeActionOpenRuntimePick:
			d.openRuntimePicker()
			return nil, false
		case runtimeActionOpenModelPick:
			d.openOpenModelPicker()
			return nil, false
		case runtimeActionOllamaURL:
			d.openOllamaURLPicker()
			return nil, false
		}
		d.actionMessage = runtimeDashboardPendingStatus(action)
		return runtimeDashboardActionCmd(d.agent, action), false
	}
	return nil, false
}

func (d *runtimeDashboard) operationActions() []runtimeDashboardAction {
	var actions []runtimeDashboardAction
	for _, row := range d.operationRows() {
		if row.Action.Kind != "" {
			actions = append(actions, row.Action)
		}
	}
	return actions
}

func (d *runtimeDashboard) clampOperationCursor() {
	actions := d.operationActions()
	if len(actions) == 0 {
		d.operationCursor = 0
		return
	}
	d.operationCursor = clampIndex(d.operationCursor, len(actions))
}

func (d *runtimeDashboard) updateCatalog(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+r":
		if d.catalogBusy {
			d.catalogMessage = "refresh already running"
			return nil, false
		}
		d.catalogBusy = true
		d.catalogMessage = "refreshing catalog..."
		return runtimeCatalogRefreshCmd(d.agent), false
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
		d.keepCatalogCursorVisible(d.catalogRowBudget(), len(filteredCatalogModels(d.catalogModels(), d.catalogSearch.Value())))
		return d.maybeFetchEstimate(), false
	case "down":
		models := filteredCatalogModels(d.catalogModels(), d.catalogSearch.Value())
		if d.catalogCursor < len(models)-1 {
			d.catalogCursor++
		}
		d.keepCatalogCursorVisible(d.catalogRowBudget(), len(models))
		return d.maybeFetchEstimate(), false
	case "home":
		d.catalogCursor = 0
		d.catalogTop = 0
		return d.maybeFetchEstimate(), false
	case "end":
		models := filteredCatalogModels(d.catalogModels(), d.catalogSearch.Value())
		if len(models) > 0 {
			d.catalogCursor = len(models) - 1
		}
		d.keepCatalogCursorVisible(d.catalogRowBudget(), len(models))
		return d.maybeFetchEstimate(), false
	case "enter":
		models := filteredCatalogModels(d.catalogModels(), d.catalogSearch.Value())
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
		d.catalogTop = 0
		d.catalogMessage = ""
		cmd = tea.Batch(cmd, d.maybeFetchEstimate())
	}
	return cmd, false
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

	catalogCtx, catalogCancel := context.WithTimeout(context.Background(), 3*time.Second)
	snap.Catalog, snap.CatalogErr = ag.ListRuntimeModels(catalogCtx)
	catalogCancel()

	return snap
}

// catalogModels is the model list backing the download-catalog pane.
// ListRuntimeModels is the richer source (it merges the online Ollama
// library and carries the fetch timestamp); the runtime-status model
// list is the fallback when that RPC failed, so the pane degrades to
// the pre-online behavior instead of going blank.
func (d *runtimeDashboard) catalogModels() []agentclient.RuntimeModel {
	if d.snapshot.CatalogErr == nil && len(d.snapshot.Catalog.Models) > 0 {
		return d.snapshot.Catalog.Models
	}
	return runtimeStatusModels(d.snapshot.Status)
}

type runtimeCatalogRefreshDoneMsg struct {
	result agentclient.CatalogRefreshResult
}

func runtimeCatalogRefreshCmd(ag *agentclient.Client) tea.Cmd {
	return func() tea.Msg {
		if ag == nil {
			return runtimeCatalogRefreshDoneMsg{result: agentclient.CatalogRefreshResult{Err: errors.New("agent client unavailable")}}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return runtimeCatalogRefreshDoneMsg{result: ag.RefreshOnlineCatalog(ctx)}
	}
}

func (d *runtimeDashboard) applyCatalogRefresh(msg runtimeCatalogRefreshDoneMsg) tea.Cmd {
	d.catalogBusy = false
	if msg.result.Err != nil {
		d.catalogMessage = "refresh failed: " + msg.result.Err.Error()
		return nil
	}
	d.catalogMessage = fmt.Sprintf("catalog refreshed · %d models", msg.result.ModelCount)
	return tea.Batch(d.refreshSnapshot(), d.maybeFetchEstimate())
}

type runtimeDashboardField struct {
	Label string
	Value string
	Hint  string
}

// renderConfigBlocks shows only the local runtime config: cloud configuration
// has a whole tab of its own (Cloud), so the dashboard no longer mirrors it.
func (d *runtimeDashboard) renderConfigBlocks() string {
	totalW := dashboardPanelWidth(d.width)
	return renderRuntimeDashboardBlock("local config", localConfigFields(d.snapshot), totalW, d.palette, d.styles)
}

func (d *runtimeDashboard) renderRuntimeStatusBlock() string {
	totalW := dashboardPanelWidth(d.width)
	contentW := dashboardBlockContentWidth(totalW)
	status := d.snapshot.Status
	cfg := d.snapshot.Config
	runtimeName := "llama_server"
	if cfg != nil {
		runtimeName = firstNonEmpty(cfg.OpenRuntime, runtimeName)
	}
	var running []agentclient.RuntimeInstance
	for _, instance := range runtimeStatusInstances(status) {
		if instance.Runtime == runtimeName || instance.Runtime == "llama_server" {
			running = append(running, instance)
		}
	}
	serverState := "not running"
	endpoint := ""
	lastError := ""
	if len(running) > 0 {
		first := running[0]
		parts := nonEmptyParts(first.State, shortModelName(first.ModelID))
		if first.PID > 0 {
			parts = append(parts, fmt.Sprintf("pid:%d", first.PID))
		}
		serverState = strings.Join(parts, " | ")
		endpoint = first.Endpoint
		lastError = first.LastError
	}
	models := runtimeStatusModels(status)
	fields := []runtimeDashboardField{
		{Label: "server", Value: serverState},
		{Label: "endpoint", Value: firstNonEmpty(endpoint, "none")},
		{Label: "installed", Value: countLabel(len(downloadedRuntimeModels(models)), "model")},
		{Label: "downloads", Value: countLabel(len(downloadJobModels(models)), "job")},
	}
	if lastError != "" {
		fields = append(fields, runtimeDashboardField{Label: "last error", Value: lastError})
	}
	if d.snapshot.StatusErr != nil {
		fields = []runtimeDashboardField{{Label: "status", Value: d.snapshot.StatusErr.Error(), Hint: "error"}}
	}
	return renderRuntimeDashboardTextBlock("runtime status", []string{renderRuntimeStatusLine(fields, contentW, d.styles)}, totalW, 0, d.palette, d.styles)
}

func renderRuntimeStatusLine(fields []runtimeDashboardField, width int, styles theme.Styles) string {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		value := firstNonEmpty(field.Value, "(unset)")
		part := styles.Muted.Render(field.Label+": ") + styles.Primary.Render(value)
		if field.Hint != "" {
			part += styles.BorderDim.Render(" " + field.Hint)
		}
		parts = append(parts, part)
	}
	line := strings.Join(parts, styles.BorderDim.Render(" │ "))
	if lipgloss.Width(line) > width {
		line = ansi.Truncate(line, width, "")
	}
	return line
}

func (d *runtimeDashboard) renderOpenServerLogBlock(height int) string {
	totalW := dashboardPanelWidth(d.width)
	contentW := dashboardBlockContentWidth(totalW)
	logs := openModelServerLogs(runtimeStatusLogs(d.snapshot.Status))
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
	models := filteredCatalogModels(d.catalogModels(), d.catalogSearch.Value())
	if len(models) > 0 {
		d.catalogCursor = clampIndex(d.catalogCursor, len(models))
	} else {
		d.catalogCursor = 0
	}
	d.keepCatalogCursorVisible(rowLimit, len(models))

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
	est, estPending := d.selectedEstimate(selected)
	details := catalogDetailLines(selected, est, estPending, detailW, d.styles)

	lines := []string{searchLine}
	start := d.catalogTop
	for i := 0; i < rowLimit; i++ {
		left := ""
		idx := start + i
		if idx < len(models) {
			left = d.renderCatalogModelRow(models[idx], idx == d.catalogCursor, listW)
		} else if i == 0 && len(models) == 0 {
			left = d.styles.Dim.Render(catalogEmptyMessage(d.catalogModels(), d.catalogSearch.Value()))
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
			end := len(models)
			if start+rowLimit < end {
				end = start + rowLimit
			}
			lines[0] = searchLine + d.styles.BorderDim.Render(fmt.Sprintf("  %d-%d/%d", start+1, end, len(models)))
		}
	}
	lines = append(lines, d.renderCatalogFooter(contentW))
	return renderRuntimeDashboardTextBlock("download catalog", lines, totalW, 0, d.palette, d.styles)
}

// renderCatalogFooter names the online catalog's freshness. The
// timestamp is the server's last successful fetch of Ollama's library;
// zero means the online catalog has never loaded (e.g. offline first
// run) and only built-in entries are listed above.
func (d *runtimeDashboard) renderCatalogFooter(width int) string {
	var s string
	switch {
	case d.catalogBusy:
		s = "refreshing catalog..."
	case d.snapshot.Catalog.CatalogUpdatedAt.IsZero():
		s = "online catalog not loaded · ctrl+r to fetch"
	default:
		s = "catalog updated " + relativeTime(d.snapshot.Catalog.CatalogUpdatedAt) + " · ctrl+r to refresh"
	}
	return d.styles.Dim.Render(truncatePlain(s, width))
}

func (d *runtimeDashboard) catalogRowBudget() int {
	contentH := dashboardContentHeight(d.height)
	rows := contentH / 2
	if rows < minCatalogRows {
		rows = minCatalogRows
	}
	if rows > maxCatalogRows {
		rows = maxCatalogRows
	}
	return rows
}

func (d *runtimeDashboard) keepCatalogCursorVisible(rowLimit, total int) {
	if total <= 0 {
		d.catalogTop = 0
		return
	}
	if rowLimit < 1 {
		rowLimit = 1
	}
	if rowLimit > total {
		rowLimit = total
	}
	d.catalogCursor = clampIndex(d.catalogCursor, total)
	if d.catalogCursor < d.catalogTop {
		d.catalogTop = d.catalogCursor
	}
	if d.catalogCursor >= d.catalogTop+rowLimit {
		d.catalogTop = d.catalogCursor - rowLimit + 1
	}
	d.catalogTop = clampInt(d.catalogTop, 0, maxInt(0, total-rowLimit))
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
		{Label: "embedding", Value: cfg.EmbeddingModel},
		{Label: "llama-server", Value: localServerSummary(s.Status, cfg.OpenRuntime)},
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

func runtimeStatusInstances(status *agentclient.RuntimeStatus) []agentclient.RuntimeInstance {
	if status == nil {
		return nil
	}
	return status.Instances
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
	// "downloaded" stays listed on purpose — app-store semantics: a
	// model doesn't vanish from catalog search the moment its download
	// finishes; it shows as downloaded (Enter is guarded upstream).
	case "not_downloaded", "downloading", "downloaded", "failed", "cancelled":
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

func catalogDetailLines(model agentclient.RuntimeModel, est *agentclient.ModelRAMEstimate, estPending bool, width int, styles theme.Styles) []string {
	if model.ID == "" {
		return []string{
			styles.Dim.Render("Select a catalog model to see details."),
		}
	}
	caps := runtimeCapabilitiesHint(model.SupportsChat, model.SupportsEmbed, model.SupportsTools)
	sizeVal := formatBytes(model.SizeBytes)
	if sizeVal == "" && est != nil && est.Err == nil {
		// Online entries carry no size until the manifest is resolved —
		// the estimate knows the exact blob size.
		sizeVal = formatBytes(est.WeightsBytes)
	}
	lines := []string{
		detailLine("name", firstNonEmpty(model.DisplayName, shortModelName(model.ID)), width, styles),
		detailLine("family", strings.Join(nonEmptyParts(model.Family, model.Quantization), " · "), width, styles),
		detailLine("size", sizeVal, width, styles),
	}
	switch {
	case estPending:
		lines = append(lines, detailLine("memory", "estimating...", width, styles))
	case est != nil && est.Err != nil:
		lines = append(lines, detailLine("memory", "estimate unavailable — "+est.Err.Error(), width, styles))
	case est != nil:
		if mem := estimateMemoryLine(*est); mem != "" {
			lines = append(lines, detailLine("memory", mem, width, styles))
		}
		if fit := estimateFitLine(*est); fit != "" {
			lines = append(lines, detailLine("fit", fit, width, styles))
		}
	}
	lines = append(lines,
		detailLine("runtime", strings.Join(nonEmptyParts(model.Runtime, model.Source), " · "), width, styles),
		detailLine("state", strings.Join(nonEmptyParts(downloadStatusText(model), model.RuntimeState), " · "), width, styles),
		detailLine("supports", caps, width, styles),
	)
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

func openModelServerLogs(logs []agentclient.RuntimeLogEntry) []agentclient.RuntimeLogEntry {
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

func (d *runtimeDashboard) renderDownloadsBlock() string {
	rows := d.downloadRows()
	if len(rows) == 0 {
		rows = []runtimeDashboardActionRow{{Label: "download", Value: "none active"}}
	}
	return d.renderActionBlock("downloads", rows)
}

func (d *runtimeDashboard) renderInstalledModelsBlock() string {
	rows := d.installedModelRows()
	if len(rows) == 0 {
		rows = []runtimeDashboardActionRow{{Label: "model", Value: "no downloaded GGUF models found"}}
	}
	return d.renderActionBlock("installed models", rows)
}

func (d *runtimeDashboard) renderProcessesBlock() string {
	rows := d.processRows()
	if len(rows) == 0 {
		rows = []runtimeDashboardActionRow{{Label: "process", Value: "none running"}}
	}
	return d.renderActionBlock("running processes", rows)
}

func (d *runtimeDashboard) renderTiersBlock() string {
	return d.renderActionBlock("model tiers", tierRows(d.snapshot.Config))
}

func (d *runtimeDashboard) renderOpenModelBlock() string {
	return d.renderActionBlock("open model", openModelRows(d.snapshot.Config))
}

func (d *runtimeDashboard) renderActionBlock(title string, rows []runtimeDashboardActionRow) string {
	totalW := dashboardPanelWidth(d.width)
	contentW := dashboardBlockContentWidth(totalW)
	lines := make([]string, 0, len(rows)+1)
	if d.focus == runtimeFocusActions && d.actionMessage != "" {
		lines = append(lines, d.styles.BorderDim.Render(truncatePlain(d.actionMessage, contentW)))
	}
	for _, row := range rows {
		selected := false
		if row.Action.Kind != "" {
			// d.renderActionOrdinal runs across ALL action blocks in
			// this render pass (reset in fullContent) — a per-block
			// counter would highlight the Nth row of every block at
			// once.
			selected = d.focus == runtimeFocusActions && d.renderActionOrdinal == d.operationCursor
			d.renderActionOrdinal++
		}
		if selected {
			// Absolute line of this row in the assembled page: the
			// block's start + top border + title line + content index.
			d.selectedActionLine = d.blockStartLine + 2 + len(lines)
		}
		lines = append(lines, d.renderActionRow(row, selected, contentW))
	}
	return renderRuntimeDashboardTextBlock(title, lines, totalW, 0, d.palette, d.styles)
}

func (d *runtimeDashboard) renderActionRow(row runtimeDashboardActionRow, selected bool, width int) string {
	marker := "  "
	if row.Action.Kind != "" {
		marker = "  "
		if selected {
			marker = d.styles.Accent.Render("▶ ")
		}
	}
	bodyW := width - lipgloss.Width(marker)
	labelW, valueW, hintW := actionColumnWidths(bodyW)
	label := truncatePlain(row.Label, labelW)
	value := truncatePlain(row.Value, valueW)
	hint := truncatePlain(row.Hint, hintW)
	labelStyle := d.styles.Muted
	valueStyle := d.styles.Primary
	if row.Action.Kind != "" {
		labelStyle = d.styles.Primary
		if selected {
			labelStyle = d.styles.Bright
			valueStyle = d.styles.Bright
		}
	}
	line := marker +
		labelStyle.Render(padRightPlain(label, labelW)) +
		d.styles.BorderDim.Render(" │ ") +
		valueStyle.Render(padRightPlain(value, valueW))
	if hintW > 0 {
		line += d.styles.BorderDim.Render(" │ " + padRightPlain(hint, hintW))
	}
	return ansi.Truncate(line, width, "")
}

func actionColumnWidths(width int) (labelW, valueW, hintW int) {
	if width < 24 {
		return maxInt(8, width/2), maxInt(8, width-width/2-3), 0
	}
	if width < 52 {
		labelW = clampInt(width/3, 10, 18)
		valueW = maxInt(8, width-labelW-3)
		return labelW, valueW, 0
	}
	labelW = clampInt(width*28/100, 18, 34)
	hintW = clampInt(width*22/100, 14, 30)
	valueW = width - labelW - hintW - 6
	if valueW < 14 {
		hintW = 0
		valueW = width - labelW - 3
	}
	return labelW, valueW, hintW
}

func (d *runtimeDashboard) operationRows() []runtimeDashboardActionRow {
	var rows []runtimeDashboardActionRow
	rows = append(rows, openModelRows(d.snapshot.Config)...)
	rows = append(rows, d.downloadRows()...)
	rows = append(rows, d.installedModelRows()...)
	rows = append(rows, d.processRows()...)
	// Keep this order in sync with fullContent's block order so cursor
	// index → action mapping stays consistent.
	rows = append(rows, tierRows(d.snapshot.Config)...)
	return rows
}

func (d *runtimeDashboard) downloadRows() []runtimeDashboardActionRow {
	models := downloadJobModels(runtimeStatusModels(d.snapshot.Status))
	rows := make([]runtimeDashboardActionRow, 0, len(models)*3)
	for i, model := range models {
		if i >= maxDashboardModels {
			rows = append(rows, runtimeDashboardActionRow{Label: "downloads", Value: fmt.Sprintf("%d more hidden", len(models)-i)})
			break
		}
		rows = append(rows, runtimeDashboardActionRow{
			Label: firstNonEmpty(model.DisplayName, shortModelName(model.ID), model.ID),
			Value: downloadActionValue(model),
			Hint:  downloadActionHint(model),
		})
		switch strings.ToLower(model.DownloadState) {
		case "downloading":
			rows = append(rows, runtimeDashboardActionRow{
				Label:  "cancel",
				Value:  shortModelName(model.ID),
				Hint:   "enter",
				Action: runtimeDashboardAction{Kind: runtimeActionCancel, Runtime: model.Runtime, ModelID: model.ID},
			})
		case "failed", "cancelled":
			rows = append(rows, runtimeDashboardActionRow{
				Label:  "retry",
				Value:  shortModelName(model.ID),
				Hint:   "enter",
				Action: runtimeDashboardAction{Kind: runtimeActionDownload, Runtime: model.Runtime, ModelID: model.ID},
			})
			if canDeleteRuntimeModel(model) {
				rows = append(rows, runtimeDashboardActionRow{
					Label:  "delete",
					Value:  shortModelName(model.ID),
					Hint:   "remove partial file",
					Action: runtimeDashboardAction{Kind: runtimeActionDelete, Runtime: model.Runtime, ModelID: model.ID},
				})
			}
		}
	}
	return rows
}

func (d *runtimeDashboard) installedModelRows() []runtimeDashboardActionRow {
	models := downloadedRuntimeModels(runtimeStatusModels(d.snapshot.Status))
	active := activeInstancesByModel(runtimeStatusInstances(d.snapshot.Status))
	rows := make([]runtimeDashboardActionRow, 0, len(models)*3)
	for i, model := range models {
		if i >= maxDashboardModels {
			rows = append(rows, runtimeDashboardActionRow{Label: "models", Value: fmt.Sprintf("%d more hidden", len(models)-i)})
			break
		}
		running := active[runtimeModelKey(model.Runtime, model.ID)]
		rows = append(rows, runtimeDashboardActionRow{
			Label: firstNonEmpty(model.DisplayName, shortModelName(model.ID), model.ID),
			Value: modelValue(model, running),
			Hint:  modelHint(model, running),
		})
		if canStartRuntimeModel(model, running) {
			rows = append(rows, runtimeDashboardActionRow{
				Label:  "start",
				Value:  shortModelName(model.ID),
				Hint:   model.Runtime,
				Action: runtimeDashboardAction{Kind: runtimeActionStart, Runtime: model.Runtime, ModelID: model.ID},
			})
		}
		if canDeleteRuntimeModel(model) {
			rows = append(rows, runtimeDashboardActionRow{
				Label:  "delete",
				Value:  shortModelName(model.ID),
				Hint:   "remove local file",
				Action: runtimeDashboardAction{Kind: runtimeActionDelete, Runtime: model.Runtime, ModelID: model.ID},
			})
		}
	}
	return rows
}

func (d *runtimeDashboard) processRows() []runtimeDashboardActionRow {
	instances := runtimeStatusInstances(d.snapshot.Status)
	rows := make([]runtimeDashboardActionRow, 0, len(instances)*3)
	for i, instance := range instances {
		if i >= maxDashboardInstances {
			rows = append(rows, runtimeDashboardActionRow{Label: "processes", Value: fmt.Sprintf("%d more hidden", len(instances)-i)})
			break
		}
		rows = append(rows, runtimeDashboardActionRow{
			Label: shortModelName(instance.ModelID),
			Value: instanceValue(instance),
			Hint:  instanceHint(instance),
		})
		if instance.ID == "" {
			continue
		}
		if instance.State != "stopped" {
			rows = append(rows, runtimeDashboardActionRow{
				Label:  "stop",
				Value:  shortModelName(instance.ModelID),
				Hint:   instance.Runtime,
				Action: runtimeDashboardAction{Kind: runtimeActionStop, InstanceID: instance.ID},
			})
		}
		if instance.Runtime != "" && instance.ModelID != "" {
			rows = append(rows, runtimeDashboardActionRow{
				Label: "restart",
				Value: shortModelName(instance.ModelID),
				Hint:  instance.Runtime,
				Action: runtimeDashboardAction{
					Kind:       runtimeActionRestart,
					Runtime:    instance.Runtime,
					ModelID:    instance.ModelID,
					InstanceID: instance.ID,
				},
			})
		}
	}
	return rows
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

func dashboardBlockContentWidth(totalW int) int {
	contentW := totalW - 4
	if contentW < 12 {
		contentW = 12
	}
	return contentW
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
			model, err := ag.DownloadRuntimeModel(ctx, action.Runtime, action.ModelID, "")
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
		download, err := ag.DownloadRuntimeModel(ctx, model.Runtime, model.ID, model.CatalogID)
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

// modelValue summarizes an installed model: size, quant, projected RAM
// (when the server pre-warmed estimate numbers onto the record), and
// only NOTABLE state. Runtime, family, and "downloaded | stopped" are
// deliberately omitted — the row lives in the "installed models"
// section (so it's downloaded), the label carries the name, and the
// hint column carries the runtime.
func modelValue(model agentclient.RuntimeModel, running agentclient.RuntimeInstance) string {
	parts := nonEmptyParts(formatBytes(model.SizeBytes), model.Quantization)
	if model.KVBytesPerToken > 0 && model.SizeBytes > 0 && model.MaxContextTokens > 0 {
		est := agentclient.ModelRAMEstimate{
			WeightsBytes:     model.SizeBytes,
			KVBytesPerToken:  model.KVBytesPerToken,
			MaxContextTokens: model.MaxContextTokens,
		}
		ctx := min64(8192, model.MaxContextTokens)
		parts = append(parts, "~"+formatBytes(estimateTotalAt(est, ctx))+" RAM @"+fmtContextTokens(ctx))
	}
	if state := strings.ToLower(model.DownloadState); state != "" && state != "downloaded" {
		parts = append(parts, state)
	}
	if running.ID != "" {
		parts = append(parts, "process:"+running.State)
	}
	return strings.Join(parts, " · ")
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
	base := filepath.Base(stripModelIDPrefix(modelID))
	if base == "." || base == string(filepath.Separator) {
		base = modelID
	}
	return shorten(base, 44)
}

// stripModelIDPrefix removes the runtime (and source qualifier) from
// inventory-style IDs like "llama_server:catalog:qwen2.5-coder-1.5b"
// or "llama_server:f1f3470efd49" — users care about the trailing model
// name, not the namespace. Ollama-style "name:tag" refs pass through
// untouched (their first segment isn't a runtime name).
func stripModelIDPrefix(id string) string {
	parts := strings.Split(id, ":")
	if len(parts) < 2 || (parts[0] != "llama_server" && parts[0] != "ollama") {
		return id
	}
	rest := parts[1:]
	if len(rest) >= 2 && (rest[0] == "catalog" || rest[0] == "online") {
		rest = rest[1:]
	}
	return strings.Join(rest, ":")
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

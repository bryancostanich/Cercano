package ui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/form"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/clients/cli/internal/uiconfig"
	"cercano/source/server/pkg/agentclient"
)

// settingsColorMsg is emitted when the accent-color field commits. The root
// model resolves the token and updates promptBorderColor (CLI-local, mirrors
// ResultSetPromptColor).
type settingsColorMsg struct{ token string }

// settingsThemeMsg is emitted when the working theme changes (selection, edit,
// save-as, delete, or import). The root model calls applyTheme and optionally
// persists the active theme name.
type settingsThemeMsg struct {
	working     theme.Theme
	persistName string // when non-empty, persist as the active theme
}

// settingsPage is the sectioned settings content page (opened by /s, /settings,
// /config). It replaces the old flat configEditor.
type settingsPage struct {
	width, height int
	palette       theme.Palette
	styles        theme.Styles
	agent         *agentclient.Client
	accentToken   string
	form          *form.Form
	offset        int
	themes        *theme.Registry
	working       theme.Theme
	dirty         bool
	// cfg/mode cache the last successful GetConfig/GetPermissionMode results so
	// that theme-color edits (which trigger snapshotSections) don't issue a
	// gRPC round-trip per keystroke. Invalidated to nil after a successful
	// config or permission commit so the next reload re-fetches fresh values.
	cfg  *agentclient.Config
	mode string
	// Cloud provider list + inline detail-editor state. profiles/activeProfile
	// cache GetCloudProfiles like cfg caches GetConfig; profilesLoaded gates the
	// fetch. cloudSelected is the expanded row's ID ("" = none); cloudDraft holds
	// the in-progress edit; cloudDraftNew is true when creating (template/other).
	profiles       []agentclient.CloudProfileInfo
	activeProfile  string
	profilesLoaded bool
	cloudSelected  string
	cloudDraft     cloudDraft
	cloudDraftNew  bool
	// Cloud model catalog for the selected profile. Fetched lazily by
	// selectCloudRow via ListCloudProfileModels when the row is anthropic-
	// style; cleared on row-selection change so a switch between profiles
	// (each of which may have its own accessible catalog) doesn't leak
	// stale entries. cloudModelsFetched marks that the fetch has been
	// attempted (even if it failed) so the fallback list rendering path
	// isn't confused with "not yet loaded".
	cloudModels        []agentclient.CloudModelInfo
	cloudModelsFetched bool
}

func newSettingsPage(ag *agentclient.Client, p theme.Palette, s theme.Styles, accentToken string, w, h int, themes *theme.Registry, active theme.Theme) (*settingsPage, tea.Cmd) {
	sp := &settingsPage{agent: ag, palette: p, styles: s, accentToken: accentToken, width: w, height: h, themes: themes, working: active}
	sp.form = form.New(sp.snapshotSections())
	sp.form.OnCommit = sp.onCommit
	sp.form.OnReload = sp.snapshotSections
	return sp, nil
}

func (sp *settingsPage) snapshotSections() []form.Section {
	if sp.cfg == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cfg, err := sp.agent.GetConfig(ctx)
		if err != nil {
			return []form.Section{{Title: "Settings", Fields: []form.Field{
				form.NewReadOnly("error", "error", err.Error(), ""),
			}}}
		}
		mode, err := sp.agent.GetPermissionMode(ctx)
		if err != nil {
			mode = ""
		}
		sp.cfg = cfg
		sp.mode = mode
	}
	if !sp.profilesLoaded && sp.agent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if profs, active, err := sp.agent.GetCloudProfiles(ctx); err == nil {
			sp.profiles = profs
			sp.activeProfile = active
			sp.profilesLoaded = true
		}
	}
	secs := buildSettingsSections(sp.cfg, sp.mode, sp.accentToken)
	secs = append(secs, sp.buildCloudSection())
	if sp.themes != nil {
		builtin := sp.themes.IsBuiltin(sp.working.Name)
		secs = append(secs, buildThemeSections(sp.working, sp.themes.Names(), builtin, sp.dirty)...)
	}
	// Development Tools is pinned to the very bottom of the settings page —
	// below every user-facing section — so it never intrudes on the primary
	// configuration flow. Inside, related toggles cluster into Groups.
	secs = append(secs, form.Section{
		Title: "Development Tools",
		Groups: []form.Group{
			{Title: "Context Management", Fields: []form.Field{
				form.NewToggle("compaction-enabled", "compaction-enabled", sp.cfg.CompactionEnabled),
				form.NewToggle("elide-tool-results", "elide-tool-results", sp.cfg.ElideToolResults),
				form.NewToggle("lossy-tool-elision", "lossy-tool-elision", sp.cfg.LossyToolElision),
			}},
			{Title: "Data Retention", Fields: []form.Field{
				form.NewText("raw-retention-days", "raw-retention-days", strconv.Itoa(sp.cfg.RawRetentionDays), ""),
				form.NewText("compacted-retention-days", "compacted-retention-days", strconv.Itoa(sp.cfg.CompactedRetentionDays), ""),
				form.NewToggle("keep-forever", "keep-forever", sp.cfg.KeepForever),
			}},
			{Title: "Watchdog", Fields: buildDevFields(sp.cfg)},
		},
	})
	return secs
}

// SetStyles refreshes palette/styles and rebuilds the form in-place (called by
// Model.applyTheme when the settings page is active).
func (sp *settingsPage) SetStyles(s theme.Styles, p theme.Palette) {
	sp.styles = s
	sp.palette = p
	cursor := 0
	if sp.form != nil {
		cursor = sp.form.Cursor()
	}
	sp.form = form.New(sp.snapshotSections())
	sp.form.OnCommit = sp.onCommit
	sp.form.OnReload = sp.snapshotSections
	// Preserve focus across the rebuild so a live theme edit (which reconstructs
	// the form via Model.applyTheme → SetStyles) doesn't jump the cursor to top.
	sp.form.SetCursor(cursor)
}

func (sp *settingsPage) ID() contentPageID { return contentPageSettings }

func (sp *settingsPage) SetSize(w, h int) { sp.width, sp.height = w, h }

// viewportHeight is the content-region height after the root chrome (header,
// divider, prompt rules+line, status bar) is reserved — matches how
// runtimeDashboard / contextView size their scroll regions.
func (sp *settingsPage) viewportHeight() int {
	h := dashboardContentHeight(sp.height)
	if h < 1 {
		h = 1
	}
	return h
}

func (sp *settingsPage) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	cmd, closed := sp.form.Update(msg)
	sp.scrollToFocus()
	return cmd, closed
}

// scrollFocusMargin is how many rows of breathing room to keep above/below the
// focused field when scroll-following. clampScroll still bounds the offset to
// the real content, so this never pads the page with trailing whitespace — at
// the very top or bottom the margin simply collapses.
const scrollFocusMargin = 2

// scrollToFocus moves the scroll offset so the focused field stays within the
// visible viewport (with a small margin) after keyboard navigation.
func (sp *settingsPage) scrollToFocus() {
	sp.form.Lines(sp.width, sp.palette, sp.styles) // refresh focusedLine
	fl := sp.form.FocusedLine()
	vh := sp.viewportHeight()
	if fl < sp.offset+scrollFocusMargin {
		sp.offset = fl - scrollFocusMargin
	} else if fl >= sp.offset+vh-scrollFocusMargin {
		sp.offset = fl - vh + 1 + scrollFocusMargin
	}
	sp.clampScroll()
}

func (sp *settingsPage) View() string {
	lines := sp.form.Lines(sp.width, sp.palette, sp.styles)
	sp.clampScroll()
	return renderScrollable(lines, sp.viewportHeight(), sp.width-2, sp.offset, sp.styles)
}

// onCommit routes a committed field to its sink.
func (sp *settingsPage) onCommit(key, value string) (string, tea.Cmd, error) {
	// Cloud provider keys — handled before everything else.
	if ca := classifyCloudCommit(key, value); ca.kind != cloudCommitNone {
		return sp.commitCloud(ca)
	}

	// Color edits — handled before classifyCommit (not a config/permission key).
	if strings.HasPrefix(key, "color:") {
		fieldKey := strings.TrimPrefix(key, "color:")
		if c, err := theme.ParseHex(value); err == nil {
			pc := sp.working.Palette
			if ptr := theme.FieldPtr(&pc, fieldKey); ptr != nil {
				*ptr = c
				sp.working.Palette = pc
				sp.dirty = true
				w := sp.working
				return "edited " + fieldKey, func() tea.Msg { return settingsThemeMsg{working: w} }, nil
			}
		}
		return "bad color", nil, nil
	}

	// Derive the current check set from the live form fields, not the cached
	// config — immune to a stale/nil sp.cfg after a failed re-fetch. The
	// just-committed toggle has already flipped its state, and toggleCheck
	// sets membership explicitly, so double-application is idempotent.
	var currentChecks []string
	if sp.form != nil {
		currentChecks = watchdogChecksFromForm(sp.form)
	} else if sp.cfg != nil {
		currentChecks = sp.cfg.WatchdogChecks
	}
	action := classifyCommit(key, value, currentChecks)
	switch action.kind {
	case commitConfig:
		// Guard: switching to llama_server requires its binary + a model
		// to be ready. If either is missing, open the install modal and
		// DON'T dispatch UpdateConfig — the modal's cancel path leaves
		// the config unchanged (switch rejected), and its install-success
		// path dispatches the UpdateConfig at the moment the runtime is
		// actually usable. Force sp.cfg to nil so the form snapshot that
		// runs immediately after this commit reflects the server's
		// (unchanged) runtime, reverting the toggle back to ollama.
		if action.update.OpenRuntime == "llama_server" {
			gctx, gcancel := context.WithTimeout(context.Background(), 3*time.Second)
			st, gerr := sp.agent.GetOpenRuntimeStatus(gctx, "llama_server")
			gcancel()
			if gerr == nil && st != nil && !st.Ok {
				sp.cfg = nil
				statusCopy := *st
				return "install required — see modal", func() tea.Msg {
					return openOpenRuntimeInstallModalMsg{status: statusCopy, pending: "llama_server"}
				}, nil
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		status, err := sp.agent.UpdateConfig(ctx, action.update)
		if err == nil {
			sp.cfg = nil
		}
		return status, nil, err
	case commitPermission:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sp.agent.SetPermissionMode(ctx, action.value); err != nil {
			return "", nil, err
		}
		sp.cfg = nil
		mode := action.value
		return "permission mode: " + mode, func() tea.Msg {
			return permissionModeChangedMsg{mode: mode}
		}, nil
	case commitColor:
		sp.accentToken = action.value
		token := action.value
		return "accent color set", func() tea.Msg {
			return settingsColorMsg{token: token}
		}, nil
	}

	// Theme-specific keys (fall through classifyCommit as commitNoop).
	switch key {
	case "theme-select":
		if t, ok := sp.themes.Get(value); ok {
			sp.working = t
			sp.dirty = false
			return "theme: " + value, func() tea.Msg { return settingsThemeMsg{working: t, persistName: value} }, nil
		}
		return "no such theme", nil, nil
	case "theme-save":
		if err := uiconfig.SaveCustomTheme(sp.working); err != nil {
			return "", nil, err
		}
		sp.dirty = false
		w := sp.working
		return "saved " + sp.working.Name, func() tea.Msg { return settingsThemeMsg{working: w} }, nil
	case "theme-save-as":
		name := strings.TrimSpace(value)
		if name == "" {
			return "name required", nil, nil
		}
		nt := theme.Theme{Name: name, Palette: sp.working.Palette}
		if err := sp.themes.Add(nt); err != nil {
			return "", nil, err
		}
		if err := uiconfig.SaveCustomTheme(nt); err != nil {
			return "", nil, err
		}
		sp.working = nt
		sp.dirty = false
		return "saved as " + name, func() tea.Msg { return settingsThemeMsg{working: nt, persistName: name} }, nil
	case "theme-delete":
		if sp.themes.IsBuiltin(sp.working.Name) {
			return "can't delete built-in theme", nil, nil
		}
		name := sp.working.Name
		if err := sp.themes.Remove(name); err != nil {
			return "", nil, err
		}
		_ = uiconfig.DeleteCustomTheme(name)
		cracker, _ := sp.themes.Get("cr4k3r_j4x")
		sp.working = cracker
		sp.dirty = false
		return "deleted " + name, func() tea.Msg { return settingsThemeMsg{working: cracker, persistName: "cr4k3r_j4x"} }, nil
	case "theme-import":
		t, err := uiconfig.ImportTheme(strings.TrimSpace(value))
		if err != nil {
			return "", nil, err
		}
		_ = sp.themes.Add(t)
		sp.working = t
		sp.dirty = false
		return "imported " + t.Name, func() tea.Msg { return settingsThemeMsg{working: t, persistName: t.Name} }, nil
	}

	return "", nil, nil
}

// --- contentPageScroller ---

func (sp *settingsPage) ScrollBy(delta int)  { sp.offset += delta; sp.clampScroll() }
func (sp *settingsPage) ScrollTo(offset int) { sp.offset = offset; sp.clampScroll() }
func (sp *settingsPage) ScrollState() contentPageScrollState {
	total := len(sp.form.Lines(sp.width, sp.palette, sp.styles))
	return contentPageScrollState{Total: total, Height: sp.viewportHeight(), Offset: sp.offset}
}

func (sp *settingsPage) clampScroll() {
	total := len(sp.form.Lines(sp.width, sp.palette, sp.styles))
	max := total - sp.viewportHeight()
	if max < 0 {
		max = 0
	}
	if sp.offset > max {
		sp.offset = max
	}
	if sp.offset < 0 {
		sp.offset = 0
	}
}

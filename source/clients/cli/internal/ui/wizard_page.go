package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/overlay"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/clients/cli/internal/wizard"
	"cercano/source/server/pkg/agentclient"
	"cercano/source/server/pkg/config"
)

const contentPageWizard contentPageID = "wizard"

// wizardRow is one selectable choice on the current wizard screen.
type wizardRow struct {
	Key        string
	Label      string
	Annotation string
	Disabled   bool // shown but not selectable (coming soon)
}

// wizardPage drives the setup wizard (docs/features/setup-wizard/README.md)
// as a content page. The wizard package owns step sequencing, validation,
// and resume persistence; this type owns rendering and key handling only.
type wizardPage struct {
	width, height int
	palette       theme.Palette
	styles        theme.Styles
	agent         *agentclient.Client
	state         wizard.State
	cursor        int
	authPick      bool // cloud step, phase 2: provider chosen, picking auth
	// picker, when non-nil, is the floating per-slot model picker on the
	// tiers step; keys route to it until it closes.
	picker *overlay.RowList
	recs   config.TierRecommendations
	recsOK bool
	status string
	// applyFn commits the collected answers on finish; defaults to
	// applyConfig, overridable in tests (agentclient.Client is a concrete
	// gRPC type with nothing to fake).
	applyFn func() error
}

// wizardTierPurpose is the per-tier "what is this for" line shown on the
// tiers step; wording tracks the definitions in pkg/config/models.go.
var wizardTierPurpose = map[config.Tier]string{
	config.TierMostCapable:   "frontier reasoning for the hardest tasks",
	config.TierEveryday:      "default workhorse for main chat",
	config.TierFastLight:     "small, low-latency background helpers",
	config.TierFastLightText: "fast prose judgment — summaries, recaps, watchdog",
}

var wizardTierOrder = []config.Tier{
	config.TierMostCapable, config.TierEveryday, config.TierFastLight, config.TierFastLightText,
}

// newWizardPage resumes a persisted run when one exists, else starts fresh.
func newWizardPage(ag *agentclient.Client, p theme.Palette, s theme.Styles, w, h int) *wizardPage {
	st, ok := wizard.Load()
	if !ok {
		st = wizard.New()
	}
	wp := &wizardPage{palette: p, styles: s, agent: ag, state: st, width: w, height: h}
	if recs, err := config.LoadTierRecommendations(); err == nil {
		wp.recs, wp.recsOK = recs, true
	}
	if st.Step == wizard.StepTiers {
		wp.autofillTiers()
	}
	wp.applyFn = wp.applyConfig
	wp.cursor = wp.defaultCursor()
	return wp
}

// applyConfig pushes the collected answers to the agent: locus mode and
// default provider in one sparse patch, then one patch per tier pick (the
// UpdateConfig taxonomy patch takes a single key per call). Synchronous by
// design — a handful of local RPCs on a once-per-setup action.
func (wp *wizardPage) applyConfig() error {
	if wp.agent == nil {
		return fmt.Errorf("no agent connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := wp.agent.UpdateConfig(ctx, agentclient.ConfigUpdate{
		LocusMode:      wp.state.LocusMode,
		ModelTierKey:   "default_provider",
		ModelTierValue: wp.state.PrimarySide,
	}); err != nil {
		return err
	}
	keys := make([]string, 0, len(wp.state.TierPicks))
	for k := range wp.state.TierPicks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err := wp.agent.UpdateConfig(ctx, agentclient.ConfigUpdate{
			ModelTierKey:   k,
			ModelTierValue: wp.state.TierPicks[k],
		}); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	return nil
}

func (wp *wizardPage) ID() contentPageID { return contentPageWizard }

func (wp *wizardPage) SetSize(w, h int) { wp.width, wp.height = w, h }

// rows returns the current screen's choices.
func (wp *wizardPage) rows() []wizardRow {
	switch wp.state.Step {
	case wizard.StepPrimary:
		return []wizardRow{
			{Key: wizard.SideOpen, Label: "open", Annotation: "open-weight model on this machine (local-first default)"},
			{Key: wizard.SideCloud, Label: "cloud", Annotation: "hosted API provider"},
		}
	case wizard.StepCloud:
		if wp.authPick {
			return wizardAuthRows(wp.state.CloudProvider)
		}
		presets := cloudPresets()
		rows := make([]wizardRow, 0, len(presets)+1)
		for _, pr := range presets {
			ann := ""
			switch pr.Tier {
			case tierUntested:
				ann = "(untested)"
			case tierComingSoon:
				ann = "(coming soon)"
			}
			rows = append(rows, wizardRow{Key: pr.ID, Label: pr.Label, Annotation: ann, Disabled: pr.Tier == tierComingSoon})
		}
		// In the design's auth matrix but its device flow isn't built yet.
		rows = append(rows, wizardRow{Key: "github-copilot", Label: "github copilot", Annotation: "(coming soon)", Disabled: true})
		return rows
	case wizard.StepLocus:
		rec := wp.state.RecommendedLocus()
		rows := []wizardRow{
			{Key: "cloud_only", Label: "cloud only", Annotation: "everything runs on the hosted API"},
			{Key: "cloud_primary", Label: "cloud primary", Annotation: "main model in the cloud; open co-processor for background work"},
			{Key: "open_primary", Label: "open primary", Annotation: "main model on this machine; cloud fallback"},
			{Key: "open_only", Label: "open only", Annotation: "work never leaves this machine"},
		}
		for i := range rows {
			if rows[i].Key == rec {
				rows[i].Annotation += "  (recommended)"
			}
		}
		return rows
	case wizard.StepTiers:
		rows := make([]wizardRow, 0, 2*len(wizardTierOrder)+1)
		for _, t := range wizardTierOrder {
			for _, side := range wp.tierSides() {
				key := string(t) + "." + side
				pick := wp.state.TierPicks[key]
				if pick == "" {
					pick = "—"
				}
				rows = append(rows, wizardRow{
					Key:        key,
					Label:      strings.ReplaceAll(string(t), "_", "-") + " · " + side,
					Annotation: pick,
				})
			}
		}
		rows = append(rows, wizardRow{Key: "continue", Label: "continue", Annotation: "accept these models"})
		return rows
	case wizard.StepDone:
		return []wizardRow{{Key: "finish", Label: "finish", Annotation: "apply these settings and close"}}
	}
	return nil
}

// tierSides lists which taxonomy sides the wizard fills: the open side
// always (every locus mode but cloud_only touches it), the cloud side only
// when a cloud provider was configured.
func (wp *wizardPage) tierSides() []string {
	if wp.state.CloudProvider != "" {
		return []string{wizard.SideCloud, wizard.SideOpen}
	}
	return []string{wizard.SideOpen}
}

// wizardAuthRows is the design doc's auth matrix for one provider.
func wizardAuthRows(providerID string) []wizardRow {
	switch providerID {
	case "anthropic":
		return []wizardRow{
			{Key: "meridian", Label: "meridian proxy", Annotation: "Claude subscription sign-in"},
			{Key: "api_key", Label: "api key", Annotation: "key from console.anthropic.com"},
		}
	case "openai":
		return []wizardRow{
			{Key: "chatgpt", Label: "chatgpt plus/pro sign-in", Annotation: "unofficial — may stop working; restricted model list"},
			{Key: "api_key", Label: "api key", Annotation: "key from platform.openai.com"},
		}
	default:
		return []wizardRow{
			{Key: "api_key", Label: "api key", Annotation: "paste a key from the provider's console"},
		}
	}
}

// autofillTiers fills empty tier slots from the shipped recommendations,
// preserving anything the user already picked (resume keeps edits).
func (wp *wizardPage) autofillTiers() {
	if !wp.recsOK {
		return
	}
	if wp.state.TierPicks == nil {
		wp.state.TierPicks = map[string]string{}
	}
	for _, t := range wizardTierOrder {
		if wp.state.CloudProvider != "" {
			key := string(t) + "." + wizard.SideCloud
			if wp.state.TierPicks[key] == "" {
				if m, ok := config.PickFirst(wp.recs.Candidates(config.ProviderCloud, wp.state.CloudProvider, t), nil); ok {
					wp.state.TierPicks[key] = m
				}
			}
		}
		key := string(t) + "." + wizard.SideOpen
		if wp.state.TierPicks[key] == "" {
			if m, ok := config.PickFirst(wp.recs.Candidates(config.ProviderOpen, "", t), nil); ok {
				wp.state.TierPicks[key] = m
			}
		}
	}
}

// defaultCursor pre-positions the cursor: the locus step starts on the
// recommended mode, everything else on the first selectable row.
func (wp *wizardPage) defaultCursor() int {
	rows := wp.rows()
	if wp.state.Step == wizard.StepLocus {
		rec := wp.state.RecommendedLocus()
		for i, r := range rows {
			if r.Key == rec {
				return i
			}
		}
	}
	for i, r := range rows {
		if !r.Disabled {
			return i
		}
	}
	return 0
}

// move advances the cursor by delta, skipping disabled rows.
func (wp *wizardPage) move(delta int) {
	rows := wp.rows()
	if len(rows) == 0 {
		return
	}
	i := wp.cursor
	for {
		i += delta
		if i < 0 || i >= len(rows) {
			return // stop at the edges, no wrap
		}
		if !rows[i].Disabled {
			wp.cursor = i
			return
		}
	}
}

func (wp *wizardPage) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if wp.picker != nil {
		next, cmd, closed := wp.picker.Update(msg, wp.styles)
		if closed {
			wp.picker = nil
			return cmd, false
		}
		wp.picker = &next
		return cmd, false
	}
	switch msg.String() {
	case "up", "k":
		wp.move(-1)
	case "down", "j":
		wp.move(1)
	case "esc":
		return nil, wp.back()
	case "enter":
		return nil, wp.selectRow()
	}
	return nil, false
}

// back steps to the previous screen; on the first screen it closes the
// page (state stays persisted for resume).
func (wp *wizardPage) back() (closed bool) {
	wp.status = ""
	if wp.state.Step == wizard.StepCloud && wp.authPick {
		wp.authPick = false
		wp.cursor = wp.defaultCursor()
		return false
	}
	if wp.state.Step == wizard.StepPrimary {
		wp.persist()
		return true
	}
	wp.state.Step = wp.state.Prev()
	wp.cursor = wp.defaultCursor()
	wp.persist()
	return false
}

// selectRow applies the highlighted choice for the current screen.
func (wp *wizardPage) selectRow() (closed bool) {
	rows := wp.rows()
	if wp.cursor >= len(rows) {
		return false
	}
	row := rows[wp.cursor]
	if row.Disabled {
		wp.status = row.Label + " isn't available yet"
		return false
	}
	wp.status = ""

	switch wp.state.Step {
	case wizard.StepPrimary:
		wp.state.PrimarySide = row.Key
		wp.advance()
	case wizard.StepCloud:
		if !wp.authPick {
			wp.state.CloudProvider = row.Key
			wp.authPick = true
			wp.cursor = 0
			wp.persist()
			return false
		}
		wp.state.AuthMethod = row.Key
		wp.authPick = false
		// Credential collection (key entry, sign-in flows) lands with the
		// auth slices; the choice itself is recorded now.
		wp.advance()
	case wizard.StepLocus:
		wp.state.LocusMode = row.Key
		wp.advance()
	case wizard.StepTiers:
		if row.Key == "continue" {
			wp.advance()
			return false
		}
		wp.openTierPicker(row.Key)
	case wizard.StepDone:
		if err := wp.applyFn(); err != nil {
			// State stays persisted: enter retries, esc walks back.
			wp.status = "apply failed: " + err.Error()
			return false
		}
		if err := wizard.Clear(); err != nil {
			wp.status = "applied, but could not clear wizard state: " + err.Error()
			return false
		}
		return true
	}
	return false
}

// advance moves the state machine forward, autofills on entry to the tiers
// step, persists, and repositions the cursor.
func (wp *wizardPage) advance() {
	if err := wp.state.Advance(); err != nil {
		wp.status = err.Error()
		return
	}
	if wp.state.Step == wizard.StepTiers {
		wp.autofillTiers()
	}
	wp.persist()
	wp.cursor = wp.defaultCursor()
}

func (wp *wizardPage) persist() {
	if err := wizard.Save(wp.state); err != nil {
		wp.status = "could not save wizard state: " + err.Error()
	}
}

// stepIndex returns the 1-based position and total screen count for the
// header; the open path has one screen fewer.
func (wp *wizardPage) stepIndex() (int, int) {
	order := []wizard.Step{wizard.StepPrimary, wizard.StepCloud, wizard.StepLocus, wizard.StepTiers}
	if wp.state.PrimarySide == wizard.SideOpen {
		order = []wizard.Step{wizard.StepPrimary, wizard.StepLocus, wizard.StepTiers}
	}
	for i, s := range order {
		if s == wp.state.Step {
			return i + 1, len(order)
		}
	}
	return len(order), len(order)
}

func (wp *wizardPage) stepTitle() string {
	switch wp.state.Step {
	case wizard.StepPrimary:
		return "primary model"
	case wizard.StepCloud:
		if wp.authPick {
			return "sign in to " + wp.state.CloudProvider
		}
		return "cloud provider"
	case wizard.StepLocus:
		return "cloud / open split"
	case wizard.StepTiers:
		return "model tiers"
	case wizard.StepDone:
		return "done"
	}
	return ""
}

func (wp *wizardPage) stepDesc() string {
	switch wp.state.Step {
	case wizard.StepPrimary:
		return "Where should your main model run?"
	case wizard.StepCloud:
		if wp.authPick {
			return "How do you want to authenticate?"
		}
		return "Pick your cloud provider."
	case wizard.StepLocus:
		return "How should Cercano split work between cloud and open models?"
	case wizard.StepTiers:
		var b strings.Builder
		b.WriteString("Cercano routes different work to different model tiers:\n")
		for _, t := range wizardTierOrder {
			fmt.Fprintf(&b, "  %-16s %s\n", strings.ReplaceAll(string(t), "_", "-"), wizardTierPurpose[t])
		}
		b.WriteString("These picks are easy to change later — /m or the config file.")
		return b.String()
	case wizard.StepDone:
		return wp.summary()
	}
	return ""
}

// summary renders the collected answers on the final screen.
func (wp *wizardPage) summary() string {
	var b strings.Builder
	b.WriteString("Setup complete:\n")
	fmt.Fprintf(&b, "  primary:  %s\n", wp.state.PrimarySide)
	if wp.state.CloudProvider != "" {
		fmt.Fprintf(&b, "  cloud:    %s (%s) — credentials not collected yet; use /cloud key for now\n",
			wp.state.CloudProvider, wp.state.AuthMethod)
	}
	fmt.Fprintf(&b, "  locus:    %s\n", wp.state.LocusMode)
	for _, t := range wizardTierOrder {
		for _, side := range wp.tierSides() {
			key := string(t) + "." + side
			if pick := wp.state.TierPicks[key]; pick != "" {
				fmt.Fprintf(&b, "  %-24s %s\n", key+":", pick)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// openTierPicker installs the floating model picker for one slot. Unlike
// the dashboard's picker, selecting only records the pick in wizard state —
// nothing is applied until finish.
func (wp *wizardPage) openTierPicker(slotKey string) {
	if wp.state.TierPicks == nil {
		wp.state.TierPicks = map[string]string{}
	}
	hooks := overlay.Hooks{
		OnSelect: func(row overlay.Row) (string, bool, tea.Cmd) {
			if row.Key == "-" {
				delete(wp.state.TierPicks, slotKey)
			} else {
				wp.state.TierPicks[slotKey] = row.Key
			}
			wp.persist()
			return "", true, nil
		},
	}
	picker := overlay.New("model — "+slotKey, wp.tierPickerCandidates(slotKey), hooks)
	wp.picker = &picker
}

// tierPickerCandidates lists a slot's options: the shipped recommendations
// first, then live entries (installed runtime models for .open slots, the
// active profile's catalog for .cloud slots — best-effort, the wizard often
// runs before any credentials exist), then the clear row.
func (wp *wizardPage) tierPickerCandidates(slotKey string) []overlay.Row {
	tierName, _, _ := strings.Cut(slotKey, ".")
	current := wp.state.TierPicks[slotKey]
	side := config.ProviderOpen
	if isCloudTierKey(slotKey) {
		side = config.ProviderCloud
	}
	var rows []overlay.Row
	seen := map[string]bool{}
	for _, m := range wp.recs.Candidates(side, wp.state.CloudProvider, config.Tier(tierName)) {
		hint := currentHint(m, current)
		if hint == "" {
			hint = "recommended"
		}
		rows = append(rows, overlay.Row{Key: m, Label: m, Hint: hint})
		seen[m] = true
	}
	if wp.agent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if side == config.ProviderOpen {
			if status, err := wp.agent.GetRuntimeStatus(ctx); err == nil {
				for _, m := range downloadedRuntimeModels(runtimeStatusModels(status)) {
					if seen[m.ID] {
						continue
					}
					rows = append(rows, overlay.Row{Key: m.ID, Label: firstNonEmpty(m.DisplayName, m.ID), Value: m.Runtime, Hint: currentHint(m.ID, current)})
					seen[m.ID] = true
				}
			}
		} else if _, active, err := wp.agent.GetCloudProfiles(ctx); err == nil && active != "" {
			if models, _, err := wp.agent.ListCloudProfileModels(ctx, active); err == nil {
				for _, m := range models {
					if seen[m.ID] {
						continue
					}
					rows = append(rows, overlay.Row{Key: m.ID, Label: firstNonEmpty(m.DisplayName, m.ID), Hint: currentHint(m.ID, current)})
					seen[m.ID] = true
				}
			}
		}
	}
	rows = append(rows, overlay.Row{Key: "-", Label: "(clear)", Value: "unset this slot"})
	return rows
}

func (wp *wizardPage) View() string {
	if wp.picker != nil {
		return wp.picker.View(wp.width, wp.palette, wp.styles)
	}
	var b strings.Builder
	idx, total := wp.stepIndex()
	header := fmt.Sprintf("Setup — %s (step %d of %d)", wp.stepTitle(), idx, total)
	b.WriteString(wp.styles.Bright.Render(header))
	b.WriteString("\n\n")
	if d := wp.stepDesc(); d != "" {
		b.WriteString(wp.styles.Primary.Render(d))
		b.WriteString("\n\n")
	}
	for i, r := range wp.rows() {
		caret := "  "
		label := r.Label
		switch {
		case r.Disabled:
			label = wp.styles.Muted.Render(label)
		case i == wp.cursor:
			caret = wp.styles.Accent.Render("▶ ")
			label = wp.styles.Bright.Render(label)
		default:
			label = wp.styles.Primary.Render(label)
		}
		b.WriteString(caret + padRight(r.Label, label, 28))
		if r.Annotation != "" {
			b.WriteString(wp.styles.Dim.Render(r.Annotation))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(wp.styles.Dim.Render("↑/↓ move · enter select · esc back"))
	if wp.status != "" {
		b.WriteString("\n" + wp.styles.Warn.Render(wp.status))
	}
	return b.String()
}

// padRight pads styled text to width using the unstyled length (ANSI codes
// would break a plain %-*s).
func padRight(plain, styled string, width int) string {
	if n := width - len([]rune(plain)); n > 0 {
		return styled + strings.Repeat(" ", n)
	}
	return styled + " "
}

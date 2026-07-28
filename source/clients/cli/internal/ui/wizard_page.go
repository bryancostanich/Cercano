package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
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
	// providers is the cloud-provider catalog fetched from the agent
	// (GetCloudProviders) once at construction. The agent owns the catalog;
	// the cloud step renders and looks up providers against this cached copy.
	// Empty when there's no agent (tests seed it directly).
	providers []agentclient.CloudProvider
	state     wizard.State
	cursor    int
	authPick  bool // cloud step, phase 2: provider chosen, picking auth
	// picker, when non-nil, is the floating per-slot model picker on the
	// tiers step; keys route to it until it closes.
	picker *overlay.RowList
	// keyEntry is the cloud step's masked API-key prompt. The key commits
	// to the agent immediately (profile + keychain + activate) — it never
	// touches the plaintext resume file.
	keyEntry bool
	keyInput textinput.Model
	// commitKeyFn mirrors applyFn: test indirection over the concrete gRPC
	// client.
	commitKeyFn func(key string) error
	recs        config.TierRecommendations
	recsOK      bool
	// catalog is the runtime model catalog fetched from the agent once at
	// construction (ListRuntimeModels). The open tier picks are autofilled and
	// displayed from its RAM-tiered RecommendedOpenModels, so every open
	// recommendation is a gate-verified curated model rather than an arbitrary
	// (possibly incompatible) id from the shipped recs. Empty with no agent
	// (tests seed it directly).
	catalog   agentclient.RuntimeModelCatalog
	catalogOK bool
	status    string
	// applyFn commits the collected answers on finish; defaults to
	// applyConfig, overridable in tests (agentclient.Client is a concrete
	// gRPC type with nothing to fake).
	applyFn func() error
	// downloadFn enrolls a runtime model download on finish; defaults to a
	// thin wrapper over the agent's DownloadRuntimeModel, overridable in tests
	// (the concrete gRPC client can't be faked).
	downloadFn func(ctx context.Context, runtime, modelID string) error
	// abandonArmed is the q-pressed-once confirm state: the next q abandons
	// the run (rollback + clear); any other key disarms.
	abandonArmed bool
	// rollbackFn undoes eager commits on abandon; defaults to
	// rollbackBaseline, overridable in tests.
	rollbackFn func() error
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
	wp.loadRuntimeCatalog()
	if st.Step == wizard.StepOpen {
		wp.autofillTiers()
	}
	wp.applyFn = wp.applyConfig
	wp.downloadFn = func(ctx context.Context, runtime, modelID string) error {
		_, err := wp.agent.DownloadRuntimeModel(ctx, runtime, modelID, "")
		return err
	}
	wp.commitKeyFn = wp.commitAPIKey
	wp.rollbackFn = wp.rollbackBaseline
	wp.loadProviders()
	if !ok {
		// Fresh run: snapshot the cloud config before any eager commits can
		// touch it. Resumed runs keep the baseline captured when they started.
		wp.captureBaseline()
	}
	wp.cursor = wp.defaultCursor()
	return wp
}

// captureBaseline records the pre-wizard cloud-profile configuration in the
// run's state so abandoning can restore it. Best-effort: with no agent (or a
// failed read) abandon still clears the run, it just can't undo commits.
func (wp *wizardPage) captureBaseline() {
	if wp.agent == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	profiles, active, err := wp.agent.GetCloudProfiles(ctx)
	if err != nil {
		return
	}
	b := &wizard.Baseline{ActiveProfile: active}
	for _, p := range profiles {
		b.Profiles = append(b.Profiles, wizard.ProfileSnapshot{
			Name: p.Name, Flavor: p.Flavor, Backend: p.Backend,
			BaseURL: p.BaseURL, Model: p.Model, Route: p.Route,
		})
	}
	wp.state.Baseline = b
}

// rollbackBaseline restores the cloud configuration captured at wizard start:
// profiles the run created are removed (deleting their keychain keys with
// them), profiles it modified are restored, and the previously active profile
// is re-activated. A key overwritten on a pre-existing profile cannot be
// restored — keys are write-only by design.
func (wp *wizardPage) rollbackBaseline() error {
	base := wp.state.Baseline
	if base == nil || wp.agent == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cur, active, err := wp.agent.GetCloudProfiles(ctx)
	if err != nil {
		return err
	}
	inBase := map[string]bool{}
	for _, b := range base.Profiles {
		inBase[b.Name] = true
	}
	// Removing a currently-active profile clears the active slot agent-side,
	// so a wizard-created active profile leaves cloud absent here and the
	// re-activation below restores the original.
	for _, p := range cur {
		if !inBase[p.Name] {
			if err := wp.agent.RemoveCloudProfile(ctx, p.Name); err != nil {
				return fmt.Errorf("remove %s: %w", p.Name, err)
			}
		}
	}
	curByName := map[string]agentclient.CloudProfileInfo{}
	for _, p := range cur {
		curByName[p.Name] = p
	}
	for _, b := range base.Profiles {
		c, ok := curByName[b.Name]
		if ok && c.Flavor == b.Flavor && c.Backend == b.Backend &&
			c.BaseURL == b.BaseURL && c.Model == b.Model && c.Route == b.Route {
			continue // untouched by the wizard
		}
		if err := wp.agent.UpsertCloudProfile(ctx, agentclient.CloudProfileInfo{
			Name: b.Name, Flavor: b.Flavor, Backend: b.Backend,
			BaseURL: b.BaseURL, Model: b.Model, Route: b.Route,
		}); err != nil {
			return fmt.Errorf("restore %s: %w", b.Name, err)
		}
	}
	if base.ActiveProfile != "" && base.ActiveProfile != active {
		if err := wp.agent.SetActiveCloudProfile(ctx, base.ActiveProfile); err != nil {
			return fmt.Errorf("re-activate %s: %w", base.ActiveProfile, err)
		}
	}
	return nil
}

// loadProviders caches the cloud-provider catalog from the agent for the
// cloud step's provider list and profile lookups. Best-effort and nil-guarded,
// like captureBaseline: with no agent (tests) or a failed read the catalog
// stays empty and tests seed wp.providers directly.
func (wp *wizardPage) loadProviders() {
	if wp.agent == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	view, err := wp.agent.GetCloudProviders(ctx)
	if err != nil {
		return
	}
	wp.providers = view.Providers
}

// wizardPreset finds a catalog provider by id in the agent-fetched catalog and
// returns it as a cloudPreset row carrier. ok is false when the id is unknown
// (or the catalog failed to load).
func (wp *wizardPage) wizardPreset(id string) (cloudPreset, bool) {
	for _, p := range wp.providers {
		if p.ID == id {
			return presetFromProvider(p), true
		}
	}
	return cloudPreset{}, false
}

// wizardProfileModel picks the model to seed a wizard-created profile with:
// the provider's everyday-tier recommendation ("the default workhorse for
// main chat" — exactly what profile.Model serves at request time). Empty when
// the provider has no recommendations; the finish step's everyday-cloud tier
// pick still lands on the profile via applyConfig.
func wizardProfileModel(recs config.TierRecommendations, provider string) string {
	m, _ := config.PickFirst(recs.Candidates(config.ProviderCloud, provider, config.TierEveryday), nil)
	return m
}

// commitAPIKey creates the provider's profile from its preset, stores the
// key, and activates the profile — all immediately, so credentials live
// where they always do (agent-side) and never in wizard state.
func (wp *wizardPage) commitAPIKey(key string) error {
	if wp.agent == nil {
		return fmt.Errorf("no agent connection")
	}
	preset, ok := wp.wizardPreset(wp.state.CloudProvider)
	if !ok {
		return fmt.Errorf("unknown provider %q", wp.state.CloudProvider)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wp.agent.UpsertCloudProfile(ctx, agentclient.CloudProfileInfo{
		Name: preset.ID, Flavor: preset.Flavor, Backend: preset.Backend, BaseURL: preset.BaseURL,
		Model: wizardProfileModel(wp.recs, preset.ID),
	}); err != nil {
		return err
	}
	if err := wp.agent.SetCloudProfileKey(ctx, preset.ID, key); err != nil {
		return err
	}
	return wp.agent.SetActiveCloudProfile(ctx, preset.ID)
}

// startKeyEntry opens the masked key prompt. Returns the cursor-blink cmd.
func (wp *wizardPage) startKeyEntry() tea.Cmd {
	ti := textinput.New()
	ti.Prompt = wp.styles.Accent.Render("▸ ")
	ti.EchoMode = textinput.EchoPassword
	cmd := ti.Focus()
	wp.keyInput = ti
	wp.keyEntry = true
	wp.status = ""
	return cmd
}

// wizardFinishUpdate builds the finish step's first config patch: locus mode,
// and — on the cloud path — the everyday-cloud tier pick as CloudModel, which
// UpdateConfig writes into the active profile and rebuilds. The profile model
// is what actually serves main-chat requests, so the "everyday workhorse"
// answer must land there, not only in the tier taxonomy.
func wizardOpenTierKey(t config.Tier) string { return "llama_server." + string(t) }

func wizardFinishUpdate(st wizard.State) agentclient.ConfigUpdate {
	u := agentclient.ConfigUpdate{
		LocusMode: st.LocusMode,
	}
	if st.CloudProvider != "" {
		u.CloudModel = st.TierPicks["everyday."+wizard.SideCloud]
	}
	if wizard.ModeUsesOpen(st.LocusMode) {
		// The wizard fills the open tiers from the llama-server catalog and
		// enrolls llama-server downloads, so the open runtime must be
		// llama-server. Without this it stays at the config default (ollama)
		// and the curated GGUFs never load — open work degrades to a dead
		// Ollama endpoint.
		u.OpenRuntime = "llama_server"
	}
	return u
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
	if _, err := wp.agent.UpdateConfig(ctx, wizardFinishUpdate(wp.state)); err != nil {
		return err
	}
	keys := make([]string, 0, len(wp.state.TierPicks))
	for k := range wp.state.TierPicks {
		// Cloud picks are internal to the wizard and applied through CloudModel
		// in wizardFinishUpdate, not as model-tier sparse patches.
		if strings.HasSuffix(k, "."+wizard.SideCloud) {
			continue
		}
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
	wp.enrollOpenDownloads(ctx)
	return nil
}

func (wp *wizardPage) ID() contentPageID { return contentPageWizard }

func (wp *wizardPage) SetSize(w, h int) { wp.width, wp.height = w, h }

// wizardRecommendedLocus is the mode the locus step pre-selects and leads
// with. cloud_primary gives the highest-quality frontier experience; it and
// open_only are the two recommended options (see the locus rows).
const wizardRecommendedLocus = "cloud_primary"

// rows returns the current screen's choices.
func (wp *wizardPage) rows() []wizardRow {
	switch wp.state.Step {
	case wizard.StepLocus:
		rows := []wizardRow{
			{Key: "cloud_primary", Label: "cloud primary", Annotation: "highest-quality frontier model in the cloud; open co-processor for background work."},
			{Key: "open_only", Label: "open only", Annotation: "fast and fully private — work never leaves this machine."},
			{Key: "open_primary", Label: "open primary", Annotation: "main model on this machine, cloud fallback — a cost saver."},
			{Key: "cloud_only", Label: "cloud only", Annotation: "everything on the hosted API — skips Cercano's local co-processor."},
		}
		for i := range rows {
			switch rows[i].Key {
			case "cloud_primary", "open_only":
				rows[i].Annotation += "  (recommended)"
			}
		}
		return rows
	case wizard.StepCloud:
		if wp.authPick {
			return wizardAuthRows(wp.state.CloudProvider)
		}
		rows := make([]wizardRow, 0, len(wp.providers))
		for _, pr := range wp.providers {
			ann := ""
			disabled := false
			switch pr.Tier {
			case "coming_soon":
				ann = "(coming soon)"
				disabled = true
			case "untested":
				ann = "(untested)"
			}
			rows = append(rows, wizardRow{Key: pr.ID, Label: pr.Label, Annotation: ann, Disabled: disabled})
		}
		return rows
	case wizard.StepOpen:
		// The open-model set: one pick per open capability tier, plus the
		// embedding slot. The cloud-side tier slots are filled silently from
		// the active profile's recommendations (autofillTiers) and not shown
		// here — this screen is the open set.
		rows := make([]wizardRow, 0, len(wizardTierOrder)+2)
		for _, t := range wizardTierOrder {
			key := wizardOpenTierKey(t)
			pick := normalizeModelLabel(wp.state.TierPicks[key])
			if pick == "" {
				pick = "—"
			}
			rows = append(rows, wizardRow{
				Key:        key,
				Label:      strings.ReplaceAll(string(t), "_", "-"),
				Annotation: pick,
			})
		}
		embKey := wizardOpenTierKey(config.TierEmbedding)
		embPick := normalizeModelLabel(wp.state.TierPicks[embKey])
		if embPick == "" {
			embPick = "—"
		}
		rows = append(rows, wizardRow{Key: embKey, Label: "embedding", Annotation: embPick})
		rows = append(rows, wizardRow{Key: "continue", Label: "continue", Annotation: "accept these models"})
		return rows
	case wizard.StepDone:
		return []wizardRow{{Key: "finish", Label: "finish", Annotation: "apply these settings and close"}}
	}
	return nil
}

// wizardAuthRows is the design doc's auth matrix for one provider.
func wizardAuthRows(providerID string) []wizardRow {
	switch providerID {
	case "anthropic":
		return []wizardRow{
			{Key: "claude", Label: "sign in with Claude (subscription)", Annotation: "Claude Max/Pro — no API key"},
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

// autofillTiers fills tier slots from the shipped recommendations. It normalizes
// any existing picks that carry stale date-suffixed IDs, and replaces picks that
// are no longer in the current recommendation list (e.g. when the YAML is
// updated between runs) so resumed state stays current.
func (wp *wizardPage) autofillTiers() {
	if !wp.recsOK {
		return
	}
	if wp.state.TierPicks == nil {
		wp.state.TierPicks = map[string]string{}
	}
	// Strip date suffixes from any previously-stored IDs (e.g. claude-haiku-4-5-20251001
	// → claude-haiku-4-5). Safe to do in-place: only affects the suffix form.
	for key, pick := range wp.state.TierPicks {
		if n := normalizeModelLabel(pick); n != pick {
			wp.state.TierPicks[key] = n
		}
	}
	for _, t := range wizardTierOrder {
		if wp.state.CloudProvider != "" {
			key := string(t) + "." + wizard.SideCloud
			candidates := wp.recs.Candidates(config.ProviderCloud, wp.state.CloudProvider, t)
			if shouldRefill(wp.state.TierPicks[key], candidates) {
				if m, ok := config.PickFirst(candidates, nil); ok {
					wp.state.TierPicks[key] = m
				}
			}
		}
		key := wizardOpenTierKey(t)
		if wp.catalogOK {
			// Prefer the RAM-tiered curated recommendation: a gate-verified model
			// that fits this machine. Store its display name (the open-slot
			// convention; taxonomy and the finish-time download resolve it back).
			if id, ok := wp.catalog.RecommendedOpenModels[string(t)]; ok {
				label := openModelDisplay(wp.catalog, id)
				if shouldRefill(wp.state.TierPicks[key], []string{label}) {
					wp.state.TierPicks[key] = label
				}
				continue
			}
		}
		candidates := wp.recs.Candidates(config.ProviderOpen, "", t)
		if shouldRefill(wp.state.TierPicks[key], candidates) {
			if m, ok := config.PickFirst(candidates, nil); ok {
				wp.state.TierPicks[key] = m
			}
		}
	}

	// The embedding slot isn't part of wizardTierOrder (rows and summary render
	// it specially), so it's filled here from the same RAM-tiered curated
	// catalog recommendation used for the capability tiers above. Without this
	// the embedding row shows "—".
	if wp.catalogOK {
		embKey := wizardOpenTierKey(config.TierEmbedding)
		if id, ok := wp.catalog.RecommendedOpenModels[string(config.TierEmbedding)]; ok {
			label := openModelDisplay(wp.catalog, id)
			if shouldRefill(wp.state.TierPicks[embKey], []string{label}) {
				wp.state.TierPicks[embKey] = label
			}
		}
	}
}

// shouldRefill reports whether a tier slot should be (re)filled from
// recommendations: either it is empty, or its current pick is no longer in the
// recommendation list (stale autofill from an older YAML).
func shouldRefill(current string, candidates []string) bool {
	if current == "" {
		return true
	}
	for _, c := range candidates {
		if c == current {
			return false
		}
	}
	return true
}

// defaultCursor pre-positions the cursor: the locus step starts on the
// recommended mode, everything else on the first selectable row.
func (wp *wizardPage) defaultCursor() int {
	rows := wp.rows()
	if wp.state.Step == wizard.StepLocus {
		rec := wizardRecommendedLocus
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
	if wp.keyEntry {
		switch msg.String() {
		case "esc":
			wp.keyEntry = false
			wp.status = ""
			return nil, false // back to the auth-method screen
		case "enter":
			key := strings.TrimSpace(wp.keyInput.Value())
			if key == "" {
				wp.status = "enter a key, or esc to go back"
				return nil, false
			}
			if err := wp.commitKeyFn(key); err != nil {
				wp.status = "key setup failed: " + err.Error()
				return nil, false
			}
			wp.keyEntry = false
			wp.authPick = false
			wp.status = wp.state.CloudProvider + " key stored, profile activated"
			wp.advance()
			return nil, false
		}
		var cmd tea.Cmd
		wp.keyInput, cmd = wp.keyInput.Update(msg)
		return cmd, false
	}
	if wp.abandonArmed && msg.String() != "q" {
		wp.abandonArmed = false
		wp.status = ""
	}
	switch msg.String() {
	case "up", "k":
		wp.move(-1)
	case "down", "j":
		wp.move(1)
	case "esc":
		return nil, wp.back()
	case "enter":
		return wp.selectRow()
	case "q":
		return nil, wp.abandon()
	}
	return nil, false
}

// abandon is the trapdoor: two presses of q close the wizard without keeping
// anything — eager commits are rolled back to the baseline and the resume
// file is cleared. The first press only arms and asks.
func (wp *wizardPage) abandon() (closed bool) {
	if !wp.abandonArmed {
		wp.abandonArmed = true
		wp.status = "abandon setup? changes made so far will be undone — press q again to confirm, any other key to keep going"
		return false
	}
	if err := wp.rollbackFn(); err != nil {
		wp.abandonArmed = false
		wp.status = "could not undo setup changes: " + err.Error()
		return false
	}
	if err := wizard.Clear(); err != nil {
		wp.abandonArmed = false
		wp.status = "could not clear wizard state: " + err.Error()
		return false
	}
	return true
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
	if wp.state.Step == wizard.StepLocus {
		wp.persist()
		return true
	}
	wp.state.Step = wp.state.Prev()
	wp.cursor = wp.defaultCursor()
	wp.persist()
	return false
}

// selectRow applies the highlighted choice for the current screen.
func (wp *wizardPage) selectRow() (tea.Cmd, bool) {
	rows := wp.rows()
	if wp.cursor >= len(rows) {
		return nil, false
	}
	row := rows[wp.cursor]
	if row.Disabled {
		wp.status = row.Label + " isn't available yet"
		return nil, false
	}
	wp.status = ""

	switch wp.state.Step {
	case wizard.StepCloud:
		if !wp.authPick {
			wp.state.CloudProvider = row.Key
			wp.authPick = true
			wp.cursor = 0
			wp.persist()
			return nil, false
		}
		wp.state.AuthMethod = row.Key
		switch row.Key {
		case "api_key":
			// authPick stays true so esc from the prompt returns here.
			return wp.startKeyEntry(), false
		case "claude":
			// Claude subscription sign-in: hand off to the loopback modal, owned
			// by the root model and composited over this wizard page. It creates
			// + activates the "claude" profile on success; the wizard's finish
			// (applyConfig) only writes locus + tier picks. Advance so the wizard
			// continues behind the modal.
			wp.authPick = false
			wp.advance()
			return func() tea.Msg {
				return openClaudeLoginModalMsg{profile: "", setActive: true}
			}, false
		case "chatgpt":
			// ChatGPT subscription sign-in: hand off to the device-code modal,
			// which is owned by the root model and composited over this wizard
			// page. It creates + activates the "chatgpt" profile on success; the
			// wizard's finish (applyConfig) only writes locus + tier picks, so it
			// won't clobber the profile. Advance so the wizard continues behind
			// the modal. Empty model lets the agent apply its default.
			wp.authPick = false
			wp.advance()
			return func() tea.Msg {
				return openChatGPTLoginModalMsg{profile: "", setActive: true}
			}, false
		default:
			wp.authPick = false
			wp.advance()
		}
	case wizard.StepLocus:
		wp.state.LocusMode = row.Key
		wp.advance()
	case wizard.StepOpen:
		if row.Key == "continue" {
			wp.advance()
			return nil, false
		}
		wp.openTierPicker(row.Key)
	case wizard.StepDone:
		if err := wp.applyFn(); err != nil {
			// State stays persisted: enter retries, esc walks back.
			wp.status = "apply failed: " + err.Error()
			return nil, false
		}
		if err := wizard.Clear(); err != nil {
			wp.status = "applied, but could not clear wizard state: " + err.Error()
			return nil, false
		}
		return wp.openRuntimeCheckCmd(), true
	}
	return nil, false
}

// openRuntimeCheckCmd, after a finish on an open-using locus, verifies the open
// runtime is installed and ready. If it isn't, it emits the shared install-modal
// message so the user is walked through installing llama-server (and a model if
// needed) instead of finishing setup with a runtime that can't run the open
// tiers. No-op for cloud-only loci, an already-good runtime, or a nil agent.
func (wp *wizardPage) openRuntimeCheckCmd() tea.Cmd {
	if wp.agent == nil || !wizard.ModeUsesOpen(wp.state.LocusMode) {
		return nil
	}
	agent := wp.agent
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		st, err := agent.GetOpenRuntimeStatus(ctx, "llama_server")
		if err != nil || st == nil || st.Ok {
			return nil
		}
		return openOpenRuntimeInstallModalMsg{status: *st}
	}
}

// advance moves the state machine forward, autofills on entry to the open
// step, persists, and repositions the cursor.
func (wp *wizardPage) advance() {
	if err := wp.state.Advance(); err != nil {
		wp.status = err.Error()
		return
	}
	if wp.state.Step == wizard.StepOpen {
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
	order := []wizard.Step{wizard.StepLocus}
	if wizard.ModeUsesCloud(wp.state.LocusMode) {
		order = append(order, wizard.StepCloud)
	}
	if wizard.ModeUsesOpen(wp.state.LocusMode) {
		order = append(order, wizard.StepOpen)
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
	case wizard.StepLocus:
		return "how to run Cercano"
	case wizard.StepCloud:
		if wp.authPick {
			return "sign in to " + wp.state.CloudProvider
		}
		return "cloud provider"
	case wizard.StepOpen:
		return "open models"
	case wizard.StepDone:
		return "done"
	}
	return ""
}

func (wp *wizardPage) stepDesc() string {
	switch wp.state.Step {
	case wizard.StepLocus:
		return "How do you want to run Cercano?"
	case wizard.StepCloud:
		if wp.authPick {
			return "How do you want to authenticate?"
		}
		return "Pick your cloud provider."
	case wizard.StepOpen:
		var b strings.Builder
		b.WriteString("Recommended open models, one per tier \u2014 verified to run on this machine:\n")
		for _, t := range wizardTierOrder {
			fmt.Fprintf(&b, "  %-16s %s\n", strings.ReplaceAll(string(t), "_", "-"), wizardTierPurpose[t])
		}
		fmt.Fprintf(&b, "  %-16s %s\n", "embedding", "vector embedding for semantic search")
		b.WriteString("Accept them, or select any to change it \u2014 easy to change later. Downloads run in the background \u2014 track them with /m.")
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
	fmt.Fprintf(&b, "  locus:    %s\n", strings.ReplaceAll(wp.state.LocusMode, "_", " "))
	if wp.state.CloudProvider != "" {
		note := ""
		if wp.state.AuthMethod == "chatgpt" {
			note = " \u2014 approve the ChatGPT sign-in window that opens in your browser"
		}
		fmt.Fprintf(&b, "  cloud:    %s (%s)%s\n", wp.state.CloudProvider, wp.state.AuthMethod, note)
	}
	for _, t := range wizardTierOrder {
		if wp.state.CloudProvider != "" {
			key := string(t) + "." + wizard.SideCloud
			if pick := wp.state.TierPicks[key]; pick != "" {
				fmt.Fprintf(&b, "  %-24s %s\n", key+":", pick)
			}
		}
		key := wizardOpenTierKey(t)
		if pick := wp.state.TierPicks[key]; pick != "" {
			fmt.Fprintf(&b, "  %-24s %s\n", key+":", pick)
		}
	}
	if wizard.ModeUsesOpen(wp.state.LocusMode) {
		b.WriteString("\nYour open models download in the background \u2014 watch progress with /m.")
		if wp.state.LocusMode != "open_only" {
			b.WriteString(" Cercano uses your cloud model until they finish.")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// locusMainSide maps a locus mode to the side (wizard.SideCloud/SideOpen)
// that serves main work under it; empty for an unrecognized mode.
func locusMainSide(mode string) string {
	switch mode {
	case "cloud_only", "cloud_primary":
		return wizard.SideCloud
	case "open_primary", "open_only":
		return wizard.SideOpen
	}
	return ""
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

// tierPickerCandidates lists an open slot's options: shipped recommendations
// first, then live installed runtime models, then the clear row. Cloud is not a
// model-tier slot; the wizard configures cloud through profile/provider fields.
func (wp *wizardPage) tierPickerCandidates(slotKey string) []overlay.Row {
	_, tierName, ok := strings.Cut(slotKey, ".")
	if !ok {
		tierName = slotKey
	}
	current := wp.state.TierPicks[slotKey]
	var rows []overlay.Row
	seen := map[string]bool{}
	if wp.catalogOK {
		// Open candidates are the gate-verified curated chat models; the
		// RAM-tiered pick for this tier is flagged recommended.
		recommended := ""
		if id, ok := wp.catalog.RecommendedOpenModels[tierName]; ok {
			recommended = openModelDisplay(wp.catalog, id)
		}
		for _, mdl := range wp.catalog.Models {
			if mdl.Runtime != "llama_server" || mdl.Source != "catalog" || !mdl.SupportsChat {
				continue
			}
			name := firstNonEmpty(mdl.DisplayName, mdl.ID)
			if seen[name] {
				continue
			}
			hint := ""
			if name == recommended {
				hint = "recommended"
			}
			rows = append(rows, overlay.Row{
				Key:      name,
				Label:    normalizeModelLabel(name),
				Value:    "llama_server",
				Selected: name == current || mdl.ID == current,
				Hint:     hint,
			})
			seen[name] = true
		}
	} else {
		for _, m := range wp.recs.Candidates(config.ProviderOpen, "", config.Tier(tierName)) {
			rows = append(rows, overlay.Row{
				Key:      m,
				Label:    normalizeModelLabel(m),
				Selected: m == current,
				Hint:     "recommended",
			})
			seen[m] = true
		}
	}
	if wp.agent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if status, err := wp.agent.GetRuntimeStatus(ctx); err == nil {
			for _, m := range downloadedRuntimeModels(runtimeStatusModels(status)) {
				// Commit the human-readable name, not the hash ID —
				// same rule as tierPickerRows. Legacy picks may hold
				// an ID, so selection checks both.
				name := firstNonEmpty(m.DisplayName, m.ID)
				if seen[m.ID] || seen[name] {
					continue
				}
				rows = append(rows, overlay.Row{
					Key:      name,
					Label:    normalizeModelLabel(name),
					Value:    firstNonEmpty(m.Runtime, "llama-server"),
					Selected: name == current || m.ID == current,
				})
				seen[name] = true
			}
		}

	}
	rows = append(rows, overlay.Row{Key: "-", Label: "clear", Value: "unset this slot", Selected: current == ""})
	return rows
}

// normalizeModelLabel strips trailing -YYYYMMDD version suffixes from model IDs
// for display. The raw ID is preserved as Row.Key so the server receives the
// canonical form (e.g. "claude-haiku-4-5-20251001" → label "claude-haiku-4-5").
func normalizeModelLabel(id string) string {
	if len(id) >= 9 && id[len(id)-9] == '-' {
		digits := true
		for _, c := range id[len(id)-8:] {
			if c < '0' || c > '9' {
				digits = false
				break
			}
		}
		if digits {
			return id[:len(id)-9]
		}
	}
	return id
}

func (wp *wizardPage) View() string {
	if wp.picker != nil {
		return wp.picker.View(wp.width, wp.palette, wp.styles)
	}
	if wp.keyEntry {
		var b strings.Builder
		b.WriteString(wp.styles.Bright.Render("Setup — api key for " + wp.state.CloudProvider))
		b.WriteString("\n\n")
		b.WriteString(wp.styles.Primary.Render("Paste your API key. Input is hidden; the key is stored agent-side, never in wizard state."))
		b.WriteString("\n\n")
		b.WriteString(wp.keyInput.View())
		b.WriteString("\n\n")
		b.WriteString(wp.styles.Muted.Render("enter save · esc back"))
		if wp.status != "" {
			b.WriteString("\n" + wp.styles.Warn.Render(wp.status))
		}
		return b.String()
	}
	var b strings.Builder
	idx, total := wp.stepIndex()
	header := fmt.Sprintf("Setup — %s (step %d of %d)", wp.stepTitle(), idx, total)
	b.WriteString(wp.styles.Bright.Render(header))
	b.WriteString("\n\n")
	if d := wp.stepDesc(); d != "" {
		// Wrap each line to the page width so long prose (the open-step blurb,
		// the finish-screen "cloud covers the gap" note) doesn't run off the
		// right edge and get clipped. Short, pre-aligned lines (the tier rows)
		// fit under the width and pass through untouched, preserving alignment.
		for i, line := range strings.Split(d, "\n") {
			if i > 0 {
				b.WriteString("\n")
			}
			if wp.width <= 0 || len([]rune(line)) <= wp.width {
				b.WriteString(wp.styles.Primary.Render(line))
				continue
			}
			for j, seg := range wrapWords(line, wp.width) {
				if j > 0 {
					b.WriteString("\n")
				}
				b.WriteString(wp.styles.Primary.Render(seg))
			}
		}
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
			for j, ln := range wrapWords(r.Annotation, wp.width-wizardAnnotationCol) {
				if j > 0 {
					b.WriteString("\n" + strings.Repeat(" ", wizardAnnotationCol))
				}
				b.WriteString(wp.styles.Info.Render(ln))
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(wp.styles.Muted.Render("↑/↓ move · enter select · esc back · q abandon"))
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

// wizardAnnotationCol is the column where a row's annotation begins: 2 for
// the caret plus the 28-wide label pad in padRight. Wrapped continuation
// lines indent to it so a long annotation reads as one aligned block.
const wizardAnnotationCol = 30

// wrapWords splits s into lines no wider than width runes, breaking only at
// spaces; a word longer than width stands alone on its line. width < 1
// returns s as a single line, so a caller with an unknown terminal width
// falls back to the old no-wrap behavior rather than shredding the text one
// rune per line.
func wrapWords(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	if width < 1 {
		return []string{strings.Join(words, " ")}
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if len([]rune(cur))+1+len([]rune(w)) <= width {
			cur += " " + w
			continue
		}
		lines = append(lines, cur)
		cur = w
	}
	return append(lines, cur)
}

// loadRuntimeCatalog caches the agent's runtime model catalog once at
// construction. The open tier autofill and picker source their RAM-tiered
// recommendations from it (RecommendedOpenModels) so every open pick is a
// gate-verified curated model. Best-effort: with no agent (tests) or a failed
// read, catalogOK stays false and the open side falls back to the shipped recs.
func (wp *wizardPage) loadRuntimeCatalog() {
	if wp.agent == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if cat, err := wp.agent.ListRuntimeModels(ctx); err == nil {
		wp.catalog, wp.catalogOK = cat, true
	}
}

// enrollOpenDownloads kicks off background downloads for the open tier picks so
// the models the user selected actually arrive — the summary promises this.
// Best-effort: a failure surfaces in status but does not fail finish. Open
// picks are stored as display names; resolve each back to its catalog model id
// (what DownloadRuntimeModel matches on) and skip any already on disk.
func (wp *wizardPage) enrollOpenDownloads(ctx context.Context) {
	if !wp.catalogOK || wp.downloadFn == nil {
		return
	}
	seen := map[string]bool{}
	for _, t := range wizardTierOrder {
		pick := wp.state.TierPicks[wizardOpenTierKey(t)]
		if pick == "" {
			continue
		}
		mdl, ok := openModelByLabel(wp.catalog, pick)
		if !ok || mdl.DownloadState == "downloaded" || seen[mdl.ID] {
			continue
		}
		seen[mdl.ID] = true
		if err := wp.downloadFn(ctx, firstNonEmpty(mdl.Runtime, "llama_server"), mdl.ID); err != nil {
			wp.status = fmt.Sprintf("downloads started; %s failed: %v", firstNonEmpty(mdl.DisplayName, mdl.ID), err)
		}
	}
}

// openModelDisplay returns the human name for a curated open model id, falling
// back to the id when the catalog has no matching record.
func openModelDisplay(cat agentclient.RuntimeModelCatalog, id string) string {
	for _, m := range cat.Models {
		if m.ID == id {
			return firstNonEmpty(m.DisplayName, m.ID)
		}
	}
	return id
}

// openModelByLabel finds a curated open model by the value stored in a tier
// slot, which may be its display name (the open-slot convention) or its id.
func openModelByLabel(cat agentclient.RuntimeModelCatalog, label string) (agentclient.RuntimeModel, bool) {
	for _, m := range cat.Models {
		if m.DisplayName == label || m.ID == label {
			return m, true
		}
	}
	return agentclient.RuntimeModel{}, false
}

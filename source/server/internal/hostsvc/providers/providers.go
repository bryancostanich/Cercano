// Package providers owns the provider/model resolver extracted from the Server
// god-object. It holds the LLM providers (cloud + open), the engine registry,
// the router and coordinator, and the Ollama catalog manager, and exposes
// model-selection + rebuild logic behind the Resolver interface.
//
// Task 3 of the Phase 2 host decomposition. Zero behavior change — methods are
// moved verbatim from internal/server/server.go; only the receiver and field
// prefixes differ.
package providers

import (
	"context"
	"fmt"
	"log"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/chatgptauth"
	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/engine"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/fallback"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/loop"
	"cercano/source/server/internal/ollamacatalog"
	"cercano/source/server/internal/usage"
	cfg "cercano/source/server/pkg/config"
)

// RouterCloudUpdater is the subset of the router interface the providers service
// needs to propagate a runtime cloud-provider swap. Mirrored from internal/server
// so providers does not import the server package.
type RouterCloudUpdater interface {
	SetCloudProvider(p agent.ModelProvider)
	GetModelProviders() map[string]agent.ModelProvider
}

// Resolver is the interface the front door (Server) depends on for
// provider/model resolution.
type Resolver interface {
	// Main returns the provider + model for the active locus mode, plus fallback signal.
	// Was resolveMainProvider on Server.
	Main() (prov llm.Provider, isCloud bool, fellBack bool, err error)

	// MainModel returns the configured model name for the given tier.
	// Was mainModelFor on Server.
	MainModel(isCloud bool) string

	// PrimaryModel returns the model the context meter measures against.
	// Was primaryModel on Server.
	PrimaryModel() string

	// Rebuild re-derives providers from current config (was rebuildCloud).
	Rebuild() error

	// InstallAbsentCloud clears the native cloud provider and installs the absent sentinel.
	InstallAbsentCloud(reason string)

	// Cloud returns the raw (unwrapped) cloud LLM provider.
	Cloud() llm.Provider

	// Open returns the raw (unwrapped) local LLM provider.
	Open() llm.Provider

	// ActiveCloudModel returns the cloud model from the active profile.
	ActiveCloudModel() string

	// LocusMode returns the currently configured Locus Mode.
	LocusMode() string

	// Router returns the cloud-updater interface for GetConfig's cloud_state check.
	Router() RouterCloudUpdater

	// Registry returns the engine registry (for ListModels, UpdateConfig).
	Registry() *engine.EngineRegistry

	// CatalogManager returns the online catalog manager (may be nil).
	CatalogManager() *ollamacatalog.Manager

	// OpenLegacy returns the legacy open model provider (for ProposeContextEdit).
	OpenLegacy() *legacymodels.OpenModelProvider

	// Reconfigure applies the UpdateConfig provider/runtime block. Restarts the
	// Ollama health monitor, updates the open provider's model/engine, and
	// rebuilds the open LLM provider via the factory when the runtime changes.
	Reconfigure(args ReconfigureArgs)

	// SetCloudLLMProvider wires the native-tool-calling cloud provider.
	SetCloudLLMProvider(p llm.Provider)

	// SetOpenLLMProvider wires the native-tool-calling local provider (Ollama).
	SetOpenLLMProvider(p llm.Provider)

	// SetOpenProviderFactory installs the constructor used to rebuild the native
	// open provider when the local runtime selection changes at runtime.
	SetOpenProviderFactory(fn func(cfg.Config) llm.Provider)

	// CloudLLMProvider returns the raw cloud provider (for dispatch engine).
	CloudLLMProvider() llm.Provider

	// OpenLLMProvider returns the raw local provider (for dispatch engine).
	OpenLLMProvider() llm.Provider

	// SetCatalogManager wires the online-catalog manager.
	SetCatalogManager(cm *ollamacatalog.Manager)

	// SetUsageSink installs the token-usage recording sink used by Main().
	SetUsageSink(fn func(usage.Usage))
}

// service is the concrete Resolver implementation.
type service struct {
	cfgSvc              cfgsvc.Service
	cloudLLMProvider    llm.Provider
	openLLMProvider     llm.Provider
	openProviderFactory func(cfg.Config) llm.Provider // rebuilds openLLMProvider on runtime change
	openProvider        *legacymodels.OpenModelProvider
	cloudFactory        agent.CloudFactory
	router              RouterCloudUpdater
	coordinator         *loop.ADKCoordinator
	registry            *engine.EngineRegistry
	healthMonitorCancel context.CancelFunc // cancel function for the active health monitor
	catalogManager      *ollamacatalog.Manager

	// usageSink wraps the main-loop provider for token recording.
	usageSink func(usage.Usage)
}

// New constructs a Resolver with the collaborators it needs.
// openProvider, cloudFactory, coordinator, router, registry may be nil when
// not applicable (e.g. tests, minimal embeddings).
func New(
	cfgSvc cfgsvc.Service,
	openProvider *legacymodels.OpenModelProvider,
	router RouterCloudUpdater,
	coordinator *loop.ADKCoordinator,
	cloudFactory agent.CloudFactory,
	registry *engine.EngineRegistry,
	usageSink func(usage.Usage),
) Resolver {
	return &service{
		cfgSvc:       cfgSvc,
		openProvider: openProvider,
		router:       router,
		coordinator:  coordinator,
		cloudFactory: cloudFactory,
		registry:     registry,
		usageSink:    usageSink,
	}
}

// --- Resolver interface implementation ---

func (p *service) Cloud() llm.Provider                         { return p.cloudLLMProvider }
func (p *service) Open() llm.Provider                          { return p.openLLMProvider }
func (p *service) OpenLegacy() *legacymodels.OpenModelProvider { return p.openProvider }
func (p *service) Router() RouterCloudUpdater                  { return p.router }
func (p *service) Registry() *engine.EngineRegistry            { return p.registry }
func (p *service) CatalogManager() *ollamacatalog.Manager      { return p.catalogManager }
func (p *service) CloudLLMProvider() llm.Provider              { return p.cloudLLMProvider }
func (p *service) OpenLLMProvider() llm.Provider               { return p.openLLMProvider }

func (p *service) SetCloudLLMProvider(prov llm.Provider)       { p.cloudLLMProvider = prov }
func (p *service) SetOpenLLMProvider(prov llm.Provider)        { p.openLLMProvider = prov }
func (p *service) SetCatalogManager(cm *ollamacatalog.Manager) { p.catalogManager = cm }
func (p *service) SetUsageSink(fn func(usage.Usage))           { p.usageSink = fn }
func (p *service) SetOpenProviderFactory(fn func(cfg.Config) llm.Provider) {
	p.openProviderFactory = fn
}

// LocusMode returns the currently configured Locus Mode.
func (p *service) LocusMode() string {
	return p.cfgSvc.Get().LocusMode
}

// ActiveCloudModel returns the cloud model from the active profile — the
// authoritative request-time value. Falls back to the legacy CloudModel
// field only when no active profile exists (e.g. local-only configs). All
// code that asks "what cloud model are we using right now?" should go
// through cfgSvc.ActiveProfile(), not currentConfig.CloudModel directly.
func (p *service) ActiveCloudModel() string {
	if prof, ok := p.cfgSvc.ActiveProfile(); ok {
		return prof.Model
	}
	return p.cfgSvc.Get().CloudModel
}

// Main returns the provider + model for the active locus mode.
// Was resolveMainProvider on Server.
func (p *service) Main() (llm.Provider, bool, bool, error) {
	c := p.cfgSvc.Get()
	mode, _ := locus.ParseMode(c.LocusMode)
	// Register the open tier absent when its GGUF isn't on disk yet (e.g. still
	// downloading after setup) so Select crosses to cloud — the "cloud covers
	// the gap" routing contract. Otherwise the not-yet-present model gets
	// picked and fails at load time instead of falling back.
	open := p.openLLMProvider
	if !dispatch.OpenModelReady(c) {
		open = nil
	}
	sel, err := dispatch.Select(mode, dispatch.RoleMain, dispatch.Providers{
		Cloud: p.cloudLLMProvider,
		Open:  open,
	})
	if err != nil {
		return nil, false, false, err
	}
	// Wrap the selected provider for "main" token-usage recording at hand-off.
	// The stored providers stay raw (the dispatch engine reads them raw and
	// wraps per-dispatch with its own source), so there's no double-counting.
	prov := usage.Wrap(sel.Provider, "main", sel.IsCloud, p.usageSink)
	return prov, sel.IsCloud, sel.FellBack, nil
}

// MainModel returns the configured model name for the active tier. Cloud
// reads from the active profile (the single source of truth — see
// ActiveCloudModel) so a profile-model change propagates without restart.
// Was mainModelFor on Server.
func (p *service) MainModel(isCloud bool) string {
	if isCloud {
		// Main chat is the everyday capability tier; on the cloud side that
		// resolves through the active profile's vendor cost table (everyday ->
		// standard), falling back to the profile's own Model when no table is
		// configured. This keeps main-chat model selection on the same
		// vendor-keyed path as dispatch — never a raw provider-blind tier slot.
		if prof, ok := p.cfgSvc.ActiveProfile(); ok {
			return p.cfgSvc.Get().ModelProfiles.ResolveCloudModelForTier(prof, cfg.TierEveryday)
		}
		return p.ActiveCloudModel()
	}
	c := p.cfgSvc.Get()
	return (&c).OpenChatModel()
}

// PrimaryModel returns the model the context meter measures against: the
// locus route's primary serving model. cloud_only/cloud_primary → the active
// cloud model (falling back to the open model when no cloud is configured);
// open_primary/open_only → the open model.
// Was primaryModel on Server.
func (p *service) PrimaryModel() string {
	cfgSnap := p.cfgSvc.Get()
	switch cfgSnap.LocusMode {
	case "cloud_only", "cloud_primary":
		// Measure against the model main chat actually uses: the everyday tier
		// resolved through the active profile's vendor cost table, so the meter
		// tracks the served model rather than the profile's bare default.
		if prof, ok := p.cfgSvc.ActiveProfile(); ok {
			if m := cfgSnap.ModelProfiles.ResolveCloudModelForTier(prof, cfg.TierEveryday); m != "" {
				return m
			}
		}
		if m := p.ActiveCloudModel(); m != "" {
			return m
		}
	}
	// OpenChatModel has a pointer receiver; take the address of the snapshot.
	return (&cfgSnap).OpenChatModel()
}

// Rebuild re-derives providers from current config.
// Was rebuildCloud on Server.
func (p *service) Rebuild() error {
	return p.rebuildCloud()
}

// InstallAbsentCloud clears the native cloud provider and points both the
// router and the coordinator's CloudModel at the absent sentinel, so a failed
// rebuild never leaves a half-wired cloud.
func (p *service) InstallAbsentCloud(reason string) {
	p.installAbsentCloud(reason)
}

// --- internal helpers ---

// installAbsentCloud is the internal (unexported) implementation. Both
// InstallAbsentCloud (interface) and rebuildCloud call this.
func (p *service) installAbsentCloud(reason string) {
	p.SetCloudLLMProvider(nil)
	absent := legacymodels.NewAbsentCloudProvider(reason)
	p.router.SetCloudProvider(absent)
	if p.coordinator != nil {
		p.coordinator.SetCloudProvider(absent)
	}
}

// rebuildCloud resolves the active profile + its key and rewires BOTH the native
// tool-loop cloud provider and the router/coordinator CloudModel. On any failure
// (no active profile, no key, unsupported flavor, keychain down) it clears the
// native cloud provider and installs the absent-cloud sentinel — the agent keeps
// running with cloud absent.
//
// After the config-service extraction, rebuildCloud no longer holds cfgMu — it
// reads a snapshot from cfgSvc. The split between config mutation and provider
// reconfiguration is now sequential (config updates first via cfgSvc, then
// rebuildCloud). A concurrent reader can momentarily observe new config with old
// provider wiring; this window is the documented trade-off of the extraction.
func (p *service) rebuildCloud() error {
	c := p.cfgSvc.Get()
	var prof cfg.CloudProfile
	found := false
	for _, pp := range c.CloudProfiles {
		if pp.Name == c.ActiveCloudProfile {
			prof = pp
			found = true
			break
		}
	}
	if !found {
		p.installAbsentCloud("no active cloud profile")
		return fmt.Errorf("no active cloud profile")
	}
	st := p.cfgSvc.Secrets()
	key := ""
	if st != nil {
		if k, err := st.Get(prof.Name); err == nil {
			key = k
		}
	}
	// If neither a key nor a proxy BaseURL is present the profile cannot
	// authenticate — install the absent sentinel rather than wiring a dead
	// provider. Carve-outs: a proxy BaseURL (Meridian) handles auth with an
	// empty key; and bedrock authenticates via the AWS credential chain, so it
	// legitimately has no keychain key (its failure mode is a missing region).
	if key == "" && prof.BaseURL == "" && prof.Flavor != cloudfactory.FlavorBedrock {
		p.installAbsentCloud("no API key for profile " + prof.Name)
		return fmt.Errorf("no API key for profile %s", prof.Name)
	}
	var cloudOpts cloudfactory.Options
	if prof.Flavor == cloudfactory.FlavorResponses && prof.Route == cloudfactory.RouteChatGPT {
		// ChatGPT subscription: authenticate via a refreshing token source over
		// the keychain (the profile's key slot holds the token JSON), not a
		// static API key.
		cloudOpts.TokenSource = chatgptauth.NewSource(st, prof.Name, chatgptauth.Flow{})
	}
	if prof.Flavor == cloudfactory.FlavorMessages && prof.Route == cloudfactory.RouteSubscription {
		// Claude subscription: same shape as ChatGPT — a refreshing token
		// source over the keychain, not a static API key.
		cloudOpts.AnthropicTokenSource = anthropicauth.NewSource(st, prof.Name, anthropicauth.Flow{})
	}
	prov, err := cloudfactory.BuildCloudProvider(prof, key, cloudOpts)
	if err != nil {
		p.installAbsentCloud(err.Error())
		return err
	}
	// A configured backup profile wraps the primary in a fallback composite;
	// everything downstream (native loop, router, coordinator) sees one
	// provider. A missing/unbuildable backup leaves the primary bare.
	prov = p.wrapBackup(prov, prof.Name, c)
	p.SetCloudLLMProvider(prov)
	mp := agent.NewLLMModelProvider(prov, prof.Model)
	p.router.SetCloudProvider(mp)
	if p.coordinator != nil {
		p.coordinator.SetCloudProvider(mp)
	}
	p.cfgSvc.SetCloudModel(prof.Model) // keep CloudModel reporting consistent
	return nil
}

// wrapBackup wraps the freshly built active-profile provider in a fallback
// composite when a backup profile is configured and buildable.
// cfg is the config snapshot already held by the caller (avoids a second Get()).
// A backup that can't be built is reported and skipped — a broken backup must
// never take down a working primary, so every failure path returns the primary unchanged.
func (p *service) wrapBackup(primary llm.Provider, primaryName string, c cfg.Config) llm.Provider {
	name := c.BackupCloudProfile
	if name == "" || name == primaryName {
		return primary
	}
	bp, ok := profileByName(c.CloudProfiles, name)
	if !ok {
		log.Printf("[cloud] backup profile %q not found; running without fallback", name)
		return primary
	}
	st := p.cfgSvc.Secrets()
	key := ""
	if st != nil {
		if k, err := st.Get(bp.Name); err == nil {
			key = k
		}
	}
	// Same authentication carve-outs as the primary build in rebuildCloud:
	// a proxy BaseURL (Meridian) authenticates with no key, and bedrock uses
	// the AWS credential chain.
	if key == "" && bp.BaseURL == "" && bp.Flavor != cloudfactory.FlavorBedrock {
		log.Printf("[cloud] backup profile %q has no API key; running without fallback", name)
		return primary
	}
	var opts cloudfactory.Options
	if bp.Flavor == cloudfactory.FlavorResponses && bp.Route == cloudfactory.RouteChatGPT {
		opts.TokenSource = chatgptauth.NewSource(st, bp.Name, chatgptauth.Flow{})
	}
	if bp.Flavor == cloudfactory.FlavorMessages && bp.Route == cloudfactory.RouteSubscription {
		opts.AnthropicTokenSource = anthropicauth.NewSource(st, bp.Name, anthropicauth.Flow{})
	}
	backup, err := cloudfactory.BuildCloudProvider(bp, key, opts)
	if err != nil {
		log.Printf("[cloud] backup profile %q unbuildable (%v); running without fallback", name, err)
		return primary
	}
	return fallback.New(primary, backup, bp.Model, func(stage string, ferr error) {
		log.Printf("[cloud] failover to backup %q (%s): primary error: %v", name, stage, ferr)
	})
}

// ReconfigureArgs carries the provider/runtime-facing arguments from an
// UpdateConfig request. All fields are from the request; empty means
// "no change for this field". ResolvedOpenModel is the fully resolved model
// name for the open runtime (UpdateConfig computes this after detecting
// llama-server defaults; Reconfigure applies it without re-doing the
// detection). MutatedConfig is the full config snapshot after all UpdateConfig
// mutations have been applied — used to rebuild openLLMProvider via the factory.
type ReconfigureArgs struct {
	// OllamaURL triggers a health-monitor restart when non-empty.
	OllamaURL string
	// OpenModel triggers openProvider.SetModelName when non-empty.
	OpenModel string
	// OpenRuntime triggers openProvider.SetEngine when non-empty.
	OpenRuntime string
	// ResolvedOpenModel is the fully-computed model for the engine swap
	// (may differ from OpenModel when the llama-server default is applied).
	ResolvedOpenModel string
	// MutatedConfig is the full config after all UpdateConfig mutations.
	// Required when OpenRuntime is non-empty so openProviderFactory can
	// rebuild the open LLM provider with the new runtime selection.
	MutatedConfig cfg.Config
}

// Reconfigure applies the UpdateConfig provider/runtime block that previously
// lived inline in UpdateConfig on the front door. It:
//   - Restarts the Ollama engine health monitor when OllamaURL is non-empty.
//   - Calls openProvider.SetModelName when OpenModel is non-empty.
//   - Calls openProvider.SetEngine(engine, ResolvedOpenModel) when OpenRuntime
//     is non-empty.
//   - Rebuilds openLLMProvider via openProviderFactory when OpenRuntime is
//     non-empty and a factory is installed.
//
// All fields that are empty mean "no change for this field".
func (p *service) Reconfigure(args ReconfigureArgs) {
	if args.OllamaURL != "" {
		if p.healthMonitorCancel != nil {
			p.healthMonitorCancel()
		}
		if p.registry != nil {
			if eng, err := p.registry.GetEngine("ollama"); err == nil {
				if confEng, ok := eng.(engine.ConfigurableEngine); ok {
					confEng.SetBaseURL(args.OllamaURL)
					// Start health monitor for the new remote endpoint.
					monitorCtx, cancel := context.WithCancel(context.Background())
					p.healthMonitorCancel = cancel
					confEng.StartHealthMonitor(monitorCtx, 30*time.Second, 3)
				}
			}
		}
	}
	if args.OpenModel != "" && p.openProvider != nil {
		p.openProvider.SetModelName(args.OpenModel)
	}
	if args.OpenRuntime != "" && p.openProvider != nil && p.registry != nil {
		if eng, err := p.registry.GetEngine(args.OpenRuntime); err == nil {
			p.openProvider.SetEngine(eng, args.ResolvedOpenModel)
		}
	}
	if args.OpenRuntime != "" && p.openProviderFactory != nil {
		// Rebuild the native open provider for the new runtime. Without this,
		// the dispatch engine's open lane (watchdog, coproc caps) keeps talking
		// to the previous runtime until the agent restarts.
		p.openLLMProvider = p.openProviderFactory(args.MutatedConfig)
	}
}

// --- helpers ---

func profileByName(profiles []cfg.CloudProfile, name string) (cfg.CloudProfile, bool) {
	for _, pr := range profiles {
		if pr.Name == name {
			return pr, true
		}
	}
	return cfg.CloudProfile{}, false
}

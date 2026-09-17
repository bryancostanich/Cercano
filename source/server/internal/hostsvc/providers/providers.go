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
	"cercano/source/server/internal/modelmetadata"
	"cercano/source/server/internal/reasoningexperiment"
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/chatgptauth"
	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/engine"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/profilechain"
	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/loop"
	"cercano/source/server/internal/ollamacatalog"
	"cercano/source/server/internal/openmodels"
	"cercano/source/server/internal/routinglog"
	"cercano/source/server/internal/usage"
	cfg "cercano/source/server/pkg/config"
)

// RouterCloudUpdater is the subset of the router interface the providers service
// needs to propagate a runtime cloud-provider swap. Mirrored from internal/server
// so providers does not import the server package.
type RouterCloudUpdater interface {
	SetOpenProvider(p agent.TurnRunner)
	SetCloudProvider(p agent.TurnRunner)
	Tiers() agent.Tiers
}

// Resolver is the interface the front door (Server) depends on for
// provider/model resolution.
type Resolver interface {
	// Main returns the provider + model for the active locus mode, plus fallback signal.
	// Was resolveMainProvider on Server.
	Main() (prov inference.Provider, isCloud bool, fellBack bool, err error)

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
	Candidates() inference.Tiers
	Cloud() inference.Provider

	// Open returns the raw (unwrapped) local LLM provider.
	Open() inference.Provider

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

	// Reconfigure applies the UpdateConfig provider/runtime block. Restarts the
	// Ollama health monitor, updates the open provider's model/engine, and
	// rebuilds the open LLM provider via the factory when the runtime changes.
	Reconfigure(args ReconfigureArgs)

	// SetCloudLLMProvider wires the native-tool-calling cloud provider.
	SetCloudLLMProvider(p inference.Provider)

	// SetOpenLLMProvider wires the native-tool-calling local provider (Ollama).
	SetOpenLLMProvider(p inference.Provider)

	// SetOpenProviderFactory installs the constructor used to rebuild the native
	// open provider when the local runtime selection changes at runtime.
	SetOpenProviderFactory(fn func(cfg.Config) inference.Provider)

	// CloudLLMProvider returns the raw cloud provider (for dispatch engine).
	CloudLLMProvider() inference.Provider

	// OpenLLMProvider returns the raw local provider (for dispatch engine).
	OpenLLMProvider() inference.Provider

	// SetCatalogManager wires the online-catalog manager.
	SetCatalogManager(cm *ollamacatalog.Manager)

	// SetUsageSink installs the token-usage recording sink used by Main().
	SetUsageSink(fn func(usage.Usage))

	// SetRoutingLog installs the structured routing/failover diagnostic sink.
	SetRoutingLog(w *routinglog.Writer)

	// SetModelSupportsVision installs the confirmed image-capability oracle used
	// when building OpenAI-compatible cloud clients. Nil leaves those clients
	// reporting no vision support, which is the safe default: the transport can
	// always carry an image, but only the model can read one.
	SetModelSupportsVision(fn func(model string) bool)
	SetProfileModelEvidence(fn func(cfg.CloudProfile, string) modelmetadata.Evidence)
}

// service is the concrete Resolver implementation.
type cloudRoutingState struct {
	primary, secondary inference.Provider
	config             cfg.Config
}
type providerSlot struct{ provider inference.Provider }

type service struct {
	rebuildMu            sync.Mutex
	cloudState           atomic.Pointer[cloudRoutingState]
	openState            atomic.Pointer[providerSlot]
	cfgSvc               cfgsvc.Service
	secondaryLLMProvider inference.Provider
	cloudLLMProvider     inference.Provider
	openLLMProvider      inference.Provider
	openProviderFactory  func(cfg.Config) inference.Provider // rebuilds openLLMProvider on runtime change
	// openModels resolves the EFFECTIVE open model for a tier on the active
	// runtime (override-else-catalog-default). A required collaborator,
	// constructed with config + catalog at startup — never nil in production.
	openModels          *openmodels.Resolver
	cloudFactory        agent.CloudFactory
	router              RouterCloudUpdater
	coordinator         *loop.ADKCoordinator
	registry            *engine.EngineRegistry
	healthMonitorCancel context.CancelFunc // cancel function for the active health monitor
	catalogManager      *ollamacatalog.Manager

	// usageSink wraps the main-loop provider for token recording.
	usageSink  func(usage.Usage)
	routingLog *routinglog.Writer

	// modelSupportsVision reports confirmed image capability for a cloud model.
	// Consulted when building OpenAI-compatible clients, whose transport can
	// encode images for any model regardless of whether that model can read one.
	modelSupportsVision  func(model string) bool
	profileModelEvidence func(cfg.CloudProfile, string) modelmetadata.Evidence
}

// New constructs a Resolver with the collaborators it needs.
// openProvider, cloudFactory, coordinator, router, registry may be nil when
// not applicable (e.g. tests, minimal embeddings).
func New(
	cfgSvc cfgsvc.Service,
	openModels *openmodels.Resolver,
	router RouterCloudUpdater,
	coordinator *loop.ADKCoordinator,
	cloudFactory agent.CloudFactory,
	registry *engine.EngineRegistry,
	usageSink func(usage.Usage),
) Resolver {
	return &service{
		cfgSvc:       cfgSvc,
		openModels:   openModels,
		router:       router,
		coordinator:  coordinator,
		cloudFactory: cloudFactory,
		registry:     registry,
		usageSink:    usageSink,
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// --- Resolver interface implementation ---

func (p *service) Cloud() inference.Provider {
	if state := p.cloudState.Load(); state != nil {
		return state.primary
	}
	return p.cloudLLMProvider
}
func (p *service) Open() inference.Provider {
	if state := p.openState.Load(); state != nil {
		return state.provider
	}
	return p.openLLMProvider
}
func (p *service) Router() RouterCloudUpdater             { return p.router }
func (p *service) Registry() *engine.EngineRegistry       { return p.registry }
func (p *service) CatalogManager() *ollamacatalog.Manager { return p.catalogManager }
func (p *service) CloudLLMProvider() inference.Provider   { return p.Cloud() }
func (p *service) OpenLLMProvider() inference.Provider    { return p.Open() }

func (p *service) SetCloudLLMProvider(prov inference.Provider) {
	state := &cloudRoutingState{primary: prov, config: p.cfgSvc.Get()}
	if previous := p.cloudState.Load(); previous != nil {
		state.config = previous.config
		state.secondary = previous.secondary
	}
	p.cloudState.Store(state)
}
func (p *service) SetOpenLLMProvider(prov inference.Provider) {
	p.openState.Store(&providerSlot{provider: prov})
	if prov == nil {
		if p.router != nil {
			p.router.SetOpenProvider(nil)
		}
		if p.coordinator != nil {
			p.coordinator.SetOpenProvider(nil)
		}
	}
}
func (p *service) SetCatalogManager(cm *ollamacatalog.Manager) { p.catalogManager = cm }
func (p *service) SetUsageSink(fn func(usage.Usage))           { p.usageSink = fn }
func (p *service) SetRoutingLog(w *routinglog.Writer)          { p.routingLog = w }
func (p *service) SetModelSupportsVision(fn func(model string) bool) {
	p.modelSupportsVision = fn
}
func (p *service) SetOpenProviderFactory(fn func(cfg.Config) inference.Provider) {
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
	return p.MainModel(true)
}

// Main returns the provider + model for the active locus mode.
// Was resolveMainProvider on Server.
func (p *service) Main() (inference.Provider, bool, bool, error) {
	c := p.cfgSvc.Get()
	mode, _ := locus.ParseMode(c.LocusMode)
	// Register the open tier absent when its GGUF isn't on disk yet (e.g. still
	// downloading after setup) so Select crosses to cloud — the "cloud covers
	// the gap" routing contract. Otherwise the not-yet-present model gets
	// picked and fails at load time instead of falling back.
	candidates := p.Candidates()
	assignment := c.TaskAssignment(cfg.TaskChat)
	if candidates.TaskFor != nil {
		assignment = candidates.TaskFor(cfg.TaskChat)
	}
	open := candidates.Open
	model := ""
	if candidates.ModelFor != nil {
		model = candidates.ModelFor(inference.Selection{IsCloud: false}, assignment.Quality.CapabilityTier())
	}
	if !dispatch.OpenModelReadyFor(c, model) {
		open = nil
	}
	candidates.Open = open
	sel, err := inference.SelectDestination(mode, assignment.Destination, candidates)
	if err != nil {
		return nil, false, false, err
	}
	// Wrap the selected provider for "main" token-usage recording at hand-off.
	// The stored providers stay raw (the dispatch engine reads them raw and
	// wraps per-dispatch with its own source), so there's no double-counting.
	if sel.IsCloud {
		model = inference.TargetForCall(sel.Provider, inference.Call{Tier: string(assignment.Quality.CapabilityTier())}).Model
	}
	prov := usage.Wrap(inference.WithTaskRoute(sel.Provider, cfg.TaskChat, assignment, sel.PolicyDestination, model), "main", sel.IsCloud, p.usageSink)
	return prov, sel.IsCloud, sel.FellBack, nil
}

// MainModel returns the configured model name for the active tier. Cloud
// reads from the active profile (the single source of truth — see
// ActiveCloudModel) so a profile-model change propagates without restart.
// Was mainModelFor on Server.
func (p *service) MainModel(isCloud bool) string {
	c := p.cfgSvc.Get()
	a := c.TaskAssignment(cfg.TaskChat)
	if isCloud {
		destination, err := c.ResolveDestination(a.Destination)
		if err != nil {
			return ""
		}
		name, _ := c.DestinationProfiles(destination)
		if prof, ok := c.Profile(name); ok {
			return c.ModelProfiles.ResolveCloudModelForTier(prof, a.Quality.CapabilityTier())
		}
		return ""
	}
	if p.openModels == nil {
		return ""
	}
	return p.openModels.Model(a.Quality.CapabilityTier())
}

func (p *service) PrimaryModel() string {
	c := p.cfgSvc.Get()
	a := c.TaskAssignment(cfg.TaskChat)
	destination, err := c.ResolveDestination(a.Destination)
	if err != nil {
		return ""
	}
	cloud := destination != cfg.DestinationLocal && c.LocusMode != "open_only" && (destination == cfg.DestinationSecondary || c.LocusMode != "open_primary")
	return p.MainModel(cloud)
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
	absent := agent.AbsentCloud(reason)
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
// A complete immutable cloud binding graph is published after both chains are
// built. Serialized rebuilds cannot overwrite a newer configuration with an older one.
func (p *service) rebuildCloud() error {
	p.rebuildMu.Lock()
	defer p.rebuildMu.Unlock()
	c := p.cfgSvc.Get()
	secondary, _ := profilechain.Build(c, cfg.DestinationSecondary, p.buildProfile, p.chainEvents(c, cfg.DestinationSecondary))
	provider, err := profilechain.Build(c, cfg.DestinationPrimary, p.buildProfile, p.chainEvents(c, cfg.DestinationPrimary))
	p.cloudState.Store(&cloudRoutingState{primary: provider, secondary: secondary, config: c})
	if err != nil {
		p.installAbsentCloud(err.Error())
		return err
	}
	// The legacy CloudModel remains bound to Primary; never pair it with a
	// model from the independently selected Secondary profile.
	model := inference.TargetForCall(provider, inference.Call{Tier: string(c.TaskAssignment(cfg.TaskChat).Quality.CapabilityTier())}).Model
	mp := agent.InferenceTurnRunner(provider, model)
	p.router.SetCloudProvider(mp)
	if p.coordinator != nil {
		p.coordinator.SetCloudProvider(mp)
	}
	p.cfgSvc.SetCloudModel(model)
	return nil
}

func (p *service) buildProfile(prof cfg.CloudProfile) (inference.Provider, error) {
	st := p.cfgSvc.Secrets()
	key := ""
	var keyErr error
	if st != nil && !cloudfactory.IsSubscription(prof) && prof.Flavor != cloudfactory.FlavorBedrock {
		key, keyErr = st.Get(prof.Name)
	}
	if err := cloudfactory.ValidateStaticCredential(prof, key, keyErr); err != nil {
		return nil, err
	}
	confirmed := p.modelSupportsVision
	if p.profileModelEvidence != nil {
		confirmed = func(model string) bool {
			return p.profileModelEvidence(prof, model).Vision == modelmetadata.VisionSupported
		}
	}
	opts := cloudfactory.Options{ModelSupportsVision: confirmed}
	if prof.Flavor == cloudfactory.FlavorResponses && prof.Route == cloudfactory.RouteChatGPT {
		opts.TokenSource = p.cfgSvc.Credentials().ChatGPT(prof.Name, chatgptauth.Flow{})
	}
	if prof.Flavor == cloudfactory.FlavorMessages && prof.Route == cloudfactory.RouteSubscription {
		opts.AnthropicTokenSource = p.cfgSvc.Credentials().Anthropic(prof.Name, anthropicauth.Flow{})
	}
	provider, err := cloudfactory.BuildCloudProvider(prof, key, opts)
	if err != nil {
		return nil, err
	}
	return profilechain.GuardVision(provider, confirmed, func(model string) modelmetadata.Evidence {
		if p.profileModelEvidence == nil {
			return modelmetadata.Evidence{}
		}
		return p.profileModelEvidence(prof, model)
	}), nil
}

func (p *service) Candidates() inference.Tiers {
	c := p.cfgSvc.Get()
	primary, secondary := p.cloudLLMProvider, p.secondaryLLMProvider
	if state := p.cloudState.Load(); state != nil {
		c = state.config
		primary = state.primary
		secondary = state.secondary
	}
	mode, _ := locus.ParseMode(c.LocusMode)
	models := map[cfg.Tier]string{}
	if p.openModels != nil {
		models = p.openModels.ModelsForConfig(c)
	}
	modelFor := func(sel inference.Selection, t cfg.Tier) string {
		if !sel.IsCloud {
			return models[t]
		}
		if profile, ok := c.Profile(sel.Profile); ok {
			return c.ModelProfiles.ResolveCloudModelForTier(profile, t)
		}
		return ""
	}
	return inference.Tiers{Mode: mode, ModelFor: modelFor, OpenReady: func(model string) bool { return dispatch.OpenModelReadyFor(c, model) }, Cloud: primary, Open: p.Open(), TaskFor: c.TaskAssignment, ResolveDestination: c.ResolveDestination, Destinations: map[cfg.Destination]inference.Candidate{
		cfg.DestinationPrimary:   {Provider: primary, Profile: c.ActiveCloudProfile, IsCloud: true},
		cfg.DestinationSecondary: {Provider: secondary, Profile: c.SecondaryCloudProfile, IsCloud: true},
	}}

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
	// OpenModel triggers rebuilding/re-wrapping the Open tier when non-empty.
	OpenModel string
	// OpenRuntime triggers rebuilding the raw open inference provider and
	// re-wrapping the Open tier when non-empty.
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
//   - Rebuilds openLLMProvider via openProviderFactory when OpenRuntime is
//     non-empty and a factory is installed.
//   - Re-wraps the open inference provider as a fresh TurnRunner and resets the
//     router/coordinator Open tier when OpenRuntime or OpenModel changes.
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
	if args.OpenRuntime != "" && p.openProviderFactory != nil {
		// Rebuild the native open provider for the new runtime. Without this,
		// the dispatch engine's open lane (watchdog, coproc caps) keeps talking
		// to the previous runtime until the agent restarts.
		p.SetOpenLLMProvider(p.openProviderFactory(args.MutatedConfig))
	}
	if args.OpenRuntime != "" || args.OpenModel != "" {
		model := args.ResolvedOpenModel
		if model == "" {
			model = args.OpenModel
		}
		p.setOpenTurnRunner(model)
	}
}

// setOpenTurnRunner wraps the current open inference provider as a fresh
// TurnRunner and installs it into the router/coordinator open slots. This is the
// rebuild-on-switch replacement for the old mutable open-provider
// SetEngine/SetModelName path.
func (p *service) setOpenTurnRunner(model string) {
	if p.Open() == nil || model == "" {
		return
	}
	tr := agent.InferenceTurnRunner(p.Open(), model)
	if p.router != nil {
		p.router.SetOpenProvider(tr)
	}
	if p.coordinator != nil {
		p.coordinator.SetOpenProvider(tr)
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

func (p *service) SetProfileModelEvidence(fn func(cfg.CloudProfile, string) modelmetadata.Evidence) {
	p.profileModelEvidence = fn
}

func (p *service) chainEvents(c cfg.Config, d cfg.Destination) func(resilience.Event) {
	preferred, backup := c.DestinationProfiles(d)
	return func(ev resilience.Event) {
		log.Printf("[cloud] %s resilience %s (%s, %s): %s: %v", d, ev.Action, ev.Stage, ev.Class, ev.Notice(), ev.Err)
		if p.routingLog != nil {
			p.routingLog.Log("cloud.resilience", routinglog.Event{"destination": string(d), "primary_profile": preferred, "backup_profile": backup, "action": string(ev.Action), "stage": ev.Stage, "error_class": string(ev.Class), "from_provider": ev.From, "to_provider": ev.To, "wait_ms": ev.Wait.Milliseconds(), "notice": ev.Notice(), "error": errorString(ev.Err)})
		}
	}
}

// RunReasoningDiagnostic deliberately bypasses destination retry/fallback chains.
func (p *service) RunReasoningDiagnostic(ctx context.Context, spec reasoningexperiment.Spec) (reasoningexperiment.Report, error) {
	return reasoningexperiment.Run(ctx, p.cfgSvc.Get(), spec, p.buildProfile)
}

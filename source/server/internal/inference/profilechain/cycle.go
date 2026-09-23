package profilechain

import (
	"context"
	"fmt"
	"sync"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
)

// CycleState survives provider rebuilds. Generation checks prevent a stale
// in-flight request from advancing a newer account selection, including ABA.
type CycleState struct {
	mu         sync.Mutex
	active     string
	generation uint64
	epoch      uint64
}

type account struct {
	name                 string
	provider             inference.Provider
	modelFor             func(string) string
	blocked, unavailable bool
}

type cycle struct {
	accounts    []account
	state       *CycleState
	onEvent     func(resilience.Event)
	defaultTier string
	chains      []*namedEngine
	epoch       uint64
}

// StateOf allows a host to retain selection across configuration rebuilds.
func StateOf(p inference.Provider) *CycleState {
	if c, ok := p.(*cycle); ok {
		return c.state
	}
	return nil
}

func BuildPrimary(c config.Config, build Builder, state *CycleState, events ...func(resilience.Event)) (inference.Provider, error) {
	if c.ActiveCloudProfile == "" {
		return nil, fmt.Errorf("primary has no preferred profile")
	}
	if state == nil {
		state = &CycleState{}
	}
	out := &cycle{state: state, defaultTier: string(c.TaskAssignment(config.TaskChat).Quality.CapabilityTier())}
	if len(events) > 0 {
		out.onEvent = events[0]
	}
	seen := map[string]bool{}
	usable := false
	var constructionErr error
	for _, name := range c.DestinationProfileNames(config.DestinationPrimary) {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		profile, ok := c.Profile(name)
		if !ok {
			return nil, fmt.Errorf("primary references missing profile %q", name)
		}
		modelFor := func(t string) string {
			if t == "" {
				t = string(c.TaskAssignment(config.TaskChat).Quality.CapabilityTier())
			}
			return c.ModelProfiles.ResolveCloudModelForTier(profile, config.Tier(t))
		}
		profile.Model = modelFor("")
		provider, err := build(profile)
		class := llm.ClassOf(err)
		blocked := class == llm.ErrLoginRequired || class == llm.ErrCredential || class == llm.ErrPermission
		unavailable := err != nil && !blocked
		if err == nil || blocked || (name != c.ActiveCloudProfile && class == llm.ErrAuth) {
			usable = true
		} else if constructionErr == nil {
			constructionErr = err
		}
		if err != nil {
			provider = &unavailableProvider{err: err}
		}
		if provider == nil {
			return nil, fmt.Errorf("profile %q has no provider", name)
		}
		out.accounts = append(out.accounts, account{name: name, provider: &routeProvider{Provider: provider, profile: name, destination: string(config.DestinationPrimary)}, modelFor: modelFor, blocked: blocked, unavailable: unavailable})
	}
	if !usable {
		return nil, constructionErr
	}
	state.mu.Lock()
	found := false
	for _, a := range out.accounts {
		if a.name == state.active {
			found = true
		}
	}
	if !found {
		state.active = out.accounts[0].name
		state.generation++
	}
	state.epoch++
	out.epoch = state.epoch
	state.mu.Unlock()
	for start := range out.accounts {
		out.chains = append(out.chains, out.chain(start))
	}
	return out, nil
}

func (c *cycle) selected() (int, uint64) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	for i, a := range c.accounts {
		if a.name == c.state.active {
			return i, c.state.generation
		}
	}
	return 0, c.state.generation
}

// Each stable chain is finite and contains each account exactly once. Stability
// also preserves the resilience engine's request-scoped auth fallback identity.
func (c *cycle) chain(start int) *namedEngine {
	var next *namedEngine
	for offset := len(c.accounts) - 1; offset >= 0; offset-- {
		index := (start + offset) % len(c.accounts)
		a := c.accounts[index]
		opts := resilience.Options{PrimaryBlocked: a.blocked, PrimaryUnavailable: a.unavailable, PrimaryModelFor: a.modelFor, PrimaryLabel: a.name, OnEvent: c.onEvent}
		if next != nil {
			backup := c.accounts[(index+1)%len(c.accounts)]
			opts.Backup = next
			opts.BackupLabel = backup.name
			tail := next
			opts.BackupModelFor = func(tier string) string {
				if tier == "" {
					tier = c.defaultTier
				}
				return inference.TargetForCall(tail, inference.Call{Tier: tier}).Model
			}
		}
		opts.OnQuota = func(ctx context.Context) {
			call, ok := ctx.Value(cycleCallKey{}).(*cycleCall)
			if !ok {
				return
			}
			call.quotas++
			c.state.mu.Lock()
			defer c.state.mu.Unlock()
			if c.state.epoch == c.epoch && c.state.generation == call.generation {
				c.state.active = c.accounts[(index+1)%len(c.accounts)].name
				c.state.generation++
				call.generation = c.state.generation
			}
		}
		next = &namedEngine{Provider: resilience.New(a.provider, opts), label: a.name}
	}
	return next
}

type cycleCallKey struct{}
type cycleCall struct {
	generation uint64
	quotas     int
}
type namedEngine struct {
	*resilience.Provider
	label string
}

func (p *namedEngine) Name() string { return p.label }

func (c *cycle) selectedForContext(ctx context.Context) (int, uint64) {
	i, g := c.selected()
	// An explicitly authorized fallback stays request-owned even if another
	// request advances the global account cursor in the meantime.
	for start, p := range c.chains {
		if llm.AuthFallbackSelected(ctx, p.Provider) {
			return start, g
		}
	}
	return i, g
}
func (c *cycle) request(req inference.Call, index int) inference.Call {
	if req.Tier == "" && req.FallbackTier == "" {
		req.FallbackTier = c.defaultTier
	}
	if index != 0 {
		if req.FallbackTier != "" {
			req.Tier = req.FallbackTier
			req.FallbackTier = ""
		}
		req.Model = c.accounts[index].modelFor(req.Tier)
	}
	return req
}
func (c *cycle) Name() string { i, _ := c.selected(); return c.accounts[i].provider.Name() }
func (c *cycle) Capabilities() inference.Capabilities {
	i, _ := c.selected()
	return c.chains[i].Capabilities()
}
func (c *cycle) TargetFor(model, tier string) llm.ServingRoute {
	return c.TargetForCall(inference.Call{Model: model, Tier: tier})
}
func (c *cycle) TargetForCall(req inference.Call) llm.ServingRoute {
	i, _ := c.selected()
	return inference.TargetForCall(c.chains[i], c.request(req, i))
}
func (c *cycle) TargetForContext(ctx context.Context, req inference.Call) llm.ServingRoute {
	i, _ := c.selectedForContext(ctx)
	return inference.TargetForContext(ctx, c.chains[i], c.request(req, i))
}
func (c *cycle) RuntimeContext(ctx context.Context, model string, prepare bool) (llm.RuntimeContext, error) {
	i, _ := c.selectedForContext(ctx)
	return llm.ResolveRuntimeContext(ctx, c.chains[i], model, prepare)
}
func (c *cycle) exhausted(err error, quotas int) error {
	if err != nil && quotas == len(c.accounts) && llm.ClassOf(err) == llm.ErrQuota {
		return fmt.Errorf("all configured cloud accounts exhausted: %w", err)
	}
	return err
}
func (c *cycle) Chat(ctx context.Context, req inference.Call) (inference.Result, error) {
	i, g := c.selectedForContext(ctx)
	call := &cycleCall{generation: g}
	result, err := c.chains[i].Chat(context.WithValue(ctx, cycleCallKey{}, call), c.request(req, i))
	return result, c.exhausted(err, call.quotas)
}
func (c *cycle) StreamChat(ctx context.Context, req inference.Call) (inference.Stream, error) {
	i, g := c.selectedForContext(ctx)
	call := &cycleCall{generation: g}
	stream, err := c.chains[i].StreamChat(context.WithValue(ctx, cycleCallKey{}, call), c.request(req, i))
	if err != nil {
		return nil, c.exhausted(err, call.quotas)
	}
	return &cycleStream{Stream: stream, cycle: c, call: call}, nil
}

type cycleStream struct {
	inference.Stream
	cycle *cycle
	call  *cycleCall
}

func (s *cycleStream) Next() (llm.StreamEvent, bool, error) {
	ev, ok, err := s.Stream.Next()
	if ok && ev.Type == llm.EventError {
		ev.Err = s.cycle.exhausted(ev.Err, s.call.quotas)
		if ev.Err != nil {
			ev.ErrText = ev.Err.Error()
		}
	}
	return ev, ok, s.cycle.exhausted(err, s.call.quotas)
}

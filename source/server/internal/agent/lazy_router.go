package agent

import (
	"fmt"
	"strings"
	"sync"
)

// SmartRouterFactory builds a SmartRouter. It is invoked on first use of
// LazyRouter so that servers can start before the embedding model is available
// (e.g. MCP-only deployments never need the router at all).
type SmartRouterFactory func() (*SmartRouter, error)

// LazyRouter implements the Router interface but defers construction of the
// underlying SmartRouter until the first call that actually needs it.
//
// Motivation: the MCP plugin path (the bulk of Cercano usage) does not touch
// the router. Building it eagerly forces every user to install nomic-embed-text
// just so the server can start — even though they will never classify intent.
// See GitHub issue #5.
type LazyRouter struct {
	factory       SmartRouterFactory
	openProvider  TurnRunner
	cloudProvider TurnRunner
	// pendingMu guards provider overrides set before the underlying SmartRouter
	// is built; they are flushed into it in ensure().
	pendingMu    sync.Mutex
	pendingCloud TurnRunner
	pendingOpen  TurnRunner

	once sync.Once
	real *SmartRouter
	err  error
}

// NewLazyRouter returns a LazyRouter that will invoke factory on first use.
// openProvider and cloudProvider are held so Tiers() works before
// the underlying SmartRouter is built (e.g. for DirectOpen bypass paths that
// only need the providers, not classification).
func NewLazyRouter(factory SmartRouterFactory, openProvider, cloudProvider TurnRunner) *LazyRouter {
	return &LazyRouter{
		factory:       factory,
		openProvider:  openProvider,
		cloudProvider: cloudProvider,
	}
}

// ensure builds the underlying SmartRouter exactly once. Returns the cached
// error on repeat calls so failures are stable across retries.
func (lr *LazyRouter) ensure() (*SmartRouter, error) {
	lr.once.Do(func() {
		lr.real, lr.err = lr.factory()
		if lr.err != nil {
			lr.err = wrapRouterInitError(lr.err)
			return
		}
		// Apply any providers that were set before the router was built.
		lr.pendingMu.Lock()
		pendingCloud := lr.pendingCloud
		pendingOpen := lr.pendingOpen
		lr.pendingMu.Unlock()
		if pendingOpen != nil {
			lr.real.SetOpenProvider(pendingOpen)
		}
		if pendingCloud != nil {
			lr.real.SetCloudProvider(pendingCloud)
		}
	})
	return lr.real, lr.err
}

// ClassifyIntent builds the router on first call.
func (lr *LazyRouter) ClassifyIntent(req *Request) (Intent, error) {
	real, err := lr.ensure()
	if err != nil {
		return "", err
	}
	return real.ClassifyIntent(req)
}

// SelectProvider builds the router on first call.
func (lr *LazyRouter) SelectProvider(req *Request, intent Intent) (TurnRunner, error) {
	real, err := lr.ensure()
	if err != nil {
		return nil, err
	}
	return real.SelectProvider(req, intent)
}

// Tiers returns the tier pair without triggering router construction. The
// DirectOpen bypass and cloud-provider override paths only need the raw
// providers, not classification — forcing a build here would re-introduce the
// eager-init bug for those paths.
func (lr *LazyRouter) Tiers() Tiers {
	// Prefer the built router's tiers if it exists so runtime SetOpenProvider /
	// SetCloudProvider updates are reflected.
	if lr.real != nil {
		return lr.real.Tiers()
	}
	t := Tiers{Open: lr.openProvider}
	lr.pendingMu.Lock()
	open := lr.pendingOpen
	cloud := lr.pendingCloud
	lr.pendingMu.Unlock()
	if open != nil {
		t.Open = open
	}
	if cloud != nil {
		t.Cloud = cloud
	} else if lr.cloudProvider != nil {
		t.Cloud = lr.cloudProvider
	}
	return t
}

// SetCloudProvider updates the cloud provider. If the underlying router is
// already built, the call is delegated. Otherwise the provider is stashed and
// applied the first time the router gets built.
func (lr *LazyRouter) SetCloudProvider(p TurnRunner) {
	if lr.real != nil {
		lr.real.SetCloudProvider(p)
		return
	}
	lr.pendingMu.Lock()
	lr.pendingCloud = p
	lr.pendingMu.Unlock()
}

// SetOpenProvider updates the open provider. If the underlying router is
// already built, the call is delegated. Otherwise the provider is stashed and
// applied the first time the router gets built. This is the open-runtime twin
// of SetCloudProvider and replaces the old mutable open-provider path.
func (lr *LazyRouter) SetOpenProvider(p TurnRunner) {
	if lr.real != nil {
		lr.real.SetOpenProvider(p)
		return
	}
	lr.pendingMu.Lock()
	lr.pendingOpen = p
	lr.openProvider = p
	lr.pendingMu.Unlock()
}

// wrapRouterInitError turns low-level errors from SmartRouter construction
// into a clean, actionable message for the agent-mode user. The original error
// is preserved via %w for debugging.
func wrapRouterInitError(err error) error {
	msg := err.Error()
	lower := strings.ToLower(msg)

	switch {
	case strings.Contains(lower, "not found, try pulling"):
		return fmt.Errorf("agent-mode routing requires an embedding model that is not installed on Ollama. "+
			"Run `ollama pull nomic-embed-text` (or whatever model is set as embedding_model in ~/.config/cercano/config.yaml) "+
			"and restart Cercano. MCP tools continue to work without this. (underlying: %w)", err)
	case strings.Contains(lower, "connection refused"), strings.Contains(lower, "no such host"):
		return fmt.Errorf("agent-mode routing requires a reachable Ollama instance. "+
			"Check that Ollama is running and that ollama_url is correct. MCP tools continue to work without this. (underlying: %w)", err)
	default:
		return fmt.Errorf("agent-mode routing unavailable: %w", err)
	}
}

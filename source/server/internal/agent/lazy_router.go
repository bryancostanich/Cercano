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
	factory        SmartRouterFactory
	localProvider  ModelProvider
	cloudProvider  ModelProvider
	pendingCloudMu sync.Mutex
	pendingCloud   ModelProvider

	once sync.Once
	real *SmartRouter
	err  error
}

// NewLazyRouter returns a LazyRouter that will invoke factory on first use.
// localProvider and cloudProvider are held so GetModelProviders() works before
// the underlying SmartRouter is built (e.g. for DirectLocal bypass paths that
// only need the providers, not classification).
func NewLazyRouter(factory SmartRouterFactory, localProvider, cloudProvider ModelProvider) *LazyRouter {
	return &LazyRouter{
		factory:       factory,
		localProvider: localProvider,
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
		// Apply any cloud provider that was set before the router was built.
		lr.pendingCloudMu.Lock()
		pending := lr.pendingCloud
		lr.pendingCloudMu.Unlock()
		if pending != nil {
			lr.real.SetCloudProvider(pending)
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
func (lr *LazyRouter) SelectProvider(req *Request, intent Intent) (ModelProvider, error) {
	real, err := lr.ensure()
	if err != nil {
		return nil, err
	}
	return real.SelectProvider(req, intent)
}

// GetModelProviders returns providers without triggering router construction.
// The DirectLocal bypass and cloud-provider override paths only need the raw
// providers, not classification — forcing a build here would re-introduce the
// eager-init bug for those paths.
func (lr *LazyRouter) GetModelProviders() map[string]ModelProvider {
	// Prefer the built router's map if it exists so runtime SetCloudProvider
	// updates are reflected.
	if lr.real != nil {
		return lr.real.GetModelProviders()
	}
	providers := map[string]ModelProvider{
		"LocalModel": lr.localProvider,
	}
	lr.pendingCloudMu.Lock()
	cloud := lr.pendingCloud
	lr.pendingCloudMu.Unlock()
	if cloud != nil {
		providers["CloudModel"] = cloud
	} else if lr.cloudProvider != nil {
		providers["CloudModel"] = lr.cloudProvider
	}
	return providers
}

// SetCloudProvider updates the cloud provider. If the underlying router is
// already built, the call is delegated. Otherwise the provider is stashed and
// applied the first time the router gets built.
func (lr *LazyRouter) SetCloudProvider(p ModelProvider) {
	if lr.real != nil {
		lr.real.SetCloudProvider(p)
		return
	}
	lr.pendingCloudMu.Lock()
	lr.pendingCloud = p
	lr.pendingCloudMu.Unlock()
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

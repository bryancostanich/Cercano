package anthropic

import "net/http"

// Route values. Kept here so the adapter is the single place that names the
// access paths it handles. RouteSubscription is exported so the cloud factory
// can select it without magic strings.
const (
	routeDirect = "direct"
	// RouteSubscription selects Claude Max/Pro subscription OAuth: a refreshing
	// Bearer token, the oauth beta header, and the identity system block.
	RouteSubscription = "subscription"
)

// authenticator decorates each outgoing Anthropic request with the auth and
// identity its access route requires. The client holds exactly one, chosen at
// construction — no per-request route switch. A nil authenticator means no
// decoration: the direct route, where the SDK's own x-api-key stands.
type authenticator interface {
	decorate(r *http.Request) error
}

// authenticatorForRoute picks the request authenticator for a route. Direct
// and unknown routes get nil (SDK x-api-key only).
func authenticatorForRoute(cfg Config) authenticator {
	switch cfg.Route {
	case RouteSubscription:
		return &subscriptionAuth{tokens: cfg.TokenSource}
	default:
		return nil
	}
}

// systemPrefixForRoute returns the system-prompt prefix a route requires, or
// "" for none. Only the subscription route carries one (the Claude Code
// identity block).
func systemPrefixForRoute(route string) string {
	if route == RouteSubscription {
		return claudeCodeIdentity
	}
	return ""
}

// authRoundTripper applies the common User-Agent and the route's authenticator
// to every outgoing request, then delegates to the base transport. It is the
// only place per-route request shaping happens.
type authRoundTripper struct {
	base http.RoundTripper
	ua   string
	auth authenticator // nil → no per-route decoration (direct route)
}

func (h *authRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if h.ua != "" {
		r.Header.Set("User-Agent", h.ua)
	}
	if h.auth != nil {
		if err := h.auth.decorate(r); err != nil {
			return nil, err
		}
	}
	return h.base.RoundTrip(r)
}

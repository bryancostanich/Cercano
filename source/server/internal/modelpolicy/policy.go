// Package modelpolicy is the final authorization boundary for physical model
// requests. The host installs its live authority; workers use a host-backed
// authority in the turn context. This package never owns enterprise credentials.
package modelpolicy

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

// Attempt describes the request actually being sent, after routing and adapter
// defaults. ID is deliberately absent: local labels do not establish identity.
type Attempt struct {
	Provider  string `json:"provider"`
	Endpoint  string `json:"endpoint"`
	Model     string `json:"model"`
	Placement string `json:"placement"`
}

type Authority interface {
	Authorize(context.Context, Attempt) error
}
type AuthorizeFunc func(context.Context, Attempt) error

func (f AuthorizeFunc) Authorize(ctx context.Context, a Attempt) error { return f(ctx, a) }

type authoritySlot struct{ authority Authority }

var process atomic.Pointer[authoritySlot]

type authorityKey struct{}

// Install sets the process-wide authority before serving work. A managed host
// keeps an authority installed even when its credentials are missing or revoked.
// The returned cleanup is intended for process shutdown, not logout.
func Install(a Authority) func() {
	previous := process.Swap(&authoritySlot{a})
	return func() { process.Store(previous) }
}

// InstallWorkerGuard makes a dedicated managed worker fail closed if any code
// loses the turn context. Only that context's host-backed proxy can authorize a
// call. Keep the guard for the lifetime of the process, including between turns.
func InstallWorkerGuard() func() {
	return Install(AuthorizeFunc(func(ctx context.Context, a Attempt) error {
		slot, ok := ctx.Value(authorityKey{}).(*authoritySlot)
		if !ok || slot.authority == nil {
			return Deny(a, "managed worker has no host authorization context")
		}
		return slot.authority.Authorize(ctx, a)
	}))
}

// WithAuthority carries the host's authorization proxy into worker execution.
// A process-wide host authority takes precedence over a context value.
func WithAuthority(ctx context.Context, a Authority) context.Context {
	return context.WithValue(ctx, authorityKey{}, &authoritySlot{a})
}
func authority(ctx context.Context) (Authority, bool) {
	if p := process.Load(); p != nil {
		return p.authority, true
	}
	if p, ok := ctx.Value(authorityKey{}).(*authoritySlot); ok {
		return p.authority, true
	}
	return nil, false
}
func Managed(ctx context.Context) bool { _, ok := authority(ctx); return ok }

// Denial contains only route metadata and a bounded reason, never prompts,
// headers, credentials or provider response bodies.
type Denial struct {
	Attempt Attempt
	Reason  string
}

func (e *Denial) Error() string {
	if e.Attempt.Model == "" {
		return "enterprise policy blocked this request: " + e.Reason
	}
	return fmt.Sprintf("enterprise policy blocked %q via %s at %s (%s): %s; choose an allowed route, reconnect, or contact your administrator", e.Attempt.Model, e.Attempt.Provider, e.Attempt.Endpoint, e.Attempt.Placement, e.Reason)
}

// IsDenial preserves the terminal policy failure across worker RPC boundaries.
func IsDenial(err error) bool {
	var denial *Denial
	return errors.As(err, &denial)
}

func (*Denial) EnterprisePolicyDenied() bool { return true }

// RetryableError is understood by the AWS SDK before network retry heuristics.
func (*Denial) RetryableError() bool      { return false }
func Deny(a Attempt, reason string) error { return &Denial{Attempt: a, Reason: reason} }
func Check(ctx context.Context, a Attempt) error {
	authority, managed := authority(ctx)
	if !managed {
		return nil
	}
	if authority == nil {
		return Deny(a, "authorization unavailable")
	}
	return authority.Authorize(ctx, a)
}

// Allowed compares all four physical identity fields. An empty allow-list denies
// everything; a matching model name on a different endpoint is not permission.
func Allowed(p v1.Policy, a Attempt) bool {
	for _, r := range p.AllowedRoutes {
		if r.Provider == a.Provider && r.Endpoint == a.Endpoint && r.Model == a.Model && r.Placement == a.Placement {
			return true
		}
	}
	return false
}

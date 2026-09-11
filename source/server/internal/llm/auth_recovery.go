package llm

import (
	"context"
	"errors"
	"sync"
)

type AuthChallenge struct {
	Provider, Profile, Reason string
	Fallback                  string
	RetrySafe                 bool
}
type AuthDecision string

const (
	AuthLogin    AuthDecision = "login"
	AuthFallback AuthDecision = "fallback"
	AuthCancel   AuthDecision = "cancel"
)

type AuthRequester func(context.Context, AuthChallenge) (AuthDecision, error)
type authRequesterKey struct{}
type authFallbackKey struct{}

func WithAuthRecovery(ctx context.Context, request AuthRequester) context.Context {
	ctx = context.WithValue(ctx, authChoiceKey{}, &authChoices{selected: make(map[any]bool)})
	return context.WithValue(ctx, authRequesterKey{}, request)
}
func HasAuthRecovery(ctx context.Context) bool {
	request, _ := ctx.Value(authRequesterKey{}).(AuthRequester)
	return request != nil
}
func WithExternalAuthFallback(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, authFallbackKey{}, name)
}
func ExternalAuthFallback(ctx context.Context) string {
	name, _ := ctx.Value(authFallbackKey{}).(string)
	return name
}

// RequestAuthRecovery never parses provider prose or accepts an API-key error
// as a subscription login. Login means credentials have actually been saved,
// not merely that the user opened a browser.
func RequestAuthRecovery(ctx context.Context, err error, fallback string, retrySafe bool) (AuthDecision, error) {
	var credential *CredentialError
	if ClassOf(err) != ErrLoginRequired || !errors.As(err, &credential) || credential.Method != AuthSubscription || credential.Profile == "" {
		return "", nil
	}
	request, _ := ctx.Value(authRequesterKey{}).(AuthRequester)
	if request == nil {
		return "", nil
	}
	if fallback == "" {
		fallback = ExternalAuthFallback(ctx)
	}
	if !retrySafe {
		fallback = ""
	}
	decision, e := request(ctx, AuthChallenge{Provider: credential.Provider, Profile: credential.Profile, Reason: credential.Reason, Fallback: fallback, RetrySafe: retrySafe})
	if e != nil {
		if errors.Is(e, context.Canceled) || ctx.Err() != nil {
			return "", e
		}
		return "", &CredentialError{Class: ErrCredential, Provider: credential.Provider, Profile: credential.Profile, Method: AuthSubscription, Reason: "recovery_failed", Cause: e}
	}
	if e = ctx.Err(); e != nil {
		return "", e
	}
	switch decision {
	case AuthLogin:
		return decision, nil
	case AuthFallback:
		if fallback != "" {
			return decision, nil
		}
	case AuthCancel:
		return decision, context.Canceled
	}
	return "", &CredentialError{Class: ErrCredential, Provider: credential.Provider, Profile: credential.Profile, Reason: "invalid_recovery_decision"}
}

// AuthFallbackRequest carries an explicit per-call choice to an enclosing
// inference wrapper. It is never a general permission for turn-level replay.
type AuthFallbackRequest struct {
	Cause error `json:"-"`
}

func (e *AuthFallbackRequest) Error() string { return "explicit inference fallback requested" }
func (e *AuthFallbackRequest) Unwrap() error { return e.Cause }

// Choices live in one turn context, never on a shared provider instance.
type authChoiceKey struct{}
type authChoices struct {
	sync.Mutex
	selected map[any]bool
}

func SelectAuthFallback(ctx context.Context, owner any) {
	if state, ok := ctx.Value(authChoiceKey{}).(*authChoices); ok {
		state.Lock()
		state.selected[owner] = true
		state.Unlock()
	}
}
func AuthFallbackSelected(ctx context.Context, owner any) bool {
	if state, ok := ctx.Value(authChoiceKey{}).(*authChoices); ok {
		state.Lock()
		defer state.Unlock()
		return state.selected[owner]
	}
	return false
}
func SelectedAuthFallbackFailure(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	switch ClassOf(err) {
	case ErrLoginRequired, ErrCredential, ErrPermission:
		return err
	}
	return &CredentialError{Class: ErrCredential, Reason: "selected fallback failed; request was not replayed", Cause: err}
}

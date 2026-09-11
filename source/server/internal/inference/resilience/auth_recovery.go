package resilience

import (
	"context"
	"errors"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// One request may retry a credential identity once after a successful login.
// Separate identities (for example a selected backup) require their own gate.
func requestAuth(ctx context.Context, err error, fallback string, safe bool, attempts map[string]bool) (llm.AuthDecision, error) {
	var credential *llm.CredentialError
	if llm.ClassOf(err) != llm.ErrLoginRequired || !errors.As(err, &credential) {
		return "", nil
	}
	key := credential.Provider + "\x00" + credential.Profile
	if attempts[key] {
		return "", nil
	}
	decision, e := llm.RequestAuthRecovery(ctx, err, fallback, safe)
	if decision != "" || e != nil {
		attempts[key] = true
	}
	return decision, e
}
func (p *Provider) chatAuth(ctx context.Context, provider inference.Provider, req inference.Call, backup bool, attempts map[string]bool) (inference.Result, error, bool) {
	for {
		result, err := provider.Chat(ctx, req)
		if err == nil {
			return result, nil, false
		}
		fallback := ""
		if backup && p.authenticationBackupAllowed(req) {
			fallback = p.authenticationBackupName()
		}
		choice, recoveryErr := requestAuth(ctx, err, fallback, true, attempts)
		if recoveryErr != nil {
			return result, recoveryErr, true
		}
		switch choice {
		case llm.AuthLogin:
			continue
		case llm.AuthFallback:
			if backup && p.authenticationBackupAllowed(req) {
				llm.SelectAuthFallback(ctx, p)
				r, e, _ := p.chatAuth(llm.WithExternalAuthFallback(ctx, ""), p.backup, p.backupRequest(req), false, attempts)
				return r, llm.SelectedAuthFallbackFailure(e), true
			}
			return result, &llm.AuthFallbackRequest{Cause: err}, true
		default:
			return result, err, false
		}
	}
}

func (p *Provider) authenticationBackupAllowed(req inference.Call) bool {
	if p.backup == nil {
		return false
	}
	caps := p.backup.Capabilities()
	if len(req.Tools) > 0 && !caps.SupportsTools {
		return false
	}
	if !caps.SupportsVision {
		for _, m := range req.Messages {
			for _, b := range m.Blocks {
				if b.Type == llm.BlockImage {
					return false
				}
			}
		}
	}
	return true
}
func incompatibleAuthFallback() error {
	return &llm.CredentialError{Class: llm.ErrCredential, Reason: "selected fallback cannot safely serve this request"}
}

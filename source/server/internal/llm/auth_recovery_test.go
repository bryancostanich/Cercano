package llm

import (
	"context"
	"errors"
	"testing"
)

func TestRecoveryChannelFailureDoesNotBecomeProviderRetry(t *testing.T) {
	ctx := WithAuthRecovery(context.Background(), func(context.Context, AuthChallenge) (AuthDecision, error) {
		return "", errors.New("recovery channel failed")
	})
	_, err := RequestAuthRecovery(ctx, &CredentialError{Class: ErrLoginRequired, Provider: "anthropic", Profile: "work", Method: AuthSubscription, Reason: CredentialExpired}, "backup", true)
	if ClassOf(err) != ErrCredential || Retryable(ClassOf(err)) || Failoverable(ClassOf(err), err) {
		t.Fatalf("recovery infrastructure error can replay/fail over: %v", err)
	}
}

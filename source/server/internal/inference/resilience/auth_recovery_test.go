package resilience

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"context"
	"testing"
)

// Until a request owner explicitly authorizes recovery, login-required failures
// must surface rather than acquiring the default transient/fallback policy.
func TestAuthRecoveryNoImplicitBackup(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		primary := &fakeProvider{name: "anthropic", outcome: []error{&llm.Error{Class: llm.ErrLoginRequired, Provider: "anthropic", Err: &llm.CredentialError{Class: llm.ErrLoginRequired, Profile: "work", Reason: llm.CredentialExpired}}}}
		backup := &fakeProvider{name: "openai"}
		p, _, sleeps := build(primary, backup)
		var err error
		if streaming {
			_, err = p.StreamChat(context.Background(), inference.Call{})
		} else {
			_, err = p.Chat(context.Background(), inference.Call{})
		}
		if llm.ClassOf(err) != llm.ErrLoginRequired || primary.calls != 1 || backup.calls != 0 || len(*sleeps) != 0 {
			t.Fatalf("streaming=%v err=%v primary=%d backup=%d sleeps=%v", streaming, err, primary.calls, backup.calls, *sleeps)
		}
	}
}

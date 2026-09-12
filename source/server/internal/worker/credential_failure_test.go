package worker

import (
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCredentialFailureRoundTrip(t *testing.T) {
	for _, class := range []llm.ErrorClass{llm.ErrLoginRequired, llm.ErrCredential, llm.ErrNetwork, llm.ErrBusy, llm.ErrPermission} {
		err := &llm.CredentialError{Class: class, Provider: "anthropic", Profile: "work", Method: llm.AuthSubscription, Reason: llm.CredentialExpired, Cause: errors.New("secret")}
		wire := marshalCredentialFailure(err, "work")
		decoded := unmarshalCredentialFailure(wire, "work")
		var got *llm.CredentialError
		if !errors.As(decoded, &got) || got.Class != class || got.Profile != "work" || got.Method != llm.AuthSubscription || strings.Contains(wire.String(), "secret") {
			t.Fatalf("lost/unsafe metadata: %v", decoded)
		}
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		if got := unmarshalCredentialFailure(marshalCredentialFailure(err, "work"), "work"); got != err {
			t.Fatal(got)
		}
	}
}
func TestCredentialResponseIsOneShotAndChecksProfile(t *testing.T) {
	c := newStreamCredentialSource(nil)
	ch := make(chan credResult, 1)
	c.pending[1] = credentialWaiter{profile: "wanted", result: ch}
	response := &proto.CredentialResponse{Id: 1, Token: "must-not-return", Failure: &proto.CredentialFailure{Class: "login_required", Provider: "anthropic", ProfileName: "wrong", Method: "subscription", Reason: "expired"}}
	c.deliver(response)
	c.deliver(response)
	got := <-ch
	if got.token != "" || llm.ClassOf(got.err) != llm.ErrCredential || len(c.pending) != 0 {
		t.Fatalf("wrong response accepted: %+v", got)
	}
}
func TestLegacyCredentialErrorFailsClosed(t *testing.T) {
	err := unmarshalCredentialFailure(nil, "work")
	if llm.ClassOf(err) != llm.ErrCredential || llm.Failoverable(llm.ClassOf(err), err) {
		t.Fatal(err)
	}
}

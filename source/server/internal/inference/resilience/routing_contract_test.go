package resilience

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"context"
	"errors"
	"testing"
)

func TestRoutingContractAuthenticationUsesBackupWithoutRetry(t *testing.T) {
	primary := &fakeProvider{name: "primary", outcome: []error{&llm.Error{Class: llm.ErrAuth, StatusCode: 401, Err: errors.New("fixture auth error")}}}
	backup := &fakeProvider{name: "backup"}
	p, _, slept := build(primary, backup)
	_, err := p.Chat(context.Background(), inference.Call{Model: "foreign", Tier: "most_capable"})
	if err != nil {
		t.Fatal(err)
	}
	if primary.calls != 1 || backup.calls != 1 || len(*slept) != 0 {
		t.Fatalf("calls=%d/%d sleeps=%v", primary.calls, backup.calls, *slept)
	}
	if backup.models[0] != "backup-most_capable" {
		t.Fatalf("foreign model leaked: %v", backup.models)
	}
}

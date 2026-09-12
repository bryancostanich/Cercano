package worker

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	cfg "cercano/source/server/pkg/config"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type failedStaticCredentialFetcher struct{}

func (*failedStaticCredentialFetcher) Fetch(_ context.Context, name string) (string, string, error) {
	if name == "named-di" {
		return "", "", errors.New("private-store-payload")
	}
	return "fixture-key", "", nil
}
func TestWorkerDeepInfraStoreFailureDoesNotBecomeAnonymousOrFallback(t *testing.T) {
	var requests atomic.Int64
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer endpoint.Close()
	c := cfg.Defaults()
	c.LocusMode = "cloud_only"
	c.ActiveCloudProfile = "named-di"
	c.BackupCloudProfile = "backup"
	c.CloudProfiles = []cfg.CloudProfile{{Name: "named-di", Provider: "deepinfra", Backend: "openai", Flavor: "chat_completions", BaseURL: endpoint.URL + "/primary"}, {Name: "backup", Provider: "deepinfra", Backend: "openai", Flavor: "chat_completions", BaseURL: endpoint.URL + "/backup"}}
	providers, err := buildWorkerProviders(context.Background(), c, &failedStaticCredentialFetcher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := llm.WithAuthRecovery(context.Background(), func(context.Context, llm.AuthChallenge) (llm.AuthDecision, error) {
		t.Error("API-key store failure requested subscription login")
		return llm.AuthCancel, nil
	})
	_, err = providers.Cloud().Chat(ctx, inference.Call{Tier: "most_capable"})
	var credential *llm.CredentialError
	if llm.ClassOf(err) != llm.ErrCredential || !errors.As(err, &credential) || credential.Profile != "named-di" || credential.Method != "api_key" || requests.Load() != 0 || strings.Contains(err.Error(), "private-store-payload") {
		t.Fatalf("unsafe store-failure handling: requests=%d error=%v", requests.Load(), err)
	}
}

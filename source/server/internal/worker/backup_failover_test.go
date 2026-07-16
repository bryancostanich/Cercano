package worker

// Backup-profile failover parity (Task A1).
//
// The in-process path wraps every cloud primary in the resilience engine via
// providers.wrapResilience → resilience.New. buildWorkerProviders must do the
// same in the worker, sourcing BOTH credentials via the stream credential
// proxy. These tests prove:
//
//  1. With a backup profile configured, the worker's resolved cloud provider IS
//     a *resilience.Provider wrapping active+backup (structural parity with
//     in-process), and BOTH credentials are fetched via the proxy (keyed by the
//     active + backup profile names).
//  2. A primary failure fails over to the backup: a 5xx from the primary (busy
//     class: one same-provider retry, then failover) yields the backup's
//     output — behavior parity with in-process.
//  3. No backup configured → the provider is still engine-wrapped (busy retry
//     + narration apply without a backup), but no backup credential is fetched
//     and the primary serves.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/internal/llm"
	pkgcfg "cercano/source/server/pkg/config"
)

// fakeCredFetcher records every Fetch and returns a per-profile canned token.
type fakeCredFetcher struct {
	mu      sync.Mutex
	tokens  map[string]string // profileName → token
	fetched []string          // ordered profile names fetched
}

func (f *fakeCredFetcher) Fetch(_ context.Context, profileName string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetched = append(f.fetched, profileName)
	return f.tokens[profileName], "", nil
}

func (f *fakeCredFetcher) sawFetch(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.fetched {
		if n == name {
			return true
		}
	}
	return false
}

// chatCompletionBody is a minimal, valid non-streaming chat completion JSON.
func chatCompletionBody(content string) string {
	return `{"id":"cmpl-1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"` + content + `"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
}

func TestWorkerBackupFailover_WrapsCompositeAndFailsOver(t *testing.T) {
	const backupText = "served by backup"

	// Primary server always 500s → triggers failover.
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":{"message":"primary down","type":"server_error"}}`)
	}))
	defer primarySrv.Close()

	// Backup server returns a valid completion.
	backupSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, chatCompletionBody(backupText))
	}))
	defer backupSrv.Close()

	cfg := pkgcfg.Config{
		LocusMode:          "cloud_primary",
		ActiveCloudProfile: "primary",
		BackupCloudProfile: "backup",
		CloudProfiles: []pkgcfg.CloudProfile{
			{Name: "primary", Flavor: cloudfactory.FlavorChatCompletions, Backend: "openai", BaseURL: primarySrv.URL, Model: "primary-model"},
			{Name: "backup", Flavor: cloudfactory.FlavorChatCompletions, Backend: "openai", BaseURL: backupSrv.URL, Model: "backup-model"},
		},
	}
	creds := &fakeCredFetcher{tokens: map[string]string{"primary": "key-primary", "backup": "key-backup"}}

	resolver, err := buildWorkerProviders(context.Background(), cfg, creds, nil)
	if err != nil {
		t.Fatalf("buildWorkerProviders: %v", err)
	}

	prov, isCloud, _, err := resolver.Main()
	if err != nil {
		t.Fatalf("Main: %v", err)
	}
	if !isCloud {
		t.Fatal("expected the cloud provider to be selected under cloud_primary")
	}

	// Structural parity: the resolved provider must be the resilience engine.
	if _, ok := prov.(*resilience.Provider); !ok {
		t.Fatalf("resolved provider is %T, want *resilience.Provider (worker did not wrap active+backup)", prov)
	}

	// Both credentials must have been fetched via the proxy (active during the
	// primary build, backup during the wrap).
	if !creds.sawFetch("primary") {
		t.Error("active (primary) credential not fetched via the proxy")
	}
	if !creds.sawFetch("backup") {
		t.Error("backup credential not fetched via the proxy")
	}

	// Behavior parity: primary 500 → fail over → backup's content.
	resp, err := prov.Chat(context.Background(), llm.ChatRequest{
		Model:    "primary-model",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("Chat (should have failed over to backup): %v", err)
	}
	var got string
	for _, b := range resp.Blocks {
		if b.Type == llm.BlockText {
			got += b.Text
		}
	}
	if got != backupText {
		t.Errorf("failover output = %q, want %q (backup should have served the request)", got, backupText)
	}
}

func TestWorkerBackupFailover_NoBackupIsBareProvider(t *testing.T) {
	primarySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, chatCompletionBody("primary"))
	}))
	defer primarySrv.Close()

	cfg := pkgcfg.Config{
		LocusMode:          "cloud_primary",
		ActiveCloudProfile: "primary",
		// No BackupCloudProfile.
		CloudProfiles: []pkgcfg.CloudProfile{
			{Name: "primary", Flavor: cloudfactory.FlavorChatCompletions, Backend: "openai", BaseURL: primarySrv.URL, Model: "primary-model"},
		},
	}
	creds := &fakeCredFetcher{tokens: map[string]string{"primary": "key-primary"}}

	resolver, err := buildWorkerProviders(context.Background(), cfg, creds, nil)
	if err != nil {
		t.Fatalf("buildWorkerProviders: %v", err)
	}
	prov, _, _, err := resolver.Main()
	if err != nil {
		t.Fatalf("Main: %v", err)
	}
	// The engine wraps even without a backup — retry policy and narration are
	// not conditional on failover being available.
	if _, ok := prov.(*resilience.Provider); !ok {
		t.Fatalf("resolved provider is %T, want *resilience.Provider even without a backup", prov)
	}
	if creds.sawFetch("backup") {
		t.Error("backup credential fetched but no backup configured")
	}
	// And it serves from the primary.
	resp, err := prov.Chat(context.Background(), llm.ChatRequest{
		Model:    "primary-model",
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.Blocks) == 0 || resp.Blocks[0].Text != "primary" {
		t.Errorf("resp = %+v, want the primary's content", resp)
	}
}

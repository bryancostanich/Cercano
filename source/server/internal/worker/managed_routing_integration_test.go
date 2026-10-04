package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/managedrouting"
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/managedsettings/settingstest"
	"cercano/source/server/internal/modelmetadata"
	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/pkg/config"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

// Exercise the real worker provider assembly, credential binding, managed plan,
// resilience chain and OpenAI HTTP transport. No worker log files or real keys
// are needed. The separate stream integration tests cover host authorization RPC.
func TestManagedWorkerProviderAssemblyUsesOnlyAdministratorFallbacks(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprintf("revoke_before_fallback=%v", revoke), func(t *testing.T) {
			var mu sync.Mutex
			var calls []string
			var revoked atomic.Bool
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					http.Error(w, "bad body", 400)
					return
				}
				name := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
				mu.Lock()
				calls = append(calls, name+":"+body.Model)
				mu.Unlock()
				if r.Header.Get("Authorization") != "Bearer fixture-"+name {
					t.Errorf("wrong credential for %s", name)
				}
				if name == "managed" {
					revoked.Store(revoke)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(429)
					fmt.Fprint(w, `{"error":{"message":"fixture quota exhausted","code":"insufficient_quota"}}`)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id":"fixture","model":%q,"choices":[{"message":{"role":"assistant","content":"approved fallback"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":3}}`, body.Model)
			}))
			defer srv.Close()
			// Sequential test: only trust this disposable server while building clients.
			previous := http.DefaultTransport
			http.DefaultTransport = srv.Client().Transport
			defer func() { http.DefaultTransport = previous }()
			cfg := config.Defaults()
			cfg.ActiveCloudProfile = "personal"
			cfg.BackupCloudProfile = "forbidden"
			cfg.SecondaryCloudProfile = "personal"
			credentials := &fakeCredFetcher{tokens: map[string]string{}}
			for _, name := range []string{"personal", "managed", "fallback", "forbidden"} {
				cfg.CloudProfiles = append(cfg.CloudProfiles, config.CloudProfile{Name: name, Flavor: "chat_completions", BaseURL: srv.URL + "/" + name + "/v1", Model: "personal-model"})
				credentials.tokens[name] = "fixture-" + name
			}
			snapshot := settingstest.Snapshot("company-a", "1", "Review carefully.")
			snapshot.Policy.AllowedRoutes = []v1.Route{
				{ID: "managed", Provider: "openai", Endpoint: srv.URL + "/managed/v1", Model: "gpt-4o", Placement: "external"},
				{ID: "fallback", Provider: "openai", Endpoint: srv.URL + "/fallback/v1", Model: "gpt-4o-mini", Placement: "external"},
			}
			snapshot.Policy.TaskDefaults[0].RouteID = "managed"
			snapshot.Policy.TaskDefaults[0].FallbackRouteIDs = []string{"fallback"}
			ctx := managedsettings.WithSnapshot(t.Context(), snapshot)
			// Prove the fixture could cross the real validated worker settings boundary.
			encoded, err := managedsettings.Encode(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = managedsettings.Decode(encoded); err != nil {
				t.Fatal(err)
			}
			ctx = modelpolicy.WithAuthority(ctx, modelpolicy.AuthorizeFunc(func(_ context.Context, a modelpolicy.Attempt) error {
				if revoked.Load() || !modelpolicy.Allowed(snapshot.Policy, a) {
					return modelpolicy.Deny(a, "fixture membership revoked or route denied")
				}
				return nil
			}))
			resolver, err := buildWorkerProviders(ctx, cfg, credentials, nil, nil, func(config.CloudProfile, string) modelmetadata.Evidence {
				return modelmetadata.Evidence{ContextWindow: 128000}
			})
			if err != nil {
				t.Fatal(err)
			}
			sel, _, _, managed, err := managedrouting.Select(ctx, config.TaskChat, resolver.Candidates(), managedrouting.Request{})
			if err != nil || !managed {
				t.Fatalf("managed=%v err=%v", managed, err)
			}
			target := inference.TargetFor(sel.Provider, "personal-model", "most_capable")
			if target.Profile != "managed" || target.Model != "gpt-4o" || !target.ContextWindowKnown || target.ContextWindow != 128000 {
				t.Fatalf("managed target lost metadata: %+v", target)
			}
			result, err := sel.Provider.Chat(ctx, inference.Call{Model: "personal-model", Tier: "most_capable"})
			want := []string{"managed:gpt-4o"}
			if revoke {
				if !modelpolicy.IsDenial(err) {
					t.Fatalf("revocation did not stop fallback: %v", err)
				}
			} else {
				want = append(want, "fallback:gpt-4o-mini")
				if err != nil || result.Model != "gpt-4o-mini" || result.Route == nil || result.Route.Profile != "fallback" {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("requests=%v want=%v", calls, want)
			}
		})
	}
}

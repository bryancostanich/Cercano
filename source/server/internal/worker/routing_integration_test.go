package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/modelmetadata"
	"cercano/source/server/internal/routingwire"
	cfg "cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	pb "google.golang.org/protobuf/proto"
)

// Exercise persisted settings, protobuf snapshots, real worker provider assembly,
// credentials, and HTTP inference adapters without contacting a hosted service.
func TestRoutingSettingsSnapshotWorkerHTTP(t *testing.T) {
	for _, primaryBackup := range []bool{false, true} {
		for _, secondaryBackup := range []bool{false, true} {
			t.Run(fmt.Sprintf("backups=%t,%t", primaryBackup, secondaryBackup), func(t *testing.T) {
				var mu sync.Mutex
				requests := map[string][]string{}
				c := cfg.Defaults()
				c.CloudProfiles = nil
				c.OpenRuntime = "llama_server"
				c.LocusMode = "cloud_only"
				credentials := map[string]string{}
				windows := map[string]int{"p": 16384, "pb": 65536, "s": 131072, "sb": 32768}
				for _, name := range []string{"p", "pb", "s", "sb"} {
					name := name
					credentials[name] = "fixture-" + name
					endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if got := r.Header.Get("Authorization"); got != "Bearer fixture-"+name {
							t.Errorf("%s received another profile credential: %q", name, got)
						}
						var request struct {
							Model string `json:"model"`
						}
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							t.Error(err)
							w.WriteHeader(400)
							return
						}
						mu.Lock()
						requests[name] = append(requests[name], request.Model)
						mu.Unlock()
						w.Header().Set("Content-Type", "application/json")
						if name == "p" || name == "s" {
							w.WriteHeader(401)
							fmt.Fprint(w, `{"error":{"message":"fixture auth failure","type":"authentication_error"}}`)
							return
						}
						json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "model": request.Model, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 4, "completion_tokens": 2}})
					}))
					defer endpoint.Close()
					profile := cfg.CloudProfile{Name: name, Flavor: "chat_completions", Backend: "openai", Provider: "openai", BaseURL: endpoint.URL + "/v1", Region: "kept-region", AWSProfile: "kept-aws"}
					if err := routingwire.ApplyChoices(&profile, &proto.ProfileModelChoices{TierOverrides: map[string]string{"premium": "same-id", "standard": "same-standard", "economy": "same-economy"}, ImageModel: "image-" + name}); err != nil {
						t.Fatal(err)
					}
					c.CloudProfiles = append(c.CloudProfiles, profile)
				}
				assignments := &proto.RoutingAssignments{Primary: "p", Secondary: "s", Tasks: map[string]*proto.TaskModelAssignment{"dispatch": {Destination: "secondary", Quality: "standard"}}}
				if primaryBackup {
					assignments.PrimaryBackup = "pb"
				}
				if secondaryBackup {
					assignments.SecondaryBackup = "sb"
				}
				routingwire.ApplyAssignments(&c, assignments)
				if err := c.ValidateRouting(); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "config.yaml")
				if err := cfg.Save(c, path); err != nil {
					t.Fatal(err)
				}
				persisted, err := cfg.Load(path)
				if err != nil {
					t.Fatal(err)
				}
				wire := SnapshotConfig(persisted, "", map[string]string{string(cfg.TierVision): "local-image"})
				encoded, err := pb.Marshal(wire)
				if err != nil {
					t.Fatal(err)
				}
				var decoded proto.ConfigSnapshot
				if err := pb.Unmarshal(encoded, &decoded); err != nil {
					t.Fatal(err)
				}
				restored := ConfigFromSnapshot(&decoded)
				if openTierModel(restored, cfg.TierVision) != "local-image" {
					t.Fatal("Local image lost")
				}
				resolver, err := buildWorkerProviders(context.Background(), restored, &fakeCredFetcher{tokens: credentials}, nil, nil, func(p cfg.CloudProfile, model string) modelmetadata.Evidence {
					return modelmetadata.Evidence{ContextWindow: windows[p.Name]}
				})
				if err != nil {
					t.Fatal(err)
				}
				main, isCloud, _, err := resolver.Main()
				if err != nil || !isCloud {
					t.Fatalf("main selection: %v", err)
				}
				response, err := main.Chat(context.Background(), inference.Call{Model: resolver.MainModel(true), Tier: "most_capable"})
				if (err == nil) != primaryBackup {
					t.Fatalf("Primary availability: %v", err)
				}
				if primaryBackup && (response.Route == nil || response.Route.Profile != "pb" || response.Route.ContextWindow != 65536 || response.Model != "same-id") {
					t.Fatalf("Primary attribution: %+v", response)
				}
				e := dispatch.NewEngine(resolver.Candidates, func() locus.Mode { return locus.CloudOnly }, nil)
				e.SetTaskAssignment(restored.TaskAssignment)
				e.SetDestinationModelFor(func(sel inference.Selection, tier cfg.Tier) string {
					p, _ := restored.Profile(sel.Profile)
					return restored.ModelProfiles.ResolveCloudModelForTier(p, tier)
				})
				target, err := e.Target(dispatch.Spec{RoutingTask: cfg.TaskDispatch})
				if err != nil || target.Profile != "s" || target.Model != "same-standard" || target.ContextWindow != 131072 {
					t.Fatalf("budget target=%+v err=%v", target, err)
				}
				result, err := e.Dispatch(context.Background(), dispatch.Spec{RoutingTask: cfg.TaskDispatch})
				if (err == nil) != secondaryBackup {
					t.Fatalf("Secondary availability: %v", err)
				}
				if secondaryBackup && (result.Profile != "sb" || result.Destination != "secondary" || result.ContextWindow != 32768 || result.Model != "same-standard") {
					t.Fatalf("Secondary attribution: %+v", result)
				}
				mu.Lock()
				defer mu.Unlock()
				for _, name := range []string{"p", "s"} {
					if len(requests[name]) != 1 {
						t.Fatalf("unexpected retry/crossing for %s: %v", name, requests)
					}
				}
				for name, enabled := range map[string]bool{"pb": primaryBackup, "sb": secondaryBackup} {
					want := 0
					if enabled {
						want = 1
					}
					if len(requests[name]) != want {
						t.Fatalf("%s requests=%v", name, requests[name])
					}
				}
			})
		}
	}
}

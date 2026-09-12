package worker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
)

func TestDestinationRedirectWorkerExecution(t *testing.T) {
	for _, destination := range []config.Destination{config.DestinationPrimary, config.DestinationSecondary} {
		t.Run(string(destination), func(t *testing.T) {
			calls := []string{}
			endpoint := func(name string, fail bool) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls = append(calls, name)
					if r.Header.Get("Authorization") != "Bearer key-"+name {
						t.Errorf("wrong credential for %s: %q", name, r.Header.Get("Authorization"))
					}
					var body struct {
						Model string `json:"model"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Model != name+"-standard" {
						t.Errorf("model=%q want %s-standard", body.Model, name)
					}
					w.Header().Set("Content-Type", "application/json")
					if fail {
						w.WriteHeader(http.StatusUnauthorized)
						io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
						return
					}
					io.WriteString(w, chatCompletionBody("final backup"))
				}))
			}
			preferred := endpoint("final", true)
			defer preferred.Close()
			backup := endpoint("backup", false)
			defer backup.Close()
			unused := endpoint("source", false)
			defer unused.Close()
			profile := func(name, url string) config.CloudProfile {
				return config.CloudProfile{Name: name, Flavor: "chat_completions", BaseURL: url, TierOverrides: map[config.CostTier]string{config.CostStandard: name + "-standard"}}
			}
			c := config.Config{LocusMode: "cloud_only", LocalRedirect: destination, ActiveCloudProfile: "source", SecondaryCloudProfile: "source", CloudProfiles: []config.CloudProfile{profile("source", unused.URL), profile("final", preferred.URL), profile("backup", backup.URL)}, TaskAssignments: map[config.Task]config.TaskAssignment{config.TaskChat: {Destination: config.DestinationLocal, Quality: config.CostStandard}}}
			if destination == config.DestinationPrimary {
				c.ActiveCloudProfile = "final"
				c.BackupCloudProfile = "backup"
			} else {
				c.SecondaryCloudProfile = "final"
				c.SecondaryBackupCloudProfile = "backup"
			}
			c = ConfigFromSnapshot(SnapshotConfig(c, "", nil))
			creds := &fakeCredFetcher{tokens: map[string]string{"source": "key-source", "final": "key-final", "backup": "key-backup"}}
			resolver, err := buildWorkerProviders(context.Background(), c, creds, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			prov, cloud, _, err := resolver.Main()
			if err != nil || !cloud {
				t.Fatalf("cloud=%v error=%v", cloud, err)
			}
			model, ok := inference.TaskModelFor(prov)
			if !ok || model != "final-standard" {
				t.Fatalf("selected model=%q", model)
			}
			a, ok := inference.TaskAssignmentFor(prov, config.TaskChat)
			if !ok || a != c.TaskAssignment(config.TaskChat) {
				t.Fatalf("original assignment lost: %+v", a)
			}
			_, err = prov.Chat(context.Background(), llm.ChatRequest{Model: model, Tier: string(config.TierEveryday), Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "fixture"}}}}})
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 2 || calls[0] != "final" || calls[1] != "backup" {
				t.Fatalf("calls=%v", calls)
			}
			// API-key profiles are built eagerly for both destinations. Selection
			// must not SEND requests with source credentials; the endpoint checks
			// above establish that without changing credential-loading policy.
			if !creds.sawFetch("final") || !creds.sawFetch("backup") {
				t.Fatal("missing final destination credentials")
			}
		})
	}
}

package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	toolssvc "cercano/source/server/internal/hostsvc/tools"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/reasoningexperiment"
	"cercano/source/server/pkg/config"
)

func TestWorkerReasoningDiagnosticAuthenticatedNoFallback(t *testing.T) {
	var calls, backupCalls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer selected-key" {
			t.Error("wrong credential")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "pinned" {
			t.Error("model changed")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer endpoint.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { backupCalls.Add(1) }))
	defer backup.Close()
	c := config.Defaults()
	c.ActiveCloudProfile = "selected"
	c.BackupCloudProfile = "backup"
	c.CloudProfiles = []config.CloudProfile{{Name: "selected", Flavor: "chat_completions", BaseURL: endpoint.URL + "/v1"}, {Name: "backup", Flavor: "chat_completions", BaseURL: backup.URL + "/v1"}}
	c = ConfigFromSnapshot(SnapshotConfig(c, "", nil))
	creds := &fakeCredFetcher{tokens: map[string]string{"selected": "selected-key", "backup": "backup-key"}}
	resolver, err := buildWorkerProviders(context.Background(), c, creds, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0.0
	toolSvc := buildWorkerToolSvcWithDiagnostic(nil, nil, nil, resolver.Cloud(), resolver.Open(), c, nil, nil, nil, nil, resolver.(reasoningexperiment.Service))
	run := toolSvc.(toolssvc.Catalog).CapRegistry().Services().ReasoningDiagnostic
	if run == nil {
		t.Fatal("worker capability stack dropped diagnostic service")
	}
	report, err := run(context.Background(), reasoningexperiment.Spec{Profile: "selected", Model: "pinned", Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "fixture"}}}}, MaxRequests: 2, MaxTokens: 128, TimeoutSeconds: 5, Temperature: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || backupCalls.Load() != 0 || !creds.sawFetch("selected") {
		t.Fatalf("calls=%d backup=%d", calls.Load(), backupCalls.Load())
	}
	for _, a := range report.Arms {
		if a.Status != "terminal_stop" || len(a.Steps) != 1 {
			t.Fatalf("%+v", a)
		}
	}
}

package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/reasoningexperiment"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
)

func TestReasoningDiagnosticUsesSelectedProfileWithoutFallback(t *testing.T) {
	var calls, backupCalls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer selected-key" {
			t.Error("wrong credential")
		}
		w.WriteHeader(401)
		fmt.Fprint(w, "private failure")
	}))
	defer endpoint.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { backupCalls.Add(1) }))
	defer backup.Close()
	c := config.Defaults()
	c.ActiveCloudProfile = "selected"
	c.BackupCloudProfile = "backup"
	c.CloudProfiles = []config.CloudProfile{{Name: "selected", Flavor: "chat_completions", BaseURL: endpoint.URL + "/v1"}, {Name: "backup", Flavor: "chat_completions", BaseURL: backup.URL + "/v1"}}
	st := secrets.NewMemory()
	st.Set("selected", "selected-key")
	st.Set("backup", "backup-key")
	svc := New(cfgsvc.New("", c, st), nil, nil, nil, nil, nil, nil)
	zero := 0.0
	report, err := svc.(reasoningexperiment.Service).RunReasoningDiagnostic(context.Background(), reasoningexperiment.Spec{Profile: "selected", Model: "pinned", Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "fixture"}}}}, MaxRequests: 2, MaxTokens: 128, TimeoutSeconds: 5, Temperature: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || backupCalls.Load() != 0 || len(report.Arms) != 2 {
		t.Fatalf("calls=%d backup=%d report=%+v", calls.Load(), backupCalls.Load(), report)
	}
	for _, a := range report.Arms {
		if a.Status != "provider_or_capture_error" {
			t.Fatalf("%+v", a)
		}
	}
}

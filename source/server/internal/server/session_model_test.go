package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cercano/source/server/internal/chatroute"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

type sessionModelRunner struct{ requests []runner.Request }

func (r *sessionModelRunner) RunTurn(_ context.Context, req runner.Request, _ runner.EventSink, _ runner.PermissionRequester, _ runner.PersistFunc) (runner.Result, error) {
	r.requests = append(r.requests, req)
	return runner.Result{FinalText: "ok"}, nil
}
func TestSessionModelRPCAndTurnSnapshot(t *testing.T) {
	srv, _ := newServerWithStore(t)
	cfg := config.Defaults()
	cfg.CloudProfiles = []config.CloudProfile{{Name: "deepinfra", Provider: "deepinfra", Flavor: "chat_completions", Model: "default-model", BaseURL: "https://example.invalid/v1"}}
	srv.SetConfigPersistence("", cfg)
	keys := secrets.NewMemory()
	if err := keys.Set("deepinfra", "fixture-key"); err != nil {
		t.Fatal(err)
	}
	srv.SetSecrets(keys)
	pinned, err := srv.SessionModel(t.Context(), &proto.SessionModelRequest{ConversationId: "one", Action: "set", Profile: "deepinfra", Model: "exact-model"})
	if err != nil {
		t.Fatal(err)
	}
	var state chatroute.Status
	if err := json.Unmarshal(pinned.ResultJson, &state); err != nil {
		t.Fatal(err)
	}
	if state.Override == nil || state.Override.Model != "exact-model" {
		t.Fatal("override not returned", state)
	}
	for _, req := range []*proto.SessionModelRequest{
		{ConversationId: "one", Action: "set", Profile: "missing", Model: "bad"},
		{ConversationId: "one", Action: "set", Profile: "deepinfra"},
		{ConversationId: "one", Action: "bogus"},
	} {
		if _, err := srv.SessionModel(t.Context(), req); err == nil {
			t.Fatal("invalid request accepted", req)
		}
	}
	record := &sessionModelRunner{}
	srv.inProcessRunner = record
	for _, id := range []string{"one", "two"} {
		if err := srv.streamProcessRequestWithToolLoop(&proto.ProcessRequestRequest{ConversationId: id, Input: "hello"}, &fakeStream{ctx: t.Context()}); err != nil {
			t.Fatal(err)
		}
	}
	if len(record.requests) != 2 || record.requests[0].ChatRoute == nil || record.requests[0].ChatRoute.Model != "exact-model" || record.requests[1].ChatRoute != nil {
		t.Fatalf("wrong turn snapshots: %+v", record.requests)
	}
	if _, err := srv.SessionModel(t.Context(), &proto.SessionModelRequest{ConversationId: "one", Action: "clear"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.streamProcessRequestWithToolLoop(&proto.ProcessRequestRequest{ConversationId: "one", Input: "normal"}, &fakeStream{ctx: t.Context()}); err != nil {
		t.Fatal(err)
	}
	if record.requests[2].ChatRoute != nil {
		t.Fatal("clear did not reach next turn")
	}
	if srv.cfgSvc.Get().CloudProfiles[0].Model != "default-model" {
		t.Fatal("global model mutated")
	}
}
func TestSessionModelUnavailablePersistenceDoesNotPanic(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)
	if _, err := srv.SessionModel(t.Context(), &proto.SessionModelRequest{ConversationId: "one", Action: "set", Profile: "deepinfra", Model: "model"}); err == nil {
		t.Fatal("missing persistence accepted")
	}
}

func TestSessionModelCatalogDiscoveryIsReadOnly(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"exact-provider-id","display_name":"Friendly Model"}]}`))
	}))
	defer api.Close()
	srv, store := newServerWithStore(t)
	cfg := config.Defaults()
	cfg.CloudProfiles = []config.CloudProfile{{Name: "saved", Flavor: "messages", BaseURL: api.URL}}
	srv.SetConfigPersistence("", cfg)
	result, err := srv.sessionModel(t.Context(), "new-session", chatroute.Request{Action: "models", Profile: "saved"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 1 || result.Models[0].ID != "exact-provider-id" {
		t.Fatalf("catalog result: %+v", result)
	}
	if route, err := store.(chatroute.Store).ChatRoute(t.Context(), "new-session"); err != nil || route != nil {
		t.Fatal("catalog changed routing", route, err)
	}
}

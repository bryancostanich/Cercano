package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/chatroute"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/hostsvc/permissions"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestSessionModelWorkerNextTurnAndToolControl(t *testing.T) {
	var mu sync.Mutex
	var models, keys []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		models = append(models, req.Model)
		keys = append(keys, r.Header.Get("Authorization"))
		first := len(models) == 1
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if first {
			args := `{"action":"set","profile":"deepinfra","model":"exact-model"}`
			event := map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "switch-model", "type": "function", "function": map[string]any{"name": "session_model", "arguments": args}}}}, "finish_reason": "tool_calls"}}}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer api.Close()
	cfg := config.Defaults()
	cfg.LocusMode = "cloud_only"
	cfg.ActiveCloudProfile = "normal"
	cfg.BackupCloudProfile = ""
	cfg.CloudProfiles = []config.CloudProfile{
		{Name: "normal", TierOverrides: map[config.CostTier]string{config.CostEconomy: "normal-model", config.CostStandard: "normal-model", config.CostPremium: "normal-model"}, Flavor: "chat_completions", Model: "normal-model", BaseURL: api.URL},
		{Name: "deepinfra", Flavor: "chat_completions", Model: "saved-default", BaseURL: api.URL},
	}
	secret := secrets.NewMemory()
	_ = secret.Set("normal", "normal-key")
	_ = secret.Set("deepinfra", "deepinfra-key")
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	proto.RegisterWorkerServer(server, New())
	go server.Serve(listener)
	defer server.Stop()
	dial := func(ctx context.Context) (*grpc.ClientConn, error) {
		return grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	host := newWorkerRunnerWithDial(nil, cfgsvc.New("", cfg, secret), permissions.New(nil, nil, nil), dial)
	var override *chatroute.Route
	host.SetSessionModel(func(_ context.Context, convID string, req chatroute.Request) (chatroute.Status, error) {
		if convID != "one" || req.Action != "set" {
			return chatroute.Status{}, fmt.Errorf("wrong scope or action: %s %+v", convID, req)
		}
		override = &chatroute.Route{Profile: req.Profile, Model: req.Model}
		return chatroute.Status{Override: override}, nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	confirmations := 0
	requester := func(_ context.Context, _ string, name string, _ json.RawMessage, tier llm.Permission, _ bool) (bool, error) {
		confirmations++
		if name != "session_model" || tier != llm.PermX {
			t.Errorf("wrong permission: %s %s", name, tier)
		}
		return true, nil
	}
	run := func(req runner.Request) {
		t.Helper()
		if _, err := host.RunTurn(ctx, req, nil, requester, nil); err != nil {
			t.Fatal(err)
		}
	}
	run(runner.Request{ConversationID: "one", Input: "Use exact-model on deepinfra next message", WorkDir: t.TempDir()})
	if override == nil || confirmations != 1 {
		t.Fatalf("tool did not reach host: override=%+v confirmations=%d", override, confirmations)
	}
	run(runner.Request{ConversationID: "one", Input: "hello", WorkDir: t.TempDir(), ChatRoute: override})
	run(runner.Request{ConversationID: "other", Input: "hello", WorkDir: t.TempDir()})
	mu.Lock()
	defer mu.Unlock()
	wantModels := []string{"normal-model", "normal-model", "exact-model", "normal-model"}
	wantKeys := []string{"Bearer normal-key", "Bearer normal-key", "Bearer deepinfra-key", "Bearer normal-key"}
	if fmt.Sprint(models) != fmt.Sprint(wantModels) || fmt.Sprint(keys) != fmt.Sprint(wantKeys) {
		t.Fatalf("models=%v keys=%v", models, keys)
	}
}

func TestSessionModelWorkerScopeAndCancellation(t *testing.T) {
	sender := &sender{ch: make(chan *proto.WorkerToHost, 1)}
	control := newStreamSessionModel(sender, "one")
	if _, err := control.Control(t.Context(), "other", chatroute.Request{Action: "clear"}); err == nil {
		t.Fatal("cross-conversation mutation accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := control.Control(ctx, "one", chatroute.Request{Action: "clear"}); err == nil {
		t.Fatal("cancel ignored")
	}
	if len(sender.ch) != 0 {
		t.Fatal("invalid request sent")
	}
}

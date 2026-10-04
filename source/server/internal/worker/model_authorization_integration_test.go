package worker_test

import (
	"cercano/source/server/internal/managedsettings"
	"cercano/source/server/internal/managedsettings/settingstest"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/agenttools"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	providers "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/openai"
	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/internal/worker"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

// The real OpenAI adapter in the worker talks to a local HTTP provider. The
// host's authority is carried only in its context, so every decision must cross
// the gRPC stream; sharing a process-global authority would hide a broken proxy.
func TestManagedWorkerAuthorizesEachPhysicalAttempt(t *testing.T) {
	for _, scenario := range []string{"allowed", "denied", "revoked_before_retry"} {
		t.Run(scenario, func(t *testing.T) {
			var hits, checks atomic.Int32
			var revoked atomic.Bool
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if scenario == "revoked_before_retry" {
					revoked.Store(true)
					http.Error(w, `{"error":{"message":"temporarily unavailable"}}`, http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"approved reply\"}}]}\n\ndata: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer provider.Close()
			adapter := openai.NewClient(openai.Config{APIKey: "test", BaseURL: provider.URL + "/v1"})
			ws := worker.NewWithFactories(func(start *proto.StartTurn) (providers.Resolver, error) {
				if !start.GetEnterpriseManaged() {
					t.Error("managed scope missing")
				}
				return &echoResolver{prov: resilience.New(adapter, resilience.Options{RetryWait: time.Millisecond, RetryWaitCap: time.Millisecond})}, nil
			}, func(*proto.StartTurn) (runner.ToolSvc, error) {
				return &hostTestToolSvc{reg: agenttools.NewRegistry()}, nil
			})
			listener := bufconn.Listen(1 << 20)
			server := grpc.NewServer()
			proto.RegisterWorkerServer(server, ws)
			go server.Serve(listener)
			defer server.Stop()
			store := secrets.NewMemory()
			host := worker.NewWorkerRunnerForTest(&fakeHistory{}, cfgsvc.New("", config.Defaults(), store), newTestBroker(), store, worker.BufconnDial(listener))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx = managedsettings.WithSnapshot(ctx, settingstest.Snapshot("test-org", "1", "Review carefully."))
			ctx = modelpolicy.WithAuthority(ctx, modelpolicy.AuthorizeFunc(func(_ context.Context, a modelpolicy.Attempt) error {
				checks.Add(1)
				if a.Provider != "openai" || a.Endpoint != provider.URL+"/v1" || a.Placement != "external" || a.Model != "approved" {
					t.Errorf("wrong request identity: %+v", a)
					return modelpolicy.Deny(a, "wrong route")
				}
				if scenario == "denied" || revoked.Load() {
					return modelpolicy.Deny(a, "not approved")
				}
				return nil
			}))
			result, err := host.RunTurn(ctx, runner.Request{ConversationID: "managed", Gen: 1, Input: "hello"}, nil, nil, nil)
			switch scenario {
			case "allowed":
				if err != nil || result.FinalText != "approved reply" || hits.Load() != 1 || checks.Load() != 1 {
					t.Fatalf("result=%+v err=%v hits=%d checks=%d", result, err, hits.Load(), checks.Load())
				}
			case "denied":
				if llm.ClassOf(err) != llm.ErrPermission || !strings.Contains(err.Error(), "enterprise policy blocked") || hits.Load() != 0 || checks.Load() != 1 {
					t.Fatalf("err=%v hits=%d checks=%d", err, hits.Load(), checks.Load())
				}
			case "revoked_before_retry":
				if llm.ClassOf(err) != llm.ErrPermission || !strings.Contains(err.Error(), "enterprise policy blocked") || hits.Load() != 1 || checks.Load() != 2 {
					t.Fatalf("err=%v hits=%d checks=%d", err, hits.Load(), checks.Load())
				}
			}
		})
	}
}

func TestManagedHostDoesNotDowngradeToLegacyWorker(t *testing.T) {
	old := &oldAuthenticationWorker{}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	proto.RegisterWorkerServer(server, old)
	go server.Serve(listener)
	defer server.Stop()
	store := secrets.NewMemory()
	host := worker.NewWorkerRunnerForTest(&fakeHistory{}, cfgsvc.New("", config.Defaults(), store), newTestBroker(), store, worker.BufconnDial(listener))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = managedsettings.WithSnapshot(ctx, settingstest.Snapshot("test-org", "1", "Review carefully."))
	ctx = modelpolicy.WithAuthority(ctx, modelpolicy.AuthorizeFunc(func(context.Context, modelpolicy.Attempt) error { return nil }))
	_, err := host.RunTurn(ctx, runner.Request{ConversationID: "old-worker", Input: "hello"}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "does not support enterprise") || old.legacyCalls.Load() != 0 {
		t.Fatalf("unsafe downgrade: err=%v legacy=%d", err, old.legacyCalls.Load())
	}
}

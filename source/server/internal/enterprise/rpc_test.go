package enterprise

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/modelpolicy"
	"cercano/source/server/pkg/proto"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type enterpriseTestAgent struct {
	proto.UnimplementedAgentServer
	client   *http.Client
	endpoint string
	pinned   atomic.Int64
}

func (s *enterpriseTestAgent) ProcessRequest(ctx context.Context, _ *proto.ProcessRequestRequest) (*proto.ProcessRequestResponse, error) {
	if bundle, ok := BundleFromContext(ctx); ok {
		s.pinned.Store(bundle.Policy.Revision)
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", s.endpoint+"/chat/completions", strings.NewReader(`{"model":"approved","messages":[{"role":"user","content":"local test"}]}`))
	response, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	response.Body.Close()
	return &proto.ProcessRequestResponse{}, nil
}

func TestRunningHostLoginActuallyControlsInference(t *testing.T) {
	f := newFixture(t, false)
	var hits atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(200) }))
	defer provider.Close()
	f.mu.Lock()
	f.routes = []v1.Route{{ID: "approved", Provider: "openai", Endpoint: provider.URL + "/v1", Model: "approved", Placement: "external"}}
	f.mu.Unlock()
	h := fixtureHost(t, f, t.TempDir())
	restore := modelpolicy.Install(h)
	defer restore()
	agent := &enterpriseTestAgent{client: modelpolicy.Client(provider.Client(), "openai", "external", modelpolicy.OpenAI), endpoint: provider.URL + "/v1"}
	server := grpc.NewServer(grpc.UnaryInterceptor(h.UnaryInterceptor()), grpc.StreamInterceptor(h.StreamInterceptor()))
	proto.RegisterAgentServer(server, agent)
	proto.RegisterEnterpriseServer(server, NewRPCServer(h, func(_ context.Context, target string) error {
		u, err := url.Parse(target)
		if err != nil {
			return err
		}
		q := u.Query()
		f.mu.Lock()
		f.challenge = q.Get("code_challenge")
		f.mu.Unlock()
		response, err := http.Get(q.Get("redirect_uri") + "?" + url.Values{"state": {q.Get("state")}, "code": {testOrg + "." + randomToken()}}.Encode())
		if err != nil {
			return err
		}
		response.Body.Close()
		return nil
	}))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	defer server.Stop()
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := proto.NewEnterpriseClient(conn)
	modelClient := proto.NewAgentClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state, err := client.GetStatus(ctx, &proto.EnterpriseControlRequest{})
	if err != nil || state.GetManaged() {
		t.Fatal("fresh host is not standalone")
	}
	state, err = client.Login(ctx, &proto.EnterpriseLoginRequest{Server: f.server.URL, OrganizationId: testOrg})
	if err != nil || !state.GetUsable() || !state.GetEnforcementActive() {
		t.Fatalf("login failed: %v", err)
	}
	inspection, err := client.GetPolicy(ctx, &proto.EnterpriseControlRequest{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := v1.DecodePolicy(inspection.PolicyJson)
	if err != nil || p.Revision != 1 || p.Scope.OrganizationID != testOrg || len(p.AllowedRoutes) != 1 || p.AllowedRoutes[0].Model != "approved" {
		t.Fatalf("incorrect policy inspection: %+v %v", p, err)
	}
	if strings.Contains(string(inspection.PolicyJson), f.access) || strings.Contains(string(inspection.PolicyJson), f.refresh) {
		t.Fatal("credentials leaked into policy inspection")
	}
	if _, err = modelClient.ProcessRequest(ctx, &proto.ProcessRequestRequest{Input: "hello"}); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 || agent.pinned.Load() != 1 {
		t.Fatal("running inference did not use verified turn bundle")
	}
	f.mu.Lock()
	f.routes = []v1.Route{}
	f.revision++
	f.mu.Unlock()
	if _, err = client.Synchronize(ctx, &proto.EnterpriseControlRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err = modelClient.ProcessRequest(ctx, &proto.ProcessRequestRequest{Input: "denied"}); err == nil {
		t.Fatal("updated policy did not block inference")
	}
	if hits.Load() != 1 {
		t.Fatal("denied request reached provider")
	}
	state, err = client.Logout(ctx, &proto.EnterpriseControlRequest{})
	if err != nil || !state.GetManaged() || state.GetConnected() || state.GetUsable() {
		t.Fatal("logout restored standalone or retained authorization")
	}
	if _, err = modelClient.ProcessRequest(ctx, &proto.ProcessRequestRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("logged-out work was not blocked: %v", err)
	}
	if _, err = client.GetPolicy(ctx, &proto.EnterpriseControlRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("logged out policy still available: %v", err)
	}
	state, err = client.UseStandalone(ctx, &proto.EnterpriseControlRequest{})
	if err != nil || state.GetManaged() {
		t.Fatalf("explicit standalone failed: %v", err)
	}
	if _, err = modelClient.ProcessRequest(ctx, &proto.ProcessRequestRequest{}); err != nil {
		t.Fatal("standalone behavior not restored:", err)
	}
	if hits.Load() != 2 {
		t.Fatal("unexpected inference count")
	}
}
func TestEnterpriseControlsRejectRemoteAndMissingPeers(t *testing.T) {
	for _, ctx := range []context.Context{context.Background(), peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 50000}})} {
		if status.Code(localControl(ctx)) != codes.PermissionDenied {
			t.Fatal("remote enterprise control accepted")
		}
	}
}

func TestPolicyInspectionRejectsRemoteCaller(t *testing.T) {
	s := NewRPCServer(nil, nil)
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 50000}})
	if _, err := s.GetPolicy(ctx, &proto.EnterpriseControlRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatal(err)
	}
}

package agentclient

import (
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/server"
	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/usage"
	"cercano/source/server/pkg/proto"
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestTokenMetricsClientServerStorageRoundTrip(t *testing.T) {
	store, e := telemetry.NewSQLiteStore(filepath.Join(t.TempDir(), "metrics.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	collector := telemetry.NewCollector(store, 8)
	defer collector.Close()
	if e = collector.EnableAccounting(telemetry.AccountingOptions{}); e != nil {
		t.Fatal(e)
	}
	s := &server.Server{}
	s.SetAccountingCollector(collector)
	listener := bufconn.Listen(1024 * 1024)
	transport := grpc.NewServer()
	proto.RegisterAgentServer(transport, s)
	go transport.Serve(listener)
	t.Cleanup(transport.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	client := &Client{conn: conn, agent: proto.NewAgentClient(conn)}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, e = store.TrackingSince(t.Context()); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(e)
		}
		time.Sleep(time.Millisecond)
	}
	now := time.Now()
	a := usage.AttemptObservation{ID: "wire-fixture", Revision: 1, StartedAt: now, EndedAt: now, Outcome: usage.Completed, Provider: "fixture", Model: "model", Attribution: usage.Attribution{Source: "main"}, Tokens: llm.TokenUsage{Input: llm.ReportedTokens(12), Output: llm.ReportedTokens(0), Final: true}}
	if e = store.WriteAttempts(t.Context(), []usage.AttemptObservation{a}); e != nil {
		t.Fatal(e)
	}
	out, e := client.GetTokenMetrics(t.Context(), &proto.GetTokenMetricsRequest{Preset: "all", Timezone: "UTC"})
	if e != nil {
		t.Fatal(e)
	}
	if out.Totals.Input.Tokens != 12 || out.Totals.Output.KnownRecords != 1 || out.Totals.Incomplete != 0 || out.Totals.Records != 1 {
		t.Fatalf("%v", out)
	}
	unknown := ""
	out, e = client.GetTokenMetrics(t.Context(), &proto.GetTokenMetricsRequest{Preset: "all", Timezone: "UTC", Provider: &unknown})
	if e != nil || out.Totals.Records != 0 {
		t.Fatalf("presence lost: %v %v", out, e)
	}
	_, e = client.GetTokenMetrics(t.Context(), &proto.GetTokenMetricsRequest{Timezone: "invalid"})
	if status.Code(e) != codes.InvalidArgument {
		t.Fatal(e)
	}
	_, e = (&server.Server{}).GetTokenMetrics(context.Background(), &proto.GetTokenMetricsRequest{Timezone: "UTC"})
	if status.Code(e) != codes.Unavailable {
		t.Fatal(e)
	}
	_, e = (&Client{}).GetTokenMetrics(t.Context(), &proto.GetTokenMetricsRequest{})
	if status.Code(e) != codes.Unavailable {
		t.Fatal(e)
	}
}

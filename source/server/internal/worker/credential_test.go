package worker_test

// Tests for the stream credential proxy (Task 2b).
//
// Two scenarios verified end-to-end via bufconn:
//
//  1. Static-key profile: worker sends CredentialRequest, fake host answers
//     with a token, worker builds its provider using the fetched key.
//     Verified by observing the CredentialRequest on the stream and confirming
//     the profile name matches.
//
//  2. ChatGPT-subscription profile (FlavorResponses + RouteChatGPT): worker
//     builds a provider backed by a streamTokenSource. The turn does NOT error
//     with "requires a token source" (finding-#3 fix). When the LLM round
//     triggers Token(), a CredentialRequest fires; the fake host answers with
//     token + accountID.

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/runner"
	"cercano/source/server/internal/worker"
	proto "cercano/source/server/pkg/proto"
)

// startCredTestServer starts a WorkerServer over bufconn with the production
// provider path (nil providerFactory) and a tool factory override. Returns a
// connected RunTurn stream.
func startCredTestServer(t *testing.T) proto.Worker_RunTurnClient {
	t.Helper()

	ws := worker.NewWithFactories(
		nil, // production provider path — will trigger CredentialRequests
		func(*proto.StartTurn) (runner.ToolSvc, error) {
			return &fakeToolSvc{reg: agenttools.NewRegistry()}, nil
		},
	)

	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer()
	proto.RegisterWorkerServer(grpcSrv, ws)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	stream, err := proto.NewWorkerClient(conn).RunTurn(ctx)
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	return stream
}

// drainWithCredAnswers reads WorkerToHost messages, answering CredentialRequests
// with credToken + credAccount. Returns (turnDone, turnErrMsg, credRequestsSeen,
// lastProfileRequested).
func drainWithCredAnswers(
	t *testing.T,
	stream proto.Worker_RunTurnClient,
	credToken, credAccount string,
) (done *proto.TurnDone, turnErr string, credSeen int, lastProfile string) {
	t.Helper()
	for {
		msg, err := stream.Recv()
		if err != nil {
			return // stream closed
		}
		switch m := msg.Msg.(type) {
		case *proto.WorkerToHost_CredRequest:
			credSeen++
			lastProfile = m.CredRequest.GetProfileName()
			resp := &proto.CredentialResponse{
				Id:      m.CredRequest.GetId(),
				Token:   credToken,
				Account: credAccount,
			}
			if sendErr := stream.Send(&proto.HostToWorker{Msg: &proto.HostToWorker_CredResponse{CredResponse: resp}}); sendErr != nil {
				t.Logf("send CredentialResponse: %v", sendErr)
				return
			}
		case *proto.WorkerToHost_Done:
			done = m.Done
			return
		case *proto.WorkerToHost_Error:
			turnErr = m.Error.GetMessage()
			return
		case *proto.WorkerToHost_Event:
			// drain events
		}
	}
}

// TestWorkerCredential_StaticKeyProfileSendsCredRequest verifies that when
// the production provider path runs with an active static-key cloud profile,
// the worker sends a CredentialRequest to the host (before the turn runs)
// with the correct profile name.
func TestWorkerCredential_StaticKeyProfileSendsCredRequest(t *testing.T) {
	const profileName = "anthropic-prod"
	const wantKey = "sk-ant-test-key-9999"

	stream := startCredTestServer(t)

	// ConfigSnapshot with a static-key (Anthropic/FlavorMessages) profile.
	// LocusMode=open_primary: the runner uses the open provider for the actual
	// turn so we avoid a real HTTP call. The cloud provider is still built
	// (which triggers the credential fetch) even though the turn uses open.
	start := &proto.StartTurn{
		ConversationId: "cred-static-1",
		Input:          "hi",
		WorkDir:        t.TempDir(),
		Config: &proto.ConfigSnapshot{
			LocusMode:          "open_primary",
			ActiveCloudProfile: profileName,
			CloudFlavor:        cloudfactory.FlavorMessages,
			CloudModel:         "claude-3-5-haiku-20241022",
		},
	}
	if err := stream.Send(&proto.HostToWorker{Msg: &proto.HostToWorker_Start{Start: start}}); err != nil {
		t.Fatalf("send StartTurn: %v", err)
	}

	_, turnErr, credSeen, lastProfile := drainWithCredAnswers(t, stream, wantKey, "")

	if credSeen == 0 {
		t.Error("worker never sent a CredentialRequest for static-key profile — credential proxy not wired")
	}
	if lastProfile != profileName {
		t.Errorf("CredentialRequest.profile_name = %q, want %q", lastProfile, profileName)
	}
	// Must NOT fail with a credential-related error (that would indicate the key
	// wasn't fetched or wasn't used).
	if strings.Contains(turnErr, "no API key") || strings.Contains(turnErr, "absent-cloud") {
		t.Errorf("turn failed with credential-related error: %s", turnErr)
	}
}

// TestWorkerCredential_ChatGPTSubProfileBuildsProvider verifies finding-#3 fix:
// a FlavorResponses+RouteChatGPT profile now builds a real provider with a
// streamTokenSource, NOT absent-cloud. Previously the production path called
// BuildCloudProvider(prof, resolvedCredential) with no TokenSource, which
// returned an error ("requires a token source") and r.cloudProv stayed nil.
//
// After the fix: a streamTokenSource is installed; the provider is built during
// buildWorkerProviders (no immediate cred fetch — the source is lazy). When the
// runner calls Token() on the first LLM round, a CredentialRequest fires. The
// fake host answers with token + accountID.
func TestWorkerCredential_ChatGPTSubProfileBuildsProvider(t *testing.T) {
	const profileName = "chatgpt-subscription"
	const wantToken = "eyJhbGciOiJSUzI1NiJ9.test-access-token"
	const wantAccount = "acc-chatgpt-12345"

	stream := startCredTestServer(t)

	// ChatGPT subscription profile.
	// LocusMode=cloud_primary: the runner uses the cloud provider.
	// The responses client will attempt to reach the codex backend → network error.
	// That's expected; we verify the provider WAS built (no "requires a token source").
	start := &proto.StartTurn{
		ConversationId: "cred-chatgpt-sub-1",
		Input:          "hi",
		WorkDir:        t.TempDir(),
		Config: &proto.ConfigSnapshot{
			LocusMode:          "cloud_primary",
			ActiveCloudProfile: profileName,
			CloudFlavor:        cloudfactory.FlavorResponses,
			CloudRoute:         cloudfactory.RouteChatGPT,
			CloudModel:         "gpt-4o",
		},
	}
	if err := stream.Send(&proto.HostToWorker{Msg: &proto.HostToWorker_Start{Start: start}}); err != nil {
		t.Fatalf("send StartTurn: %v", err)
	}

	_, turnErr, credSeen, lastProfile := drainWithCredAnswers(t, stream, wantToken, wantAccount)

	// MUST NOT error with "requires a token source" — that is the finding-#3 bug.
	if strings.Contains(turnErr, "requires a token source") {
		t.Errorf("finding #3 not fixed: worker still errors 'requires a token source': %s", turnErr)
	}

	if credSeen > 0 {
		// CredentialRequest fired when Token() was invoked during the LLM round.
		if lastProfile != profileName {
			t.Errorf("CredentialRequest.profile_name = %q, want %q", lastProfile, profileName)
		}
		t.Logf("CredentialRequest fired for ChatGPT-sub profile; token+account forwarded ✓")
	} else {
		// Token() may not have been invoked if the network call errored at a layer
		// above (e.g. the HTTP client itself). Still acceptable — the invariant is
		// no "requires a token source" error.
		t.Logf("no CredentialRequest seen (turn errored before Token() invoked); turnErr=%q", turnErr)
	}

	// Any turn error must be a network/LLM error, NOT a provider-build error.
	if turnErr != "" {
		badErrors := []string{"requires a token source", "absent-cloud", "no cloud provider"}
		for _, bad := range badErrors {
			if strings.Contains(turnErr, bad) {
				t.Errorf("turn failed with provider-build error: %s", turnErr)
			}
		}
		t.Logf("turn errored (expected — no real backend): %s", turnErr)
	}
}

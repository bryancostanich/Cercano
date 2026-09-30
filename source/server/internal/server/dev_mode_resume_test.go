package server

import (
	"cercano/source/server/pkg/agentclient"
	"context"
	"google.golang.org/grpc"
	"net"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

func TestDevModeSurvivesStoreAndServerRestart(t *testing.T) {
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "conversations.db")
	repo := t.TempDir()
	first, store := newServerWithStore(t, database)
	first.SetCloudLLMProvider(&scriptedProvider{scripts: [][]llm.Block{{{Type: llm.BlockText, Text: "ready"}}}, caps: inference.Capabilities{SupportsTools: true}})
	if err := first.StreamProcessRequest(&proto.ProcessRequestRequest{Input: "development kickoff", ConversationId: "dev-conversation", WorkDir: repo, DebugMode: true}, &fakeStream{ctx: ctx}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// A fresh store and server must restore this without the original CLI model,
	// provider, stream, or any in-memory session object.
	restarted, reopened := newServerWithStore(t, database)
	info, err := reopened.Get(ctx, "dev-conversation")
	if err != nil {
		t.Fatal(err)
	}
	if info.DevWorkDir != repo {
		t.Fatal("dev-mode repository was not durably saved")
	}
	// Reload through actual RPC adapters on the freshly opened server too.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	transport := grpc.NewServer()
	proto.RegisterAgentServer(transport, restarted)
	go transport.Serve(listener)
	t.Cleanup(transport.Stop)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, err := agentclient.DialExisting(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	saved, err := client.GetConversation(ctx, "dev-conversation")
	if err != nil {
		t.Fatal(err)
	}
	if saved.DevWorkDir != repo {
		t.Fatal("buffered resume metadata lost dev directory")
	}
	seenTail := false
	err = client.StreamResumeConversationViewportFirst(ctx, "dev-conversation", 48, 96, func(event agentclient.ResumeViewportEvent) error {
		if event.Kind == agentclient.ResumeViewportEventTail {
			seenTail = true
			if event.DevWorkDir != repo {
				t.Error("progressive resume lost dev directory")
			}
		}
		if event.Kind == agentclient.ResumeViewportEventHydrationComplete && !seenTail {
			t.Error("input enabled before persisted mode arrived")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !seenTail {
		t.Fatal("missing initial resume event")
	}
	// Ordinary turns cannot accidentally erase an established dev session.
	if err := restarted.persistDevMode(ctx, &proto.ProcessRequestRequest{ConversationId: "dev-conversation", WorkDir: repo}); err != nil {
		t.Fatal(err)
	}
	again, err := reopened.Get(ctx, "dev-conversation")
	if err != nil || again.DevWorkDir != repo {
		t.Fatal("ordinary client erased persisted dev mode")
	}
	if err := reopened.EnsureConversation(ctx, "ordinary", repo, ""); err != nil {
		t.Fatal(err)
	}
	plain, err := client.GetConversation(ctx, "ordinary")
	if err != nil {
		t.Fatal(err)
	}
	if plain.DevWorkDir != "" {
		t.Fatal("ordinary project directory inferred dev mode")
	}
}

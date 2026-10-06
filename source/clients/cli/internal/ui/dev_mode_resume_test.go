package ui

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/server/pkg/agentclient"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
)

// Read metadata from disk for every RPC: there is no live CLI model or in-memory
// session state that a fresh model could accidentally rely on during resume.
type persistedDevResumeServer struct {
	proto.UnimplementedAgentServer
	metadataPath string
}

func (s *persistedDevResumeServer) GetConversation(context.Context, *proto.GetConversationRequest) (*proto.Conversation, error) {
	raw, err := os.ReadFile(s.metadataPath)
	if err != nil {
		return nil, err
	}
	var info proto.Conversation
	if err := protojson.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	return &info, nil
}
func (s *persistedDevResumeServer) ResumeConversation(context.Context, *proto.ResumeConversationRequest) (*proto.ResumeConversationResponse, error) {
	return &proto.ResumeConversationResponse{Turns: []*proto.PersistedTurn{{Role: "user", Content: "development kickoff"}, {Role: "assistant", Content: "ready"}}}, nil
}
func (s *persistedDevResumeServer) StreamResumeConversationViewportFirst(req *proto.ResumeConversationViewportFirstRequest, stream proto.Agent_StreamResumeConversationViewportFirstServer) error {
	info, err := s.GetConversation(stream.Context(), &proto.GetConversationRequest{})
	if err != nil {
		return err
	}
	turns, _ := s.ResumeConversation(stream.Context(), &proto.ResumeConversationRequest{})
	if err := stream.Send(&proto.ResumeConversationViewportFirstEvent{Kind: proto.ResumeConversationViewportFirstEvent_TAIL, ConversationId: req.ConversationId, Turns: turns.Turns, TotalTurns: 2, DevWorkDir: info.DevWorkDir}); err != nil {
		return err
	}
	if err := stream.Send(&proto.ResumeConversationViewportFirstEvent{Kind: proto.ResumeConversationViewportFirstEvent_HYDRATION_COMPLETE, ConversationId: req.ConversationId}); err != nil {
		return err
	}
	return stream.Send(&proto.ResumeConversationViewportFirstEvent{Kind: proto.ResumeConversationViewportFirstEvent_BACKFILL_COMPLETE, ConversationId: req.ConversationId})
}
func devResumeClient(t *testing.T, metadataPath string) *agentclient.Client {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	proto.RegisterAgentServer(server, &persistedDevResumeServer{metadataPath: metadataPath})
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	client, err := agentclient.DialExisting(context.Background(), listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}
func writeDevResumeMetadata(t *testing.T, path, repo string) {
	t.Helper()
	raw, err := protojson.Marshal(&proto.Conversation{Id: "saved", ProjectDir: repo, DevWorkDir: repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestFreshCLIRehydratesDevModeFromPersistedConversation(t *testing.T) {
	for _, progressive := range []bool{false, true} {
		name := "buffered"
		if progressive {
			name = "progressive"
		}
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			metadata := filepath.Join(t.TempDir(), "conversation.json")
			writeDevResumeMetadata(t, metadata, repo)
			client := devResumeClient(t, metadata)
			fresh := New(nil, false)
			fresh.agent = client
			if fresh.workDirOverride != "" {
				t.Fatal("test did not start from a fresh CLI")
			}
			if progressive {
				fresh, _ = fresh.beginProgressiveResume("saved")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				err := client.StreamResumeConversationViewportFirst(ctx, "saved", 48, 96, func(event agentclient.ResumeViewportEvent) error {
					fresh, _ = fresh.applyProgressiveResumeEvent(resumeViewportStreamMsg{gen: fresh.resumeGen, event: event})
					if event.Kind == agentclient.ResumeViewportEventTail && fresh.workDirOverride != repo {
						t.Error("dev state missing before hydration enables input")
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				fresh, _ = fresh.applyResume("saved")
			}
			if fresh.workDirOverride != repo || fresh.effectiveWorkDir() != repo {
				t.Fatalf("repository not restored: override=%q effective=%q", fresh.workDirOverride, fresh.effectiveWorkDir())
			}
			if fresh.wdRef == nil || fresh.wdRef.dir != repo {
				t.Fatal("tool/completion working directory not restored")
			}
			if !fresh.debugTaskControlsEnabled() {
				t.Fatal("debug UI gate not rehydrated")
			}
			if len(fresh.inputHistory) != 1 || fresh.streaming {
				t.Fatal("resume replayed kickoff instead of restoring state")
			}
			// Switching to ordinary/legacy metadata clears dev mode, despite a transcript
			// containing the same development-looking text.
			writeDevResumeMetadata(t, metadata, "")
			fresh, _ = fresh.applyResume("plain")
			if fresh.workDirOverride != "" || fresh.wdRef.dir != "" || fresh.debugTaskControlsEnabled() {
				t.Fatal("dev mode leaked into ordinary conversation")
			}
		})
	}
}
func TestProgressiveDevModeMetadataIgnoresStaleConversation(t *testing.T) {
	m := New(nil, false)
	m.resumeGen = 2
	m.restoreDevMode("/tmp/current")
	m, _ = m.applyProgressiveResumeEvent(resumeViewportStreamMsg{gen: 1, event: agentclient.ResumeViewportEvent{Kind: agentclient.ResumeViewportEventTail, DevWorkDir: "/tmp/old"}})
	if m.workDirOverride != "/tmp/current" {
		t.Fatal("stale resume changed dev mode")
	}
	m.restoreDevMode("relative/path")
	if m.workDirOverride != "" {
		t.Fatal("relative metadata unexpectedly enabled dev mode")
	}
}

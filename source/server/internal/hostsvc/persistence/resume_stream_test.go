package persistence

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc/metadata"
)

func TestResumeChunkBudgetCreatesMultipleChunksInOrder(t *testing.T) {
	turns := make([]conversation.Turn, 0, 6)
	for i := 0; i < 6; i++ {
		turns = append(turns, conversation.Turn{
			ID:             fmt.Sprintf("turn-%d", i),
			ConversationID: "conv-1",
			Role:           "assistant",
			Content:        strings.Repeat("x", 300),
			CreatedAt:      time.Unix(int64(i), 0),
		})
	}

	var chunks []*proto.ResumeConversationChunk
	err := sendResumeTurnChunks("conv-1", turns, 512, func(chunk *proto.ResumeConversationChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("sendResumeTurnChunks() error = %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want multiple chunks", len(chunks))
	}
	var got []string
	for _, chunk := range chunks {
		for _, turn := range chunk.GetTurns() {
			got = append(got, turn.GetId())
		}
	}
	for i, id := range got {
		want := fmt.Sprintf("turn-%d", i)
		if id != want {
			t.Fatalf("turn order at %d = %q, want %q", i, id, want)
		}
	}
}

func TestResumeChunkBudgetHandlesEmptyTranscript(t *testing.T) {
	called := false
	err := sendResumeTurnChunks("conv-1", nil, 512, func(chunk *proto.ResumeConversationChunk) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("sendResumeTurnChunks() error = %v", err)
	}
	if called {
		t.Fatal("empty transcript should not send an empty chunk")
	}
}

func TestResumeChunkBudgetRejectsOversizedSingleTurn(t *testing.T) {
	turns := []conversation.Turn{{
		ID:             "turn-large",
		ConversationID: "conv-1",
		Role:           "assistant",
		Content:        strings.Repeat("x", 1024),
	}}
	err := sendResumeTurnChunksWithLimits("conv-1", turns, 512, 256, func(chunk *proto.ResumeConversationChunk) error {
		return nil
	})
	if err == nil {
		t.Fatal("sendResumeTurnChunks() error = nil, want oversized-turn error")
	}
}

type viewportResumeFakeAgent struct {
	store       conversation.Store
	hydrateWait chan struct{}
}

func (f viewportResumeFakeAgent) PersistentStore() conversation.Store { return f.store }
func (f viewportResumeFakeAgent) ListConversations(context.Context, string, int) ([]conversation.Info, error) {
	return nil, nil
}
func (f viewportResumeFakeAgent) GetConversation(context.Context, string) (conversation.Info, error) {
	return conversation.Info{}, nil
}
func (f viewportResumeFakeAgent) ResumeConversation(ctx context.Context, conversationID string) ([]conversation.Turn, error) {
	if f.hydrateWait != nil {
		select {
		case <-f.hydrateWait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.store.GetTurns(ctx, conversationID)
}
func (f viewportResumeFakeAgent) DeleteConversation(context.Context, string) error { return nil }
func (f viewportResumeFakeAgent) RenameConversation(context.Context, string, string) error {
	return nil
}
func (f viewportResumeFakeAgent) IsCompacting(string) bool  { return false }
func (f viewportResumeFakeAgent) ScheduleCompaction(string) {}

type viewportResumeFakeStream struct {
	ctx    context.Context
	events []*proto.ResumeConversationViewportFirstEvent
}

func (s *viewportResumeFakeStream) Send(ev *proto.ResumeConversationViewportFirstEvent) error {
	s.events = append(s.events, ev)
	return nil
}
func (s *viewportResumeFakeStream) SetHeader(metadata.MD) error  { return nil }
func (s *viewportResumeFakeStream) SendHeader(metadata.MD) error { return nil }
func (s *viewportResumeFakeStream) SetTrailer(metadata.MD)       {}
func (s *viewportResumeFakeStream) Context() context.Context     { return s.ctx }
func (s *viewportResumeFakeStream) SendMsg(any) error            { return nil }
func (s *viewportResumeFakeStream) RecvMsg(any) error            { return nil }

func TestViewportFirstResumeStreamsTailBeforeHydrationAndBackfillsOlder(t *testing.T) {
	ctx := context.Background()
	store, err := conversation.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const convID = "conv-viewport"
	if err := store.EnsureConversation(ctx, convID, "/tmp/project", "model"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if err := store.Append(ctx, conversation.Turn{ID: fmt.Sprintf("turn-%d", i), ConversationID: convID, Role: "assistant", Content: fmt.Sprintf("turn-%d", i), CreatedAt: time.Unix(int64(i), 0)}); err != nil {
			t.Fatal(err)
		}
	}
	hydrateWait := make(chan struct{})
	svc := &svc{convAgent: viewportResumeFakeAgent{store: store, hydrateWait: hydrateWait}}
	stream := &viewportResumeFakeStream{ctx: ctx}
	done := make(chan error, 1)
	go func() {
		done <- svc.StreamResumeConversationViewportFirst(&proto.ResumeConversationViewportFirstRequest{ConversationId: convID, TailTurns: 2, OlderChunkTurns: 2}, stream)
	}()

	deadline := time.After(2 * time.Second)
	for len(stream.events) < 3 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for tail/backfill events; got %d", len(stream.events))
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if stream.events[0].GetKind() != proto.ResumeConversationViewportFirstEvent_TAIL {
		t.Fatalf("first event kind = %v, want tail", stream.events[0].GetKind())
	}
	if got := stream.events[0].GetTurns(); len(got) != 2 || got[0].GetContent() != "turn-4" || got[1].GetContent() != "turn-5" {
		t.Fatalf("tail turns = %+v, want turn-4/turn-5", got)
	}
	if stream.events[0].GetStartIndex() != 4 || stream.events[0].GetTotalTurns() != 6 {
		t.Fatalf("tail range = %d/%d, want 4/6", stream.events[0].GetStartIndex(), stream.events[0].GetTotalTurns())
	}
	for _, ev := range stream.events[:3] {
		if ev.GetKind() == proto.ResumeConversationViewportFirstEvent_HYDRATION_COMPLETE {
			t.Fatalf("hydration completed before test released it: events=%v", stream.events)
		}
	}
	close(hydrateWait)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StreamResumeConversationViewportFirst() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not finish after hydration release")
	}
	last := stream.events[len(stream.events)-1]
	if last.GetKind() != proto.ResumeConversationViewportFirstEvent_HYDRATION_COMPLETE {
		t.Fatalf("last event kind = %v, want hydration complete", last.GetKind())
	}
}

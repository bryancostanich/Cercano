package persistence

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/pkg/proto"
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

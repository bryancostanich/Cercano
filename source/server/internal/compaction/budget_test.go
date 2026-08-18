package compaction

import (
	"errors"
	"fmt"
	"testing"

	"cercano/source/server/internal/llm"
)

func budgetTextMsg(role llm.Role, text string) llm.Message {
	return llm.Message{Role: role, Blocks: []llm.Block{{Type: llm.BlockText, Text: text}}}
}

func TestEstimateSummaryBudget_OutputReserveCanOverflow(t *testing.T) {
	prompt := string(make([]byte, 1000)) // about 251 tokens
	fit := EstimateSummaryBudget(prompt, 100, 500)
	if !fit.Fits {
		t.Fatalf("expected smaller reserve to fit: %+v", fit)
	}
	over := EstimateSummaryBudget(prompt, 250, 500)
	if over.Fits {
		t.Fatalf("expected reserve to overflow budget: %+v", over)
	}
}

func TestPackSummaryChunks_OneFittingChunk(t *testing.T) {
	msgs := []llm.Message{budgetTextMsg(llm.RoleUser, "one"), budgetTextMsg(llm.RoleAssistant, "two")}
	chunks, err := PackSummaryChunks(msgs, 16000, DefaultSummaryOutputReserve)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || len(chunks[0]) != 2 {
		t.Fatalf("chunks = %#v", chunks)
	}
}

func TestPackSummaryChunks_MultipleChunksStableOrder(t *testing.T) {
	msgs := []llm.Message{}
	for i := 0; i < 5; i++ {
		msgs = append(msgs, budgetTextMsg(llm.RoleUser, fmt.Sprintf("msg-%d %s", i, string(make([]byte, 1800)))))
	}
	chunks, err := PackSummaryChunks(msgs, 3000, 256)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	var got []string
	for _, chunk := range chunks {
		budget := EstimateSummaryBudget(BuildSummaryPrompt(chunk), 256, 3000)
		if !budget.Fits {
			t.Fatalf("chunk over budget: %+v", budget)
		}
		for _, msg := range chunk {
			got = append(got, msg.Blocks[0].Text[:5])
		}
	}
	want := []string{"msg-0", "msg-1", "msg-2", "msg-3", "msg-4"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order changed: got %v want %v", got, want)
		}
	}
}

func TestPackSummaryChunks_SingleOversizedMessageDefers(t *testing.T) {
	msgs := []llm.Message{budgetTextMsg(llm.RoleUser, string(make([]byte, 20000)))}
	_, err := PackSummaryChunks(msgs, 2000, 256)
	var def *DeferralError
	if !errors.As(err, &def) {
		t.Fatalf("expected DeferralError, got %T %v", err, err)
	}
}

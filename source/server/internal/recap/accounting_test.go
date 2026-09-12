package recap

import (
	"context"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/usage"
)

func TestBackgroundRecapCarriesAccounting(t *testing.T) {
	store := &fakeStore{turns: []conversation.Turn{{Role: "user", Content: "hello"}}, updated: make(chan string, 2)}
	observed := make(chan usage.AttemptObservation, 8)
	g := New(store, func(ctx context.Context, _ string) (string, error) {
		usage.StartAttempt(ctx, "fake", "fake").Finish(usage.Completed)
		return "summary", nil
	}, time.Millisecond, 12)
	g.SetAttemptSink(func(a usage.AttemptObservation) bool { observed <- a; return true })
	g.Schedule("conversation")
	var first string
	for i := 0; i < 4; i++ {
		select {
		case a := <-observed:
			if a.Attribution.Source != "recap" || a.Attribution.ConversationID != "conversation" || a.Attribution.OperationID == "" {
				t.Fatalf("background attribution=%+v", a.Attribution)
			}
			if first == "" {
				first = a.ID
			} else if i == 2 && a.ID == first {
				t.Fatal("title reused recap attempt ID")
			}
		case <-time.After(time.Second):
			t.Fatal("background recap/title accounting missing")
		}
	}
}

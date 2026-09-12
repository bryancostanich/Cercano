package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/server/internal/broker"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/openai"
	"cercano/source/server/internal/telemetry"
	"cercano/source/server/internal/usage"
)

func TestServerAttemptAccountingRoundTrip(t *testing.T) {
	store, err := telemetry.NewSQLiteStore(filepath.Join(t.TempDir(), "telemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	collector := telemetry.NewCollector(store, 8)
	defer collector.Close()
	collector.SetSessionID("session")
	if err = collector.EnableAccounting(telemetry.AccountingOptions{}); err != nil {
		t.Fatal(err)
	}
	var id string
	s := &Server{turnBroker: broker.New()}
	s.SetAttemptSink(func(a usage.AttemptObservation) bool {
		if a.Revision == 1 {
			id = a.ID
		}
		return collector.EmitAttempt(a)
	})
	ctx, _, release := s.beginTurn(t.Context(), "conversation")
	defer release()
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"actual","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7}}`))
	}))
	defer httpServer.Close()
	provider := openai.NewClient(openai.Config{BaseURL: httpServer.URL, APIKey: "fake", Model: "requested"})
	if _, err = provider.Chat(ctx, llm.ChatRequest{}); err != nil {
		t.Fatal(err)
	}
	closeCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err = collector.CloseContext(closeCtx); err != nil {
		t.Fatal(err)
	}
	a, err := store.AccountingAttempt(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if a.Outcome != usage.Completed || a.Model != "actual" || a.Tokens.Input != llm.ReportedTokens(11) || a.Tokens.Output != llm.ReportedTokens(7) {
		t.Fatalf("actual usage=%+v", a)
	}
	if a.Attribution.Source != "main" || a.Attribution.ConversationID != "conversation" || a.Attribution.SessionID != "session" || a.Attribution.OperationID == "" {
		t.Fatalf("attribution=%+v", a.Attribution)
	}
}

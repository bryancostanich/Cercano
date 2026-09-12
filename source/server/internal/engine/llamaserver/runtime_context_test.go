package llamaserver

import (
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/localruntime"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRuntimeContextBindsRequestToProcessGeneration(t *testing.T) {
	m := &fakeRuntimeManager{instances: []localruntime.InstanceRecord{{ID: "serving", ModelID: "model", Runtime: runtimeName, PID: 10, StartedAt: time.Now(), Endpoint: "http://127.0.0.1:1", State: localruntime.InstanceRunning, Context: confirmedTestCapacity()}}}
	e := NewEngine(m)
	provider := NewLLMProvider(e)
	capacity, err := e.RuntimeContext(t.Context(), "model", true)
	if err != nil {
		t.Fatal(err)
	}
	if capacity.Window != 16384 {
		t.Fatalf("capacity=%+v", capacity)
	}
	ctx := llm.WithRuntimeContext(t.Context(), capacity)
	m.instances[0].PID++
	if _, _, err := provider.clientFor(ctx, llm.ChatRequest{Model: "model"}); err == nil {
		t.Fatal("request crossed a process generation without rebudgeting")
	}
	m.instances[0].State = localruntime.InstanceStopped
	if _, err := e.RuntimeContext(t.Context(), "model", false); err == nil {
		t.Fatal("stopped capacity reused")
	}
	if _, err := e.RuntimeContext(t.Context(), "other-model", false); err == nil {
		t.Fatal("other model reused capacity")
	}
}

func TestRuntimeContextCancellationDoesNotStart(t *testing.T) {
	m := &fakeRuntimeManager{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewEngine(m).RuntimeContext(ctx, "model", true); err == nil {
		t.Fatal("canceled preparation succeeded")
	}
	if m.startCount != 0 {
		t.Fatal("canceled preparation started runtime")
	}
}

func TestDirectCompletionRejectsGenerationChangeBeforeHTTP(t *testing.T) {
	sent := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent++
		fmt.Fprint(w, `{"choices":[{"message":{"content":"wrong generation"}}]}`)
	}))
	defer server.Close()
	m := &fakeRuntimeManager{instances: []localruntime.InstanceRecord{{ID: "serving", ModelID: "model", Runtime: runtimeName, PID: 10, StartedAt: time.Now(), Endpoint: server.URL, State: localruntime.InstanceRunning, Context: confirmedTestCapacity()}}}
	e := NewEngine(m)
	capacity, err := e.RuntimeContext(t.Context(), "model", true)
	if err != nil {
		t.Fatal(err)
	}
	m.instances[0].PID++
	_, err = e.Complete(llm.WithRuntimeContext(t.Context(), capacity), "model", "hello", "", engine.GenOptions{})
	if err == nil || sent != 0 {
		t.Fatalf("direct completion crossed generation: sent=%d err=%v", sent, err)
	}
}

package worker

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/runner"
	"cercano/source/server/pkg/proto"
	"context"
	wire "google.golang.org/protobuf/proto"
	"testing"
	"time"
)

type runtimeContextHost struct{ inference.Provider }

func (runtimeContextHost) Name() string { return "llama_server" }
func (runtimeContextHost) RuntimeContext(_ context.Context, model string, _ bool) (llm.RuntimeContext, error) {
	n := 8192
	if model == "large" {
		n = 131072
	}
	return llm.RuntimeContext{Window: n, InstanceID: model + "-generation"}, nil
}

func TestWorkerCapacityRoundTripsThroughOwningHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sndr := &sender{ch: make(chan *proto.WorkerToHost, 4), done: make(chan struct{})}
	p := newHostManagedOpenProxy(sndr, "llama_server")
	host := &workerRunner{openProvider: func() inference.Provider { return runtimeContextHost{} }}
	done := make(chan struct{})
	defer func() { cancel(); <-done }()
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-sndr.ch:
				data, err := wire.Marshal(msg)
				if err != nil {
					t.Error(err)
					return
				}
				var decoded proto.WorkerToHost
				if err := wire.Unmarshal(data, &decoded); err != nil {
					t.Error(err)
					return
				}
				host.serveOpenInference(ctx, decoded.GetOpenRequest(), func(reply *proto.HostToWorker) error {
					data, err := wire.Marshal(reply)
					if err != nil {
						return err
					}
					var decoded proto.HostToWorker
					if err := wire.Unmarshal(data, &decoded); err != nil {
						return err
					}
					p.deliver(decoded.GetOpenEvent())
					return nil
				})
			}
		}
	}()
	for _, model := range []string{"small", "large", "small"} {
		got, err := llm.ResolveRuntimeContext(ctx, p, model, true)
		if err != nil {
			t.Fatal(err)
		}
		want := 8192
		if model == "large" {
			want = 131072
		}
		if got.Window != want || got.InstanceID != model+"-generation" {
			t.Fatalf("capacity leaked between models: %+v", got)
		}
	}
}

func TestWorkerCapacityCancellationCleansPending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := newHostManagedOpenProxy(&sender{ch: make(chan *proto.WorkerToHost), done: make(chan struct{})}, "llama_server")
	if _, err := p.RuntimeContext(ctx, "model", true); err == nil {
		t.Fatal("expected cancellation")
	}
	if len(p.contextPending) != 0 {
		t.Fatal("pending context request leaked")
	}
}

func TestWorkerForwardsRequestAccounting(t *testing.T) {
	sndr := &sender{ch: make(chan *proto.WorkerToHost, 1)}
	sink, ok := any(&streamEventSink{sndr: sndr}).(runner.RequestAccountingSink)
	if !ok {
		t.Fatal("worker drops request accounting instead of forwarding it")
	}
	want := runner.RequestAccounting{Model: "local", Provider: "llama_server", RuntimeInstanceID: "process-1", ContextWindow: 65536, ContextWindowKnown: true, MessageTokens: 40, SystemTokens: 10, ToolSchemaTokens: 5, OutputReserveTokens: 128, EstimatedRequestTokens: 183}
	sink.RecordRequestAccounting(want)
	data, err := wire.Marshal(<-sndr.ch)
	if err != nil {
		t.Fatal(err)
	}
	var message proto.WorkerToHost
	if err := wire.Unmarshal(data, &message); err != nil {
		t.Fatal(err)
	}
	if got := unmarshalRequestAccounting(message.GetRequestAccounting()); got != want {
		t.Fatalf("accounting mismatch: %+v", got)
	}
}

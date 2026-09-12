package runtimecontrol

import (
	"cercano/source/server/pkg/proto"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeRPC struct {
	statusFailure         bool
	before, after         []*proto.RuntimeInstance
	statusCalls, restarts int
	request               *proto.RestartRuntimeRequest
	failure               string
	transport             error
}

func (f *fakeRPC) GetRuntimeStatus(context.Context, *proto.GetRuntimeStatusRequest) (*proto.GetRuntimeStatusResponse, error) {
	f.statusCalls++
	if f.statusCalls > 1 && f.statusFailure {
		return nil, errors.New("status unavailable")
	}
	instances := f.before
	if f.statusCalls > 1 {
		instances = f.after
	}
	return &proto.GetRuntimeStatusResponse{Instances: instances}, nil
}
func (f *fakeRPC) RestartRuntime(_ context.Context, r *proto.RestartRuntimeRequest) (*proto.RestartRuntimeResponse, error) {
	f.restarts++
	f.request = r
	if f.transport != nil {
		return nil, f.transport
	}
	if f.failure != "" {
		return &proto.RestartRuntimeResponse{Error: f.failure}, nil
	}
	return &proto.RestartRuntimeResponse{Ok: true, Instance: &proto.RuntimeInstance{Id: "new", Runtime: r.Runtime, ModelId: r.ModelId, Pid: 456, State: "running"}}, nil
}
func instance(id string) *proto.RuntimeInstance {
	return &proto.RuntimeInstance{Id: id, Runtime: "llama_server", ModelId: "model-" + id, Pid: 123, State: "running"}
}
func TestRestartUsesExactRPCSelection(t *testing.T) {
	for _, id := range []string{"", "one"} {
		t.Run(id, func(t *testing.T) {
			f := &fakeRPC{before: []*proto.RuntimeInstance{instance("one"), {Id: "ollama", Runtime: "ollama"}}}
			raw, err := Restart(t.Context(), f, id)
			if err != nil {
				t.Fatal(err)
			}
			var result Result
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			if !result.OK || result.OldPID != 123 || result.Instance.PID != 456 || f.request.InstanceId != "one" || f.request.Runtime != "llama_server" || f.request.ModelId != "model-one" || f.restarts != 1 {
				t.Fatalf("wrong restart: %s request=%+v", raw, f.request)
			}
		})
	}
}
func TestRestartRejectsAmbiguousUnknownAndNonLlama(t *testing.T) {
	for _, id := range []string{"", "unknown", "ollama"} {
		f := &fakeRPC{before: []*proto.RuntimeInstance{instance("one"), instance("two"), {Id: "ollama", Runtime: "ollama"}}}
		raw, err := Restart(t.Context(), f, id)
		if err != nil {
			t.Fatal(err)
		}
		var result Result
		_ = json.Unmarshal(raw, &result)
		if result.OK || result.State != "unchanged" || f.restarts != 0 || len(result.Available) != 2 {
			t.Fatalf("unsafe selection: %s", raw)
		}
	}
}
func TestFailedRestartReportsStoppedAndPreservesGuardError(t *testing.T) {
	for _, transport := range []bool{false, true} {
		f := &fakeRPC{before: []*proto.RuntimeInstance{instance("one")}, failure: "memory guard: projected usage exceeds limit"}
		if transport {
			f.transport = errors.New(f.failure)
		}
		raw, err := Restart(t.Context(), f, "")
		if err != nil {
			t.Fatal(err)
		}
		var result Result
		_ = json.Unmarshal(raw, &result)
		if result.OK || result.State != "stopped" || !strings.Contains(result.Error, "memory guard") || f.restarts != 1 || f.statusCalls != 2 {
			t.Fatalf("failure hidden/retried: %s", raw)
		}
	}
}
func TestFailedRestartDoesNotClaimStillRunningProcessStopped(t *testing.T) {
	f := &fakeRPC{before: []*proto.RuntimeInstance{instance("one")}, after: []*proto.RuntimeInstance{instance("one")}, failure: "stop refused"}
	raw, err := Restart(t.Context(), f, "one")
	if err != nil {
		t.Fatal(err)
	}
	var result Result
	_ = json.Unmarshal(raw, &result)
	if result.State != "running" || result.OK {
		t.Fatalf("wrong outcome: %s", raw)
	}
}

func TestFailedRestartDoesNotBorrowAnotherInstanceState(t *testing.T) {
	other := instance("other")
	other.ModelId = "model-one"
	f := &fakeRPC{before: []*proto.RuntimeInstance{instance("one"), other}, after: []*proto.RuntimeInstance{other}, failure: "memory guard"}
	raw, err := Restart(t.Context(), f, "one")
	if err != nil {
		t.Fatal(err)
	}
	var result Result
	_ = json.Unmarshal(raw, &result)
	if result.State != "stopped" || result.Instance != nil {
		t.Fatalf("borrowed unrelated instance: %s", raw)
	}
}

func TestCancelledRestartDoesNotInvokeRuntimeRPC(t *testing.T) {
	f := &fakeRPC{before: []*proto.RuntimeInstance{instance("one")}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Restart(ctx, f, ""); !errors.Is(err, context.Canceled) || f.restarts != 0 {
		t.Fatalf("cancelled call restarted runtime: calls=%d err=%v", f.restarts, err)
	}
}

func TestFailedRestartWithUnavailableStatusReportsUnknown(t *testing.T) {
	f := &fakeRPC{before: []*proto.RuntimeInstance{instance("one")}, failure: "memory guard", statusFailure: true}
	raw, err := Restart(t.Context(), f, "one")
	if err != nil {
		t.Fatal(err)
	}
	var result Result
	_ = json.Unmarshal(raw, &result)
	if result.OK || result.State != "unknown" || result.Error != "memory guard" {
		t.Fatalf("invented outcome: %s", raw)
	}
}

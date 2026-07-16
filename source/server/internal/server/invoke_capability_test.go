package server

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/pkg/proto"
)

func TestInvokeCapabilityRunsRegisteredCap(t *testing.T) {
	reg := capabilities.NewRegistry(capabilities.Services{})
	reg.MustRegister(testEchoCap{})
	s := NewServer(nil, nil, nil, nil, nil)
	s.toolSvc.SetCapRegistry(reg)
	args, _ := json.Marshal(map[string]any{"v": "hi"})
	resp, err := s.InvokeCapability(context.Background(), &proto.InvokeCapabilityRequest{
		Name: "echo", ArgsJson: args,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	var got capabilities.Result
	if err := json.Unmarshal(resp.ResultJson, &got); err != nil {
		t.Fatal(err)
	}
	if got.Text == "" {
		t.Fatalf("empty result: %+v", got)
	}
}

func TestInvokeCapabilityUnknownName(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	s.toolSvc.SetCapRegistry(capabilities.NewRegistry(capabilities.Services{}))
	resp, err := s.InvokeCapability(context.Background(), &proto.InvokeCapabilityRequest{Name: "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsError {
		t.Fatal("expected is_error for unknown capability")
	}
}

func TestInvokeCapabilityNilRegistry(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, nil)
	resp, err := s.InvokeCapability(context.Background(), &proto.InvokeCapabilityRequest{Name: "anything"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsError {
		t.Fatal("expected is_error for nil registry")
	}
	if resp.Error != "capability registry not initialized" {
		t.Fatalf("unexpected error message: %s", resp.Error)
	}
}

// testEchoCap is a minimal capability for the handler test.
type testEchoCap struct{}

func (testEchoCap) Name() string                   { return "echo" }
func (testEchoCap) Description() string            { return "echo" }
func (testEchoCap) Tier() capabilities.Tier        { return capabilities.TierR }
func (testEchoCap) Schema() capabilities.Schema    { return capabilities.Schema(`{"type":"object"}`) }
func (testEchoCap) Surfaces() capabilities.Surface { return capabilities.SurfaceMCP }
func (testEchoCap) Execute(_ context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	return capabilities.NewTextResult("echo " + string(call.Args)), nil
}

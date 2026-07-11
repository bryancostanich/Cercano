package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	runtimessvc "cercano/source/server/internal/hostsvc/runtimes"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// writeTestGGUF writes a minimal GGUF with qwen-style keys (28 layers,
// 4 KV heads, head_dim 128 -> 57344 KV bytes/token) plus filler so the
// file is larger than the parse window.
func writeTestGGUF(t *testing.T) string {
	t.Helper()
	var kvs bytes.Buffer
	writeStr := func(w *bytes.Buffer, s string) {
		binary.Write(w, binary.LittleEndian, uint64(len(s)))
		w.WriteString(s)
	}
	addStr := func(key, val string) {
		writeStr(&kvs, key)
		binary.Write(&kvs, binary.LittleEndian, uint32(8))
		writeStr(&kvs, val)
	}
	addU32 := func(key string, val uint32) {
		writeStr(&kvs, key)
		binary.Write(&kvs, binary.LittleEndian, uint32(4))
		binary.Write(&kvs, binary.LittleEndian, val)
	}
	addStr("general.architecture", "qwen2")
	addU32("qwen2.block_count", 28)
	addU32("qwen2.context_length", 32768)
	addU32("qwen2.embedding_length", 3584)
	addU32("qwen2.attention.head_count", 28)
	addU32("qwen2.attention.head_count_kv", 4)

	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, uint32(0x46554747))
	binary.Write(&out, binary.LittleEndian, uint32(3))
	binary.Write(&out, binary.LittleEndian, uint64(0))
	binary.Write(&out, binary.LittleEndian, uint64(6))
	out.Write(kvs.Bytes())
	out.Write(bytes.Repeat([]byte{0xCD}, 300*1024))

	path := filepath.Join(t.TempDir(), "test-model.gguf")
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatalf("write test gguf: %v", err)
	}
	return path
}

// estimateFakeProvider serves a fixed inventory.
type estimateFakeProvider struct {
	models []localruntime.ModelRecord
}

func (p *estimateFakeProvider) Name() string { return "llama_server" }
func (p *estimateFakeProvider) Capabilities() localruntime.RuntimeCapabilities {
	return localruntime.RuntimeCapabilities{CanListModels: true}
}
func (p *estimateFakeProvider) Discover(context.Context) ([]localruntime.ModelRecord, error) {
	return p.models, nil
}
func (p *estimateFakeProvider) Start(context.Context, localruntime.StartRequest, localruntime.LogSink) (*localruntime.InstanceRecord, error) {
	return nil, nil
}
func (p *estimateFakeProvider) Stop(context.Context, string) error { return nil }
func (p *estimateFakeProvider) Probe(context.Context, string) (*localruntime.InstanceHealth, error) {
	return nil, nil
}

func TestGetModelRAMEstimate_LocalModelFromInventory(t *testing.T) {
	path := writeTestGGUF(t)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mgr := localruntime.NewManager()
	mgr.RegisterProvider(&estimateFakeProvider{models: []localruntime.ModelRecord{{
		ID:        "llama_server:test-model",
		Runtime:   "llama_server",
		Path:      path,
		SizeBytes: fi.Size(),
	}}})
	rtSvc := runtimessvc.New(cfgsvc.New("", config.Config{}, nil))
	rtSvc.SetRuntimeManager(mgr)
	s := &Server{runtimesSvc: rtSvc}

	resp, err := s.GetModelRAMEstimate(context.Background(), &proto.GetModelRAMEstimateRequest{
		Runtime: "llama_server",
		ModelId: "llama_server:test-model",
	})
	if err != nil {
		t.Fatalf("GetModelRAMEstimate: %v", err)
	}
	if resp.GetError() != "" {
		t.Fatalf("unexpected error: %s", resp.GetError())
	}
	if resp.GetKvBytesPerToken() != 57344 {
		t.Errorf("KvBytesPerToken = %d, want 57344", resp.GetKvBytesPerToken())
	}
	if resp.GetMaxContextTokens() != 32768 {
		t.Errorf("MaxContextTokens = %d, want 32768", resp.GetMaxContextTokens())
	}
	if resp.GetWeightsBytes() != fi.Size() {
		t.Errorf("WeightsBytes = %d, want %d", resp.GetWeightsBytes(), fi.Size())
	}
	if resp.GetArchitecture() != "qwen2" {
		t.Errorf("Architecture = %q", resp.GetArchitecture())
	}
}

func TestGetModelRAMEstimate_UnknownModelSoftFails(t *testing.T) {
	rtSvc2 := runtimessvc.New(cfgsvc.New("", config.Config{}, nil))
	rtSvc2.SetRuntimeManager(localruntime.NewManager())
	s := &Server{runtimesSvc: rtSvc2}
	resp, err := s.GetModelRAMEstimate(context.Background(), &proto.GetModelRAMEstimateRequest{
		ModelId: "nope",
	})
	if err != nil {
		t.Fatalf("expected soft failure, got RPC error: %v", err)
	}
	if resp.GetError() == "" {
		t.Error("expected Error to be set for unknown model")
	}
}

func TestGetModelRAMEstimate_OnlineWithoutCatalogSoftFails(t *testing.T) {
	s := &Server{}
	resp, err := s.GetModelRAMEstimate(context.Background(), &proto.GetModelRAMEstimateRequest{
		CatalogId: "qwen2.5-coder:7b",
	})
	if err != nil {
		t.Fatalf("expected soft failure, got RPC error: %v", err)
	}
	if resp.GetError() == "" {
		t.Error("expected Error to be set when catalog manager is absent")
	}
}

func TestGetModelRAMEstimate_EmptyRequestSoftFails(t *testing.T) {
	s := &Server{}
	resp, err := s.GetModelRAMEstimate(context.Background(), &proto.GetModelRAMEstimateRequest{})
	if err != nil {
		t.Fatalf("expected soft failure, got RPC error: %v", err)
	}
	if resp.GetError() == "" {
		t.Error("expected Error for empty request")
	}
}

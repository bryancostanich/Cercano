package llamaserver

import (
	"cercano/source/server/internal/gguf"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
	"fmt"
	"math"
	"reflect"
	"testing"
)

func TestAutomaticMemoryRequiresEvidence(t *testing.T) {
	p := NewProvider(config.LlamaServerConfig{})
	p.totalRAM = func() int64 { return 128 << 30 }
	p.nonEvictable = func() (int64, bool) { return 0, true }
	_, err := p.checkMemoryBudget(localruntime.ModelRecord{ID: "unknown", SizeBytes: 1 << 30})
	if err == nil {
		t.Fatal("automatic launch accepted missing context and memory metadata")
	}
}

func TestContextAliasCannotOverrideChosenSize(t *testing.T) {
	p := NewProvider(config.LlamaServerConfig{})
	n := 65536
	args := p.argsFor(config.LlamaServerConfig{ContextSize: &n, ExtraArgs: []string{"-c", "8192", "--ctx-size=4096"}}, localruntime.ModelRecord{}, 1234)
	for _, a := range args {
		if a == "-c" || a == "--ctx-size=4096" {
			t.Fatalf("unmanaged override survived: %v", args)
		}
	}
	assertSingleCtxSize(t, args, n)
}

func TestPlannedContextPrecedenceAndEvidence(t *testing.T) {
	meta := &gguf.Meta{Architecture: "llama", ContextLength: 131072, BlockCount: 2, HeadCount: 4, HeadCountKV: 2, EmbeddingLength: 128}
	model := localruntime.ModelRecord{ID: "fixture", ContextSize: 65536, ExtraArgs: []string{"-c=32768"}}
	n := 8192
	cfg := config.LlamaServerConfig{ContextSize: &n}
	for _, want := range []struct {
		n      int
		source string
	}{{8192, "config"}, {65536, "ram_profile"}, {32768, "model_args"}, {131072, "gguf"}} {
		plan, err := resolvePlannedContext(cfg, model, meta, nil)
		if err != nil || plan.Tokens != want.n || plan.Source != want.source || plan.KVBytesPerToken != 512 {
			t.Fatalf("plan=%+v err=%v want=%+v", plan, err, want)
		}
		switch want.source {
		case "config":
			cfg.ContextSize = nil
		case "ram_profile":
			model.ContextSize = 0
		case "model_args":
			model.ExtraArgs = nil
		}
	}
	if _, err := resolvePlannedContext(cfg, model, nil, fmt.Errorf("missing")); err == nil {
		t.Fatal("missing automatic metadata accepted")
	}
	cfg.ContextSize = &n
	if plan, err := resolvePlannedContext(cfg, model, nil, fmt.Errorf("missing")); err != nil || plan.Tokens != 8192 {
		t.Fatalf("explicit override unavailable: %+v %v", plan, err)
	}
	meta.ContextLength = math.MaxUint64
	cfg.ContextSize = nil
	if _, err := resolvePlannedContext(cfg, model, meta, nil); err == nil {
		t.Fatal("oversized native context accepted")
	}
	meta.ContextLength = 131072
	meta.BlockCount = math.MaxUint64
	if _, err := resolvePlannedContext(cfg, model, meta, nil); err == nil {
		t.Fatal("overflowing KV metadata accepted")
	}
}

func TestPlannedAllocationUsedForGuardAndArgs(t *testing.T) {
	p := NewProvider(config.LlamaServerConfig{})
	p.totalRAM = func() int64 { return 16 << 30 }
	p.nonEvictable = func() (int64, bool) { return 1 << 30, true }
	plan := PlannedContext{Tokens: 65536, Source: "gguf", Automatic: true, KVBytesPerToken: 512}
	model := localruntime.ModelRecord{ID: "fixture", SizeBytes: 1 << 30}
	projection, err := p.checkPlannedMemory(model, plan)
	if err != nil {
		t.Fatal(err)
	}
	if projection.ContextTokens != 65536 || projection.KVBytes != 33554432 {
		t.Fatalf("wrong allocation %+v", projection)
	}
	n := plan.Tokens
	cfg := config.LlamaServerConfig{ContextSize: &n}
	assertSingleCtxSize(t, p.argsFor(cfg, model, 1234), projection.ContextTokens)
	plan.Tokens = 16 << 20 // 8 GiB KV + 2 GiB current/model > 6 GiB usable
	if _, err := p.checkPlannedMemory(model, plan); err == nil {
		t.Fatal("unsafe allocation accepted")
	}
	p.totalRAM = func() int64 { return 0 }
	if _, err := p.checkPlannedMemory(model, plan); err == nil {
		t.Fatal("unknown RAM accepted for automatic allocation")
	}
}

func TestContextArgumentsAndEnvironment(t *testing.T) {
	for _, args := range [][]string{{"--ctx-size"}, {"-c", "0"}, {"--ctx-size=nope"}, {"-c=-1"}} {
		if _, _, err := contextArgs(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	clean, n, err := contextArgs([]string{"--jinja", "-c=8192", "--ctx-size", "65536"})
	if err != nil || n != 65536 || !reflect.DeepEqual(clean, []string{"--jinja"}) {
		t.Fatalf("%v %d %v", clean, n, err)
	}
	env := managedEnvironment([]string{"PATH=/bin", "LLAMA_ARG_CTX_SIZE=0", "LLAMA_ARG_CACHE_TYPE_K=f32", "LLAMA_ARG_CACHE_TYPE_V=f32"})
	if !reflect.DeepEqual(env, []string{"PATH=/bin"}) {
		t.Fatalf("unmanaged environment: %v", env)
	}
}

func TestNativeContextReadFromGGUFFixture(t *testing.T) {
	path := writeIdentityGGUF(t, t.TempDir())
	p := NewProvider(config.LlamaServerConfig{})
	plan, err := p.planContext(p.snapshot(), localruntime.ModelRecord{ID: "fixture", Path: path})
	if err != nil || plan.Tokens != 2048 || plan.Source != "gguf" || plan.KVBytesPerToken <= 0 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestExplicitF32CacheHasConservativeMemoryEstimate(t *testing.T) {
	n := 8192
	cfg := config.LlamaServerConfig{ContextSize: &n, ExtraArgs: []string{"--cache-type-k=f32"}}
	meta := &gguf.Meta{ContextLength: 8192, BlockCount: 2, HeadCount: 4, HeadCountKV: 2, EmbeddingLength: 128}
	plan, err := resolvePlannedContext(cfg, localruntime.ModelRecord{}, meta, nil)
	if err != nil || plan.KVBytesPerToken != 1024 {
		t.Fatalf("explicit f32 plan=%+v err=%v", plan, err)
	}
}

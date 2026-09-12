package llamaserver

import (
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"cercano/source/server/internal/gguf"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
)

// PlannedContext is an allocation decision, not observed serving capacity.
type PlannedContext struct {
	Tokens          int
	Source          string
	KVBytesPerToken int64
	Automatic       bool
}

// contextArgs removes all supported context aliases and extracts the last
// model-specific value. A malformed model setting is an error, not a fallback.
func contextArgs(args []string) ([]string, int, error) {
	out := make([]string, 0, len(args))
	size := 0
	for i := 0; i < len(args); i++ {
		name, value, inline := strings.Cut(args[i], "=")
		if name != "--ctx-size" && name != "-c" {
			out = append(out, args[i])
			continue
		}
		if !inline {
			i++
			if i >= len(args) {
				return nil, 0, fmt.Errorf("%s requires a positive context size", name)
			}
			value = args[i]
		}
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return nil, 0, fmt.Errorf("invalid context argument %s=%q", name, value)
		}
		size = n
	}
	return out, size, nil
}

func readContextMeta(path string) (*gguf.Meta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return gguf.ParseMeta(io.LimitReader(f, identityHeaderWindow))
}

// resolvePlannedContext uses metadata only after explicit/profile/model policy.
// Metadata still supplies the memory evidence for automatic allocations.
func resolvePlannedContext(cfg config.LlamaServerConfig, model localruntime.ModelRecord, meta *gguf.Meta, metaErr error) (PlannedContext, error) {
	plan := PlannedContext{Automatic: cfg.ContextSize == nil}
	fail := func(reason string) (PlannedContext, error) {
		return plan, fmt.Errorf("llama-server context for %s: %s; supply a positive llama_server.context_size override or repair model metadata", model.ID, reason)
	}
	if err := cfg.Validate(); err != nil {
		return plan, err
	}
	if _, _, err := contextArgs(cfg.ExtraArgs); err != nil {
		return plan, err
	}
	_, modelSize, err := contextArgs(model.ExtraArgs)
	if err != nil {
		return plan, err
	}
	switch {
	case cfg.ContextSize != nil:
		plan.Tokens = *cfg.ContextSize
		plan.Source = "config"
	case model.ContextSize > 0:
		plan.Tokens = model.ContextSize
		plan.Source = "ram_profile"
	case modelSize > 0:
		plan.Tokens = modelSize
		plan.Source = "model_args"
	case meta != nil && meta.ContextLength > 0 && meta.ContextLength <= uint64(math.MaxInt):
		plan.Tokens = int(meta.ContextLength)
		plan.Source = "gguf"
	default:
		return fail("no valid automatic context candidate")
	}
	if metaErr != nil || meta == nil {
		if plan.Automatic {
			return fail("missing memory-sizing metadata")
		}
		return plan, nil
	}
	perToken, err := checkedKVBytes(meta)
	if err != nil {
		return fail(err.Error())
	}
	if perToken == 0 && plan.Automatic {
		return fail("missing KV memory-sizing evidence")
	}
	// Use a conservative upper bound for recognized cache formats: f32 costs
	// twice f16. Unknown formats cannot be validated with this estimator.
	wideCache := false
	for _, args := range [][]string{cfg.ExtraArgs, model.ExtraArgs} {
		for i := 0; i < len(args); i++ {
			name, value, inline := strings.Cut(args[i], "=")
			if name != "--cache-type-k" && name != "--cache-type-v" && name != "-ctk" && name != "-ctv" {
				continue
			}
			if !inline {
				i++
				if i >= len(args) {
					return fail("cache type is missing")
				}
				value = args[i]
			}
			switch value {
			case "f32":
				wideCache = true
			case "f16", "bf16", "q8_0", "q4_0", "q4_1", "q5_0", "q5_1", "iq4_nl":
			default:
				return fail("unsupported cache memory estimate for " + value)
			}
		}
	}
	if wideCache {
		if perToken > math.MaxInt64/2 {
			return fail("f32 KV estimate overflows memory accounting")
		}
		perToken *= 2
	}
	if perToken > 0 && int64(plan.Tokens) > math.MaxInt64/perToken {
		return fail("KV allocation overflows memory accounting")
	}
	plan.KVBytesPerToken = perToken
	return plan, nil
}

func checkedKVBytes(m *gguf.Meta) (int64, error) {
	multiply := func(a, b uint64) (uint64, error) {
		if b > 0 && a > math.MaxInt64/b {
			return 0, fmt.Errorf("KV metadata overflows memory accounting")
		}
		return a * b, nil
	}
	heads := m.KVHeadsTotal
	var err error
	if heads == 0 {
		heads, err = multiply(m.BlockCount, m.HeadCountKV)
		if err != nil {
			return 0, err
		}
	}
	k, v := m.KeyLength, m.ValueLength
	if k == 0 || v == 0 {
		if m.HeadCount == 0 {
			return 0, nil
		}
		dim := m.EmbeddingLength / m.HeadCount
		if k == 0 {
			k = dim
		}
		if v == 0 {
			v = dim
		}
	}
	if k > math.MaxInt64 || v > math.MaxInt64-k {
		return 0, fmt.Errorf("KV dimensions overflow memory accounting")
	}
	n, err := multiply(heads, k+v)
	if err != nil {
		return 0, err
	}
	n, err = multiply(n, 2)
	return int64(n), err
}

func (p *Provider) planContext(cfg config.LlamaServerConfig, model localruntime.ModelRecord) (PlannedContext, error) {
	meta, err := readContextMeta(model.Path)
	return resolvePlannedContext(cfg, model, meta, err)
}

// managedEnvironment prevents inherited llama.cpp settings from bypassing the
// checked context/cache allocation. Other environment entries remain unchanged.
func managedEnvironment(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key == "LLAMA_ARG_CTX_SIZE" || key == "LLAMA_ARG_CACHE_TYPE_K" || key == "LLAMA_ARG_CACHE_TYPE_V" {
			continue
		}
		out = append(out, entry)
	}
	return out
}

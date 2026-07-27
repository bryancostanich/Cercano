package config

// MistralRSMemoryDefaults contains RAM-aware launch caps for mistral.rs. These
// values are deliberately conservative: mistral.rs/Metal allocations live in
// IOAccelerator unified memory, not ordinary RSS, so an uncapped long-context
// model can push a 128 GB Mac deep into swap while ps still reports tiny RSS.
type MistralRSMemoryDefaults struct {
	PagedAttn        string
	PAMemoryFraction string
	MaxSeqLen        int
	MaxSeqs          int
	MaxBatchSize     int
}

// MistralRSMemoryDefaultsForRAM returns safe defaults for a machine with the
// given physical RAM in bytes. Unknown RAM (<=0) uses the 32-63 GB tier rather
// than risking the model-advertised context window.
func MistralRSMemoryDefaultsForRAM(bytes uint64) MistralRSMemoryDefaults {
	const gib = uint64(1024 * 1024 * 1024)
	gb := bytes / gib
	// PagedAttn stays "auto" here ON PURPOSE — do not change it to "on".
	// This file is platform-agnostic (it compiles everywhere), but mistral.rs's
	// "auto" mode DISABLES PagedAttention on Metal/CPU, which turns off the
	// KV-cache memory governor exactly where these fraction caps matter most.
	// The fix lives at the platform-aware layer: the provider's argsFor()
	// translates "auto" -> "on" only where PagedAttention is actually supported
	// (Metal on darwin/arm64, CUDA on linux/windows). Keeping the stored value
	// "auto" keeps a config portable across machines; hardcoding "on" here would
	// make a Metal-authored config error on a CPU-only host.
	out := MistralRSMemoryDefaults{
		PagedAttn:    "auto",
		MaxSeqs:      1,
		MaxBatchSize: 1,
	}
	switch {
	case bytes > 0 && gb < 32:
		out.MaxSeqLen = 8192
		out.PAMemoryFraction = "0.20"
	case bytes > 0 && gb < 64:
		out.MaxSeqLen = 16384
		out.PAMemoryFraction = "0.25"
	case bytes > 0 && gb < 128:
		out.MaxSeqLen = 32768
		out.PAMemoryFraction = "0.30"
	case bytes >= 128*gib:
		// Even on 128 GB machines, start at 32k rather than 64k. The 30B
		// qwen model advertises a 262k context, and allowing mistral.rs to size
		// buffers from that blew past physical memory into ~180 GB Metal
		// footprint. Users can opt up explicitly after observing headroom.
		out.MaxSeqLen = 32768
		out.PAMemoryFraction = "0.35"
	default:
		out.MaxSeqLen = 16384
		out.PAMemoryFraction = "0.25"
	}
	return out
}

func defaultMistralRSMemoryDefaults() MistralRSMemoryDefaults {
	return MistralRSMemoryDefaultsForRAM(detectPhysicalMemoryBytes())
}

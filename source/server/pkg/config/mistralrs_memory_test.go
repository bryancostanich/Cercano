package config

import "testing"

func TestMistralRSMemoryDefaultsForRAM(t *testing.T) {
	const gib = uint64(1024 * 1024 * 1024)
	tests := []struct {
		name     string
		ram      uint64
		seq      int
		fraction string
	}{
		{name: "unknown", ram: 0, seq: 16384, fraction: "0.25"},
		{name: "small", ram: 16 * gib, seq: 8192, fraction: "0.20"},
		{name: "mid", ram: 32 * gib, seq: 16384, fraction: "0.25"},
		{name: "large", ram: 64 * gib, seq: 32768, fraction: "0.30"},
		{name: "huge", ram: 128 * gib, seq: 32768, fraction: "0.35"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MistralRSMemoryDefaultsForRAM(tt.ram)
			if got.PagedAttn != "auto" || got.MaxSeqs != 1 || got.MaxBatchSize != 1 {
				t.Fatalf("unexpected fixed defaults: %+v", got)
			}
			if got.MaxSeqLen != tt.seq || got.PAMemoryFraction != tt.fraction {
				t.Fatalf("defaults for %s = %+v, want seq=%d fraction=%s", tt.name, got, tt.seq, tt.fraction)
			}
		})
	}
}

func TestApplyMistralRSDefaultsFillsSafetyCaps(t *testing.T) {
	defaults := MistralRSConfig{
		ModelDirs:        []string{"~/.cercano/models"},
		Host:             "127.0.0.1",
		PagedAttn:        "auto",
		PAMemoryFraction: "0.35",
		MaxSeqLen:        32768,
		MaxSeqs:          1,
		MaxBatchSize:     1,
	}
	cfg := MistralRSConfig{}
	applyMistralRSDefaults(&cfg, defaults)
	if cfg.PagedAttn != "auto" || cfg.PAMemoryFraction != "0.35" || cfg.MaxSeqLen != 32768 || cfg.MaxSeqs != 1 || cfg.MaxBatchSize != 1 {
		t.Fatalf("missing safety defaults: %+v", cfg)
	}
}

func TestApplyMistralRSDefaultsPreservesExplicitSafetyCaps(t *testing.T) {
	defaults := MistralRSConfig{PagedAttn: "auto", PAMemoryFraction: "0.35", MaxSeqLen: 32768, MaxSeqs: 1, MaxBatchSize: 1}
	cfg := MistralRSConfig{PagedAttn: "off", PAMemoryFraction: "0.10", MaxSeqLen: 4096, MaxSeqs: 3, MaxBatchSize: 2}
	applyMistralRSDefaults(&cfg, defaults)
	if cfg.PagedAttn != "off" || cfg.PAMemoryFraction != "0.10" || cfg.MaxSeqLen != 4096 || cfg.MaxSeqs != 3 || cfg.MaxBatchSize != 2 {
		t.Fatalf("explicit values overwritten: %+v", cfg)
	}
}

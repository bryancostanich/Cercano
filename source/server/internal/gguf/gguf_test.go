package gguf

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

// ggufBuilder assembles a synthetic GGUF header for tests.
type ggufBuilder struct {
	buf     bytes.Buffer
	kvCount uint64
	kvs     bytes.Buffer
}

func (b *ggufBuilder) writeStringTo(w *bytes.Buffer, s string) {
	binary.Write(w, binary.LittleEndian, uint64(len(s)))
	w.WriteString(s)
}

func (b *ggufBuilder) addString(key, val string) *ggufBuilder {
	b.writeStringTo(&b.kvs, key)
	binary.Write(&b.kvs, binary.LittleEndian, uint32(typeString))
	b.writeStringTo(&b.kvs, val)
	b.kvCount++
	return b
}

func (b *ggufBuilder) addUint32(key string, val uint32) *ggufBuilder {
	b.writeStringTo(&b.kvs, key)
	binary.Write(&b.kvs, binary.LittleEndian, uint32(typeUint32))
	binary.Write(&b.kvs, binary.LittleEndian, val)
	b.kvCount++
	return b
}

func (b *ggufBuilder) addUint64(key string, val uint64) *ggufBuilder {
	b.writeStringTo(&b.kvs, key)
	binary.Write(&b.kvs, binary.LittleEndian, uint32(typeUint64))
	binary.Write(&b.kvs, binary.LittleEndian, val)
	b.kvCount++
	return b
}

func (b *ggufBuilder) addFloat32(key string, val float32) *ggufBuilder {
	b.writeStringTo(&b.kvs, key)
	binary.Write(&b.kvs, binary.LittleEndian, uint32(typeFloat32))
	binary.Write(&b.kvs, binary.LittleEndian, val)
	b.kvCount++
	return b
}

func (b *ggufBuilder) addUint32Array(key string, vals []uint32) *ggufBuilder {
	b.writeStringTo(&b.kvs, key)
	binary.Write(&b.kvs, binary.LittleEndian, uint32(typeArray))
	binary.Write(&b.kvs, binary.LittleEndian, uint32(typeUint32))
	binary.Write(&b.kvs, binary.LittleEndian, uint64(len(vals)))
	for _, v := range vals {
		binary.Write(&b.kvs, binary.LittleEndian, v)
	}
	b.kvCount++
	return b
}

func (b *ggufBuilder) addStringArray(key string, vals []string) *ggufBuilder {
	b.writeStringTo(&b.kvs, key)
	binary.Write(&b.kvs, binary.LittleEndian, uint32(typeArray))
	binary.Write(&b.kvs, binary.LittleEndian, uint32(typeString))
	binary.Write(&b.kvs, binary.LittleEndian, uint64(len(vals)))
	for _, v := range vals {
		b.writeStringTo(&b.kvs, v)
	}
	b.kvCount++
	return b
}

func (b *ggufBuilder) bytes() []byte {
	b.buf.Reset()
	binary.Write(&b.buf, binary.LittleEndian, uint32(ggufMagic))
	binary.Write(&b.buf, binary.LittleEndian, uint32(3))
	binary.Write(&b.buf, binary.LittleEndian, uint64(0)) // tensor count
	binary.Write(&b.buf, binary.LittleEndian, b.kvCount)
	b.buf.Write(b.kvs.Bytes())
	return b.buf.Bytes()
}

// qwenStyle builds a header shaped like qwen2.5-coder:7b — 28 layers,
// 28 attention heads, 4 KV heads, embedding 3584 (head_dim 128),
// max context 32768.
func qwenStyle() *ggufBuilder {
	b := &ggufBuilder{}
	b.addString("general.architecture", "qwen2").
		addString("general.name", "Qwen2.5 Coder 7B").
		addUint32("qwen2.block_count", 28).
		addUint32("qwen2.context_length", 32768).
		addUint32("qwen2.embedding_length", 3584).
		addUint32("qwen2.attention.head_count", 28).
		addUint32("qwen2.attention.head_count_kv", 4).
		addFloat32("qwen2.attention.layer_norm_rms_epsilon", 1e-6)
	return b
}

func TestParseMeta_QwenStyleFields(t *testing.T) {
	meta, err := ParseMeta(bytes.NewReader(qwenStyle().bytes()))
	if err != nil {
		t.Fatalf("ParseMeta: %v", err)
	}
	if meta.Architecture != "qwen2" {
		t.Errorf("Architecture = %q", meta.Architecture)
	}
	if meta.BlockCount != 28 || meta.ContextLength != 32768 || meta.EmbeddingLength != 3584 {
		t.Errorf("core fields = %d/%d/%d", meta.BlockCount, meta.ContextLength, meta.EmbeddingLength)
	}
	if meta.HeadCount != 28 || meta.HeadCountKV != 4 {
		t.Errorf("head counts = %d/%d", meta.HeadCount, meta.HeadCountKV)
	}
}

func TestKVBytesPerToken_QwenMath(t *testing.T) {
	meta, err := ParseMeta(bytes.NewReader(qwenStyle().bytes()))
	if err != nil {
		t.Fatalf("ParseMeta: %v", err)
	}
	// 28 layers x 4 kv heads x (128 + 128) dims x 2 bytes = 57344.
	if got := meta.KVBytesPerToken(); got != 57344 {
		t.Errorf("KVBytesPerToken = %d, want 57344", got)
	}
}

func TestParseMeta_StopsBeforeTruncatedTokenizerArray(t *testing.T) {
	// All architecture keys first, then a tokenizer key whose array the
	// window cuts off mid-way. The parser must return success without
	// reading past the tokenizer boundary.
	b := qwenStyle()
	b.addStringArray("tokenizer.ggml.tokens", []string{"<pad>", "<eos>", "hello"})
	full := b.bytes()
	truncated := full[:len(full)-10] // cut inside the token array
	meta, err := ParseMeta(bytes.NewReader(truncated))
	if err != nil {
		t.Fatalf("ParseMeta on truncated window: %v", err)
	}
	if meta.KVBytesPerToken() == 0 {
		t.Error("expected a usable estimate from the truncated window")
	}
}

func TestParseMeta_GemmaKeyLengthOverride(t *testing.T) {
	// Gemma-style: head_dim is 256, NOT embedding/head_count (2304/8=288).
	b := &ggufBuilder{}
	b.addString("general.architecture", "gemma2").
		addUint32("gemma2.block_count", 26).
		addUint32("gemma2.context_length", 8192).
		addUint32("gemma2.embedding_length", 2304).
		addUint32("gemma2.attention.head_count", 8).
		addUint32("gemma2.attention.head_count_kv", 4).
		addUint32("gemma2.attention.key_length", 256).
		addUint32("gemma2.attention.value_length", 256)
	meta, err := ParseMeta(bytes.NewReader(b.bytes()))
	if err != nil {
		t.Fatalf("ParseMeta: %v", err)
	}
	// 26 x 4 x (256+256) x 2 = 106496 — NOT 26 x 4 x (288+288) x 2.
	if got := meta.KVBytesPerToken(); got != 106496 {
		t.Errorf("KVBytesPerToken = %d, want 106496 (key_length override ignored?)", got)
	}
}

func TestParseMeta_HeadCountKVArraySums(t *testing.T) {
	b := &ggufBuilder{}
	b.addString("general.architecture", "llama").
		addUint32("llama.block_count", 4).
		addUint32("llama.context_length", 4096).
		addUint32("llama.embedding_length", 4096).
		addUint32("llama.attention.head_count", 32).
		addUint32Array("llama.attention.head_count_kv", []uint32{8, 8, 8, 8})
	meta, err := ParseMeta(bytes.NewReader(b.bytes()))
	if err != nil {
		t.Fatalf("ParseMeta: %v", err)
	}
	if meta.KVHeadsTotal != 32 {
		t.Errorf("KVHeadsTotal = %d, want 32", meta.KVHeadsTotal)
	}
	// Uniform array: same result as scalar form — 32 total heads x
	// (128+128) dims x 2 bytes.
	if got := meta.KVBytesPerToken(); got != 16384 {
		t.Errorf("KVBytesPerToken = %d, want 16384", got)
	}
}

// qwen3next-style hybrid: most layers are linear attention (0 KV
// heads), a few carry full attention. The first element being 0 must
// not fail the parse (the original bug), and the sum must drive the
// estimate.
func TestParseMeta_HybridAttentionZeroFirstLayer(t *testing.T) {
	heads := make([]uint32, 48)
	for i := 3; i < 48; i += 4 {
		heads[i] = 2 // every 4th layer has 2 KV heads -> sum 24
	}
	b := &ggufBuilder{}
	b.addString("general.architecture", "qwen3next").
		addUint32("qwen3next.block_count", 48).
		addUint32("qwen3next.context_length", 262144).
		addUint32("qwen3next.embedding_length", 2048).
		addUint32("qwen3next.attention.head_count", 16).
		addUint32Array("qwen3next.attention.head_count_kv", heads)
	meta, err := ParseMeta(bytes.NewReader(b.bytes()))
	if err != nil {
		t.Fatalf("ParseMeta on hybrid model: %v", err)
	}
	if meta.KVHeadsTotal != 24 {
		t.Errorf("KVHeadsTotal = %d, want 24", meta.KVHeadsTotal)
	}
	// 24 total heads x (128+128) dims x 2 bytes = 12288.
	if got := meta.KVBytesPerToken(); got != 12288 {
		t.Errorf("KVBytesPerToken = %d, want 12288", got)
	}
}

func TestParseMeta_UintTypeFlexibility(t *testing.T) {
	// Same keys emitted as uint64 — some converters do this.
	b := &ggufBuilder{}
	b.addString("general.architecture", "llama").
		addUint64("llama.block_count", 32).
		addUint64("llama.context_length", 131072).
		addUint64("llama.embedding_length", 4096).
		addUint64("llama.attention.head_count", 32).
		addUint64("llama.attention.head_count_kv", 8)
	meta, err := ParseMeta(bytes.NewReader(b.bytes()))
	if err != nil {
		t.Fatalf("ParseMeta: %v", err)
	}
	if meta.ContextLength != 131072 {
		t.Errorf("ContextLength = %d", meta.ContextLength)
	}
}

func TestParseMeta_BadMagic(t *testing.T) {
	data := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}
	if _, err := ParseMeta(bytes.NewReader(data)); err == nil {
		t.Fatal("expected error for bad magic")
	}
}

func TestParseMeta_UnsupportedVersion(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(ggufMagic))
	binary.Write(&buf, binary.LittleEndian, uint32(1))
	if _, err := ParseMeta(&buf); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("expected version error, got %v", err)
	}
}

func TestParseMeta_WindowEndsBeforeRequiredKeys(t *testing.T) {
	// Architecture present but block_count et al never appear before EOF.
	b := &ggufBuilder{}
	b.addString("general.architecture", "qwen2").
		addString("general.name", "incomplete")
	_, err := ParseMeta(bytes.NewReader(b.bytes()))
	if err == nil {
		t.Fatal("expected error for missing required keys")
	}
	if !strings.Contains(err.Error(), "required architecture keys") {
		t.Errorf("error should name the failure mode: %v", err)
	}
}

func TestParseMeta_TruncatedMidValue(t *testing.T) {
	full := qwenStyle().bytes()
	// Cut inside the KV section, before required keys are complete.
	_, err := ParseMeta(bytes.NewReader(full[:40]))
	if err == nil {
		t.Fatal("expected error for truncated header")
	}
}

func TestKVBytesPerToken_ZeroOnIncompleteMeta(t *testing.T) {
	m := &Meta{}
	if got := m.KVBytesPerToken(); got != 0 {
		t.Errorf("empty meta KVBytesPerToken = %d, want 0", got)
	}
}

// Sanity check that io semantics behave: a reader that yields exactly
// the header then EOF must not error.
func TestParseMeta_ExactWindow(t *testing.T) {
	data := qwenStyle().bytes()
	meta, err := ParseMeta(io.LimitReader(bytes.NewReader(data), int64(len(data))))
	if err != nil {
		t.Fatalf("ParseMeta: %v", err)
	}
	if !meta.complete() {
		t.Error("meta should be complete")
	}
}

// Package gguf parses just enough of a GGUF file header to estimate
// runtime memory: the architecture keys that determine KV-cache cost
// per token and the model's maximum context length.
//
// It reads from a plain io.Reader and stops as soon as it has what it
// needs, so callers can hand it a bounded window (the first 256 KiB of
// a file, or an HTTP Range response) without downloading whole models.
// The architecture keys are written near the front of the metadata
// section in practice — before the multi-megabyte tokenizer arrays —
// which is what makes the ranged-fetch strategy work.
package gguf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const ggufMagic = 0x46554747 // "GGUF" little-endian

// GGUF metadata value types (spec: ggml/docs/gguf.md).
const (
	typeUint8   = 0
	typeInt8    = 1
	typeUint16  = 2
	typeInt16   = 3
	typeUint32  = 4
	typeInt32   = 5
	typeFloat32 = 6
	typeBool    = 7
	typeString  = 8
	typeArray   = 9
	typeUint64  = 10
	typeInt64   = 11
	typeFloat64 = 12
)

// maxStringLen guards against a corrupt header making us try to skip
// gigabytes: no metadata key or string value we care about is remotely
// this long, and the tokenizer arrays we skip element-by-element.
const maxStringLen = 1 << 20

// Meta holds the header fields needed for RAM estimation.
type Meta struct {
	Architecture    string
	BlockCount      uint64
	ContextLength   uint64
	EmbeddingLength uint64
	HeadCount       uint64
	HeadCountKV     uint64
	// KeyLength / ValueLength override the per-head K/V dimension when
	// present (e.g. gemma uses head_dim 256 which is NOT
	// embedding/head_count). Zero means "derive from embedding length".
	KeyLength   uint64
	ValueLength uint64
	// KVHeadsTotal is the sum of KV heads across all layers, set when
	// head_count_kv is a per-layer array — hybrid architectures like
	// qwen3next interleave linear-attention layers (0 KV heads) with
	// full-attention layers, so no uniform per-layer value exists.
	// Zero means "uniform: derive as BlockCount * HeadCountKV".
	KVHeadsTotal uint64
}

// kvHeadsTotal is the total KV-head count across all layers: the
// per-layer array sum when present (hybrid architectures), otherwise
// uniform layers x per-layer heads.
func (m *Meta) kvHeadsTotal() uint64 {
	if m.KVHeadsTotal > 0 {
		return m.KVHeadsTotal
	}
	return m.BlockCount * m.HeadCountKV
}

// KVBytesPerToken returns the KV-cache cost of one context token in
// bytes, assuming the llama.cpp default f16 cache (2 bytes/element):
// total_kv_heads x (key_dim + value_dim) x 2.
func (m *Meta) KVBytesPerToken() int64 {
	total := m.kvHeadsTotal()
	if total == 0 {
		return 0
	}
	keyDim := m.KeyLength
	valDim := m.ValueLength
	if keyDim == 0 || valDim == 0 {
		if m.HeadCount == 0 {
			return 0
		}
		headDim := m.EmbeddingLength / m.HeadCount
		if keyDim == 0 {
			keyDim = headDim
		}
		if valDim == 0 {
			valDim = headDim
		}
	}
	return int64(total) * int64(keyDim+valDim) * 2
}

// complete reports whether every field required for an estimate is set.
// KeyLength/ValueLength stay optional — most architectures derive them.
// The KV-head requirement is satisfied by either form: a uniform
// scalar (HeadCountKV) or a per-layer array sum (KVHeadsTotal).
func (m *Meta) complete() bool {
	return m.Architecture != "" &&
		m.BlockCount != 0 &&
		m.ContextLength != 0 &&
		m.EmbeddingLength != 0 &&
		m.HeadCount != 0 &&
		(m.HeadCountKV != 0 || m.KVHeadsTotal != 0)
}

// ParseMeta reads GGUF header metadata from r until it has all the
// architecture keys required for a RAM estimate, then returns without
// consuming the rest. If r ends (bounded window) before the required
// keys appear, it returns an error wrapping io.ErrUnexpectedEOF.
func ParseMeta(r io.Reader) (*Meta, error) {
	var magic, version uint32
	if err := binary.Read(r, binary.LittleEndian, &magic); err != nil {
		return nil, fmt.Errorf("gguf: read magic: %w", err)
	}
	if magic != ggufMagic {
		return nil, fmt.Errorf("gguf: bad magic 0x%08x (not a GGUF file)", magic)
	}
	if err := binary.Read(r, binary.LittleEndian, &version); err != nil {
		return nil, fmt.Errorf("gguf: read version: %w", err)
	}
	if version < 2 || version > 3 {
		return nil, fmt.Errorf("gguf: unsupported version %d", version)
	}
	var tensorCount, kvCount uint64
	if err := binary.Read(r, binary.LittleEndian, &tensorCount); err != nil {
		return nil, fmt.Errorf("gguf: read tensor count: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &kvCount); err != nil {
		return nil, fmt.Errorf("gguf: read kv count: %w", err)
	}

	meta := &Meta{}
	for i := uint64(0); i < kvCount; i++ {
		key, err := readString(r)
		if err != nil {
			if meta.complete() {
				return meta, nil
			}
			return nil, incomplete(fmt.Errorf("gguf: read key %d: %w", i, err))
		}
		// The optional key/value-length overrides sit adjacent to the
		// other attention keys; once we're past the architecture block
		// and into the tokenizer section they will not appear, so stop
		// before touching the multi-megabyte vocab arrays (which a
		// bounded window has likely truncated anyway).
		if meta.complete() && isTokenizerKey(key) {
			return meta, nil
		}
		var valType uint32
		if err := binary.Read(r, binary.LittleEndian, &valType); err != nil {
			if meta.complete() {
				return meta, nil
			}
			return nil, incomplete(fmt.Errorf("gguf: read type of %q: %w", key, err))
		}
		if err := meta.consume(r, key, valType); err != nil {
			if meta.complete() {
				return meta, nil
			}
			return nil, incomplete(err)
		}
		if meta.complete() && meta.KeyLength != 0 && meta.ValueLength != 0 {
			return meta, nil
		}
	}
	if meta.complete() {
		return meta, nil
	}
	return nil, incomplete(nil)
}

// incomplete builds the "header window ended early" error.
func incomplete(cause error) error {
	if cause == nil {
		cause = io.ErrUnexpectedEOF
	}
	return fmt.Errorf("gguf: header window ended before required architecture keys were found: %w", cause)
}

// consume either records a value we care about or skips it.
func (m *Meta) consume(r io.Reader, key string, valType uint32) error {
	switch {
	case key == "general.architecture":
		s, err := readString(r)
		if err != nil {
			return fmt.Errorf("gguf: read architecture: %w", err)
		}
		m.Architecture = s
		return nil
	case m.Architecture != "" && key == m.Architecture+".block_count":
		return m.readUint(r, valType, &m.BlockCount, key)
	case m.Architecture != "" && key == m.Architecture+".context_length":
		return m.readUint(r, valType, &m.ContextLength, key)
	case m.Architecture != "" && key == m.Architecture+".embedding_length":
		return m.readUint(r, valType, &m.EmbeddingLength, key)
	case m.Architecture != "" && key == m.Architecture+".attention.head_count":
		return m.readUint(r, valType, &m.HeadCount, key)
	case m.Architecture != "" && key == m.Architecture+".attention.head_count_kv":
		return m.readKVHeads(r, valType, key)
	case m.Architecture != "" && key == m.Architecture+".attention.key_length":
		return m.readUint(r, valType, &m.KeyLength, key)
	case m.Architecture != "" && key == m.Architecture+".attention.value_length":
		return m.readUint(r, valType, &m.ValueLength, key)
	default:
		return skipValue(r, valType)
	}
}

// readUint accepts any integer-typed value and stores it as uint64.
// Some converters emit uint32, others uint64 — both appear in the wild.
func (m *Meta) readUint(r io.Reader, valType uint32, dst *uint64, key string) error {
	switch valType {
	case typeUint8:
		var v uint8
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return err
		}
		*dst = uint64(v)
	case typeUint16:
		var v uint16
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return err
		}
		*dst = uint64(v)
	case typeUint32:
		var v uint32
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return err
		}
		*dst = uint64(v)
	case typeInt32:
		var v int32
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return err
		}
		if v < 0 {
			return fmt.Errorf("gguf: %s is negative (%d)", key, v)
		}
		*dst = uint64(v)
	case typeUint64:
		var v uint64
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return err
		}
		*dst = v
	case typeInt64:
		var v int64
		if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
			return err
		}
		if v < 0 {
			return fmt.Errorf("gguf: %s is negative (%d)", key, v)
		}
		*dst = uint64(v)
	case typeArray:
		// Array-valued scalars outside head_count_kv (which has its
		// own summing reader) are rare; take the first element as the
		// representative value and skip the rest.
		var elemType uint32
		var count uint64
		if err := binary.Read(r, binary.LittleEndian, &elemType); err != nil {
			return err
		}
		if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("gguf: %s is an empty array", key)
		}
		if err := m.readUint(r, elemType, dst, key); err != nil {
			return err
		}
		for i := uint64(1); i < count; i++ {
			if err := skipValue(r, elemType); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("gguf: %s has unexpected type %d", key, valType)
	}
	return nil
}

// readKVHeads handles both publication forms of attention.head_count_kv:
// a uniform scalar (stored in HeadCountKV), or a per-layer array —
// hybrid architectures like qwen3next publish one entry per layer,
// zero for the linear-attention layers — summed into KVHeadsTotal.
func (m *Meta) readKVHeads(r io.Reader, valType uint32, key string) error {
	if valType != typeArray {
		return m.readUint(r, valType, &m.HeadCountKV, key)
	}
	var elemType uint32
	var count uint64
	if err := binary.Read(r, binary.LittleEndian, &elemType); err != nil {
		return err
	}
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("gguf: %s is an empty array", key)
	}
	var total uint64
	for i := uint64(0); i < count; i++ {
		var v uint64
		if err := m.readUint(r, elemType, &v, key); err != nil {
			return err
		}
		total += v
	}
	m.KVHeadsTotal = total
	return nil
}

// skipValue discards one value of the given type.
func skipValue(r io.Reader, valType uint32) error {
	switch valType {
	case typeUint8, typeInt8, typeBool:
		return discard(r, 1)
	case typeUint16, typeInt16:
		return discard(r, 2)
	case typeUint32, typeInt32, typeFloat32:
		return discard(r, 4)
	case typeUint64, typeInt64, typeFloat64:
		return discard(r, 8)
	case typeString:
		var n uint64
		if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
			return err
		}
		if n > maxStringLen {
			return fmt.Errorf("gguf: string length %d exceeds sanity cap", n)
		}
		return discard(r, int64(n))
	case typeArray:
		var elemType uint32
		var count uint64
		if err := binary.Read(r, binary.LittleEndian, &elemType); err != nil {
			return err
		}
		if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
			return err
		}
		// Fixed-size elements skip in one CopyN; strings (and nested
		// arrays, which the spec permits but nothing emits) go one by
		// one.
		if size, fixed := fixedSize(elemType); fixed {
			return discard(r, int64(count)*size)
		}
		for i := uint64(0); i < count; i++ {
			if err := skipValue(r, elemType); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("gguf: unknown value type %d", valType)
	}
}

func fixedSize(valType uint32) (int64, bool) {
	switch valType {
	case typeUint8, typeInt8, typeBool:
		return 1, true
	case typeUint16, typeInt16:
		return 2, true
	case typeUint32, typeInt32, typeFloat32:
		return 4, true
	case typeUint64, typeInt64, typeFloat64:
		return 8, true
	default:
		return 0, false
	}
}

func discard(r io.Reader, n int64) error {
	if n == 0 {
		return nil
	}
	written, err := io.CopyN(io.Discard, r, n)
	if err != nil {
		if errors.Is(err, io.EOF) && written < n {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}

func readString(r io.Reader) (string, error) {
	var n uint64
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return "", err
	}
	if n > maxStringLen {
		return "", fmt.Errorf("gguf: string length %d exceeds sanity cap", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func isTokenizerKey(key string) bool {
	return len(key) >= 10 && key[:10] == "tokenizer."
}

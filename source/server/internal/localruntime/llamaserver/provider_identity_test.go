package llamaserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/pkg/config"
)

// writeIdentityGGUF writes a minimal GGUF whose header carries the
// identity keys — deliberately under a filename that defeats the old
// inference (hash-style stem, no quant suffix).
func writeIdentityGGUF(t *testing.T, dir string) string {
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
	addStr("general.architecture", "nomic-bert-moe")
	addStr("general.name", "Nomic Embed Text V2 MoE")
	addU32("general.file_type", 1) // F16
	addU32("nomic-bert-moe.block_count", 12)
	addU32("nomic-bert-moe.context_length", 2048)
	addU32("nomic-bert-moe.embedding_length", 768)
	addU32("nomic-bert-moe.attention.head_count", 12)

	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, uint32(0x46554747))
	binary.Write(&out, binary.LittleEndian, uint32(3))
	binary.Write(&out, binary.LittleEndian, uint64(0))
	binary.Write(&out, binary.LittleEndian, uint64(7))
	out.Write(kvs.Bytes())

	path := filepath.Join(dir, "f1f3470efd49.gguf")
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscover_IdentityFromHeaderNotFilename(t *testing.T) {
	dir := t.TempDir()
	writeIdentityGGUF(t, dir)
	provider := NewProvider(config.LlamaServerConfig{ModelDirs: []string{dir}})

	models, err := provider.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var rec *struct {
		display, family, quant string
		chat, embed            bool
	}
	for _, m := range models {
		if m.Source == "configured_path" {
			rec = &struct {
				display, family, quant string
				chat, embed            bool
			}{m.DisplayName, m.Family, m.Quantization, m.SupportsChat, m.SupportsEmbed}
		}
	}
	if rec == nil {
		t.Fatalf("no configured_path record in %d models", len(models))
	}
	if rec.display != "Nomic Embed Text V2 MoE" {
		t.Errorf("DisplayName = %q, want the header's general.name", rec.display)
	}
	if rec.family != "nomic-bert-moe" {
		t.Errorf("Family = %q, want the header architecture", rec.family)
	}
	if rec.quant != "F16" {
		t.Errorf("Quantization = %q, want F16 from file_type", rec.quant)
	}
	if rec.chat || !rec.embed {
		t.Errorf("encoder caps wrong: chat=%v embed=%v, want embed-only", rec.chat, rec.embed)
	}
}

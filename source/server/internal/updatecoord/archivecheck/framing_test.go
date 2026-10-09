package archivecheck

import (
	"archive/tar"
	"bytes"
	"context"
	"math"
	"testing"
)

func TestArchiveTarEndMarkerCannotBePayloadZeros(t *testing.T) {
	raw := buildTar(t, []tarEntry{{name: "release/agent", body: string(make([]byte, 1024)), typ: tar.TypeReg, mode: 0755}})
	opts := Options{Format: TarGz, Layout: Layout{Root: "release", Required: []string{"agent"}}, Bounds: testBounds()}
	if _, err := Check(context.Background(), Archive{Reader: bytes.NewReader(gzipBytes(t, raw))}, opts); err != nil {
		t.Fatal("valid fixture rejected", err)
	}
	for _, remove := range []int{512, 1024} {
		data := gzipBytes(t, raw[:len(raw)-remove])
		if _, err := Check(context.Background(), Archive{Reader: bytes.NewReader(data)}, opts); err == nil {
			t.Fatalf("accepted missing %d marker bytes after zero payload", remove)
		}
	}
}
func TestArchiveTarHiddenTailRejected(t *testing.T) {
	raw := buildTar(t, []tarEntry{{name: "release/agent", body: "x", typ: tar.TypeReg, mode: 0755}})
	hidden := append(append(append([]byte{}, raw...), bytes.Repeat([]byte{'x'}, 512)...), make([]byte, 1024)...)
	opts := Options{Format: TarGz, Layout: Layout{Root: "release", Required: []string{"agent"}}, Bounds: testBounds()}
	if _, err := Check(context.Background(), Archive{Reader: bytes.NewReader(gzipBytes(t, hidden))}, opts); err == nil {
		t.Fatal("accepted nonzero content past tar terminator")
	}
}
func TestArchiveBoundArithmeticRefusesOverflow(t *testing.T) {
	opts := Options{Format: TarGz, Layout: Layout{Root: "release", Required: []string{"agent"}}, Bounds: testBounds()}
	for _, which := range []string{"compressed", "total", "members"} {
		o := opts
		switch which {
		case "compressed":
			o.Bounds.MaxCompressedBytes = math.MaxInt64
		case "total":
			o.Bounds.MaxUncompressedBytes = math.MaxInt64
		case "members":
			o.Bounds.MaxMembers = math.MaxInt
		}
		if _, err := Check(context.Background(), Archive{Reader: bytes.NewReader(buildTarGz(t, []tarEntry{{name: "release/agent", body: "x", typ: tar.TypeReg, mode: 0755}}))}, o); err == nil {
			t.Fatalf("accepted overflowing %s budget", which)
		}
	}
}

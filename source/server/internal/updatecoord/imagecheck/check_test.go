package imagecheck

import (
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"os"
	"runtime"
	"testing"
)

func machoFixture() []byte {
	d := make([]byte, 512)
	le.PutUint32(d, 0xfeedfacf)
	le.PutUint32(d[4:], uint32(macho.CpuArm64))
	le.PutUint32(d[12:], uint32(macho.TypeExec))
	le.PutUint32(d[16:], 1)
	le.PutUint32(d[20:], 72)
	p := d[32:]
	le.PutUint32(p, 0x19)
	le.PutUint32(p[4:], 72)
	copy(p[8:], "__TEXT")
	le.PutUint64(p[24:], 0x100000000)
	le.PutUint64(p[32:], 512)
	le.PutUint64(p[48:], 512)
	le.PutUint32(p[56:], 5)
	le.PutUint32(p[60:], 5)
	return d
}
func elfFixture(arm bool) []byte {
	d := make([]byte, 256)
	copy(d, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	le.PutUint16(d[16:], uint16(elf.ET_EXEC))
	m := uint16(elf.EM_X86_64)
	if arm {
		m = uint16(elf.EM_AARCH64)
	}
	le.PutUint16(d[18:], m)
	le.PutUint32(d[20:], 1)
	le.PutUint64(d[24:], 0x400080)
	le.PutUint64(d[32:], 64)
	le.PutUint16(d[52:], 64)
	le.PutUint16(d[54:], 56)
	le.PutUint16(d[56:], 1)
	p := d[64:]
	le.PutUint32(p, uint32(elf.PT_LOAD))
	le.PutUint32(p[4:], 5)
	le.PutUint64(p[16:], 0x400000)
	le.PutUint64(p[32:], 256)
	le.PutUint64(p[40:], 256)
	le.PutUint64(p[48:], 4096)
	return d
}
func peFixture(arm bool) []byte {
	d := make([]byte, 1024)
	copy(d, "MZ")
	le.PutUint32(d[60:], 64)
	copy(d[64:], "PE\x00\x00")
	h := d[68:]
	m := uint16(pe.IMAGE_FILE_MACHINE_AMD64)
	if arm {
		m = pe.IMAGE_FILE_MACHINE_ARM64
	}
	le.PutUint16(h, m)
	le.PutUint16(h[2:], 1)
	le.PutUint16(h[16:], 240)
	le.PutUint16(h[18:], pe.IMAGE_FILE_EXECUTABLE_IMAGE)
	o := d[88:]
	le.PutUint16(o, 0x20b)
	le.PutUint32(o[16:], 0x1000)
	le.PutUint64(o[24:], 0x140000000)
	le.PutUint32(o[32:], 4096)
	le.PutUint32(o[36:], 512)
	le.PutUint32(o[56:], 8192)
	le.PutUint32(o[60:], 512)
	le.PutUint16(o[68:], 3)
	le.PutUint32(o[108:], 16)
	s := d[328:]
	copy(s, ".text")
	le.PutUint32(s[8:], 512)
	le.PutUint32(s[12:], 0x1000)
	le.PutUint32(s[16:], 512)
	le.PutUint32(s[20:], 512)
	le.PutUint32(s[36:], pe.IMAGE_SCN_MEM_EXECUTE|pe.IMAGE_SCN_MEM_READ|pe.IMAGE_SCN_CNT_CODE)
	return d
}
func TestImageFormats(t *testing.T) {
	cases := []struct {
		os, arch string
		data     []byte
	}{{"darwin", "arm64", machoFixture()}, {"linux", "amd64", elfFixture(false)}, {"linux", "arm64", elfFixture(true)}, {"windows", "amd64", peFixture(false)}, {"windows", "arm64", peFixture(true)}}
	for _, tc := range cases {
		t.Run(tc.os+tc.arch, func(t *testing.T) {
			if e := Check(tc.data, tc.os, tc.arch); e != nil {
				t.Fatal("valid fixture", e)
			}
			for _, other := range cases {
				if other.os != tc.os || other.arch != tc.arch {
					if Check(tc.data, other.os, other.arch) == nil {
						t.Fatal("wrong platform accepted", other.os, other.arch)
					}
				}
			}
			for n := 0; n < len(tc.data); n++ {
				if Check(tc.data[:n], tc.os, tc.arch) == nil {
					t.Fatalf("truncated at %d accepted", n)
				}
			}
		})
	}
}
func TestImageDangerousHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, os, arch string
		data           func() []byte
		mutate         func([]byte)
	}{
		{"machoObject", "darwin", "arm64", machoFixture, func(d []byte) { le.PutUint32(d[12:], 1) }},
		{"machoCount", "darwin", "arm64", machoFixture, func(d []byte) { le.PutUint32(d[16:], 0xffffffff) }},
		{"machoSections", "darwin", "arm64", machoFixture, func(d []byte) { le.PutUint32(d[96:], 0xffffffff) }},
		{"fat", "darwin", "arm64", machoFixture, func(d []byte) { le.PutUint32(d, 0xbebafeca) }},
		{"elfDSO", "linux", "amd64", func() []byte { return elfFixture(false) }, func(d []byte) { le.PutUint16(d[16:], uint16(elf.ET_DYN)) }},
		{"elfOffset", "linux", "amd64", func() []byte { return elfFixture(false) }, func(d []byte) { le.PutUint64(d[32:], ^uint64(0)) }},
		{"elfCount", "linux", "amd64", func() []byte { return elfFixture(false) }, func(d []byte) { le.PutUint16(d[56:], 0xffff) }},
		{"dll", "windows", "amd64", func() []byte { return peFixture(false) }, func(d []byte) { le.PutUint16(d[86:], pe.IMAGE_FILE_EXECUTABLE_IMAGE|pe.IMAGE_FILE_DLL) }},
		{"peSections", "windows", "amd64", func() []byte { return peFixture(false) }, func(d []byte) { le.PutUint16(d[70:], 0xffff) }},
		{"peSymbols", "windows", "amd64", func() []byte { return peFixture(false) }, func(d []byte) { le.PutUint32(d[80:], 0xffffffff) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.data()
			tc.mutate(d)
			if Check(d, tc.os, tc.arch) == nil {
				t.Fatal("malformed/unsupported image accepted")
			}
		})
	}
}
func TestHostTestBinary(t *testing.T) {
	if runtime.GOOS == "darwin" && runtime.GOARCH != "arm64" {
		t.Skip("Intel macOS is outside release matrix")
	}
	p, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	d, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = Check(d, runtime.GOOS, runtime.GOARCH); e != nil {
		t.Fatal(e)
	}
}
func FuzzImageHeaders(f *testing.F) {
	f.Add(machoFixture())
	f.Add(elfFixture(false))
	f.Add(peFixture(false))
	f.Fuzz(func(t *testing.T, d []byte) {
		if len(d) > 1<<20 {
			return
		}
		for _, o := range []string{"darwin", "linux", "windows"} {
			_ = Check(d, o, "arm64")
			_ = Check(d, o, "amd64")
		}
	})
}

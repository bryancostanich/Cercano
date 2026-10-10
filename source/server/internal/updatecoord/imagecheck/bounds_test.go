package imagecheck

import (
	"debug/elf"
	"testing"
)

func TestDynamicLinuxExecutableNeedsInterpreter(t *testing.T) {
	d := elfFixture(false)
	le.PutUint16(d[16:], uint16(elf.ET_DYN))
	le.PutUint16(d[56:], 2)
	p := d[120:]
	le.PutUint32(p, uint32(elf.PT_INTERP))
	le.PutUint64(p[8:], 192)
	name := []byte("/lib/ld.so\x00")
	copy(d[192:], name)
	le.PutUint64(p[32:], uint64(len(name)))
	if err := Check(d, "linux", "amd64"); err != nil {
		t.Fatal("PIE fixture refused", err)
	}
	d[192] = 'x'
	if Check(d, "linux", "amd64") == nil {
		t.Fatal("relative interpreter accepted")
	}
}
func TestAllocationHeaderBounds(t *testing.T) {
	d := machoFixture()
	le.PutUint32(d[16:], 2)
	le.PutUint32(d[20:], 152)
	p := d[104:]
	le.PutUint32(p, 0xb)
	le.PutUint32(p[4:], 80)
	le.PutUint32(p[60:], 0xffffffff)
	if Check(d, "darwin", "arm64") == nil {
		t.Fatal("unbounded indirect symbols accepted")
	}
	e := elfFixture(false)
	le.PutUint64(e[40:], 128)
	le.PutUint16(e[58:], 64)
	le.PutUint16(e[60:], 1)
	s := e[128:]
	le.PutUint32(s[4:], uint32(elf.SHT_STRTAB))
	le.PutUint64(s[8:], uint64(elf.SHF_COMPRESSED))
	le.PutUint64(s[24:], 224)
	le.PutUint64(s[32:], 32)
	if Check(e, "linux", "amd64") == nil {
		t.Fatal("compressed name table accepted")
	}
	if Check(nil, "linux", "amd64") == nil {
		t.Fatal("empty accepted")
	}
	if Check(machoFixture(), "darwin", "amd64") != ErrPlatform {
		t.Fatal("Intel macOS accepted")
	}
}

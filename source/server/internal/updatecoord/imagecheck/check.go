// Package imagecheck classifies an already authenticated, immutable image.
// It does not establish publisher trust, version identity, loader acceptance or
// runtime health. Callers must bind these bytes to their verified archive.
package imagecheck

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"errors"
)

const MaxImageBytes = 256 << 20

var ErrImage = errors.New("imagecheck: unsupported or malformed executable")
var ErrPlatform = errors.New("imagecheck: unsupported platform")

// Check reads data without modifying it. The caller must not mutate it during
// this call. Only the shipped platform matrix is accepted. Static Linux PIE
// without PT_INTERP is conservatively refused; ET_DYN alone also denotes a DSO.
func Check(data []byte, goos, goarch string) error {
	if goarch != "amd64" && goarch != "arm64" {
		return ErrPlatform
	}
	if goos != "darwin" && goos != "linux" && goos != "windows" {
		return ErrPlatform
	}
	if goos == "darwin" && goarch != "arm64" {
		return ErrPlatform
	}
	if len(data) == 0 || len(data) > MaxImageBytes {
		return ErrImage
	}
	var ok bool
	switch goos {
	case "darwin":
		ok = checkMachO(data)
	case "linux":
		ok = checkELF(data, goarch)
	case "windows":
		ok = checkPE(data, goarch)
	}
	if !ok {
		return ErrImage
	}
	return nil
}

var le = binary.LittleEndian

func span(data []byte, offset, size uint64) bool {
	return offset <= uint64(len(data)) && size <= uint64(len(data))-offset
}
func table(data []byte, offset, count, size uint64) bool {
	return size != 0 && count <= uint64(len(data))/size && span(data, offset, count*size)
}
func checkMachO(d []byte) bool {
	if len(d) < 32 || le.Uint32(d) != 0xfeedfacf || le.Uint32(d[4:]) != uint32(macho.CpuArm64) || le.Uint32(d[12:]) != uint32(macho.TypeExec) {
		return false
	}
	n, size := uint64(le.Uint32(d[16:])), uint64(le.Uint32(d[20:]))
	if n == 0 || n > 4096 || !span(d, 32, size) {
		return false
	}
	end, pos := 32+size, uint64(32)
	for i := uint64(0); i < n; i++ {
		if pos > end || end-pos < 8 {
			return false
		}
		cmd := le.Uint32(d[pos:])
		length := uint64(le.Uint32(d[pos+4:]))
		if length < 8 || length%8 != 0 || length > end-pos {
			return false
		}
		// Bound segment sections before debug/macho allocates them.
		if cmd == 0x19 {
			if length < 72 {
				return false
			}
			sections := uint64(le.Uint32(d[pos+64:]))
			if sections > 4096 || sections > (length-72)/80 {
				return false
			}
			off, sz := le.Uint64(d[pos+40:]), le.Uint64(d[pos+48:])
			if !span(d, off, sz) {
				return false
			}
			for j := uint64(0); j < sections; j++ {
				section := d[pos+72+j*80:]
				if !table(d, uint64(le.Uint32(section[56:])), uint64(le.Uint32(section[60:])), 8) {
					return false
				}
			}

		}
		if cmd == 0xb { // LC_DYSYMTAB: indirect symbols are allocated by macho.NewFile.
			if length < 80 {
				return false
			}
			count := uint64(le.Uint32(d[pos+60:]))
			if count > 1<<20 || !table(d, uint64(le.Uint32(d[pos+56:])), count, 4) {
				return false
			}
		}
		if cmd == 0x2 {
			if length < 24 {
				return false
			}
			symoff, count := uint64(le.Uint32(d[pos+8:])), uint64(le.Uint32(d[pos+12:]))
			if count > 1<<20 || !table(d, symoff, count, 16) || !span(d, uint64(le.Uint32(d[pos+16:])), uint64(le.Uint32(d[pos+20:]))) {
				return false
			}
		}
		pos += length
	}
	if pos != end {
		return false
	}
	f, e := macho.NewFile(bytes.NewReader(d))
	if e != nil {
		return false
	}
	defer f.Close()
	segment := f.Segment("__TEXT")
	return segment != nil && segment.Filesz > 0
}
func checkELF(d []byte, arch string) bool {
	if len(d) < 64 || !bytes.Equal(d[:4], []byte{0x7f, 'E', 'L', 'F'}) || d[4] != 2 || d[5] != 1 || d[6] != 1 || le.Uint32(d[20:]) != 1 || le.Uint16(d[52:]) != 64 {
		return false
	}
	machine := uint16(elf.EM_X86_64)
	if arch == "arm64" {
		machine = uint16(elf.EM_AARCH64)
	}
	typ := elf.Type(le.Uint16(d[16:]))
	if le.Uint16(d[18:]) != machine || (typ != elf.ET_EXEC && typ != elf.ET_DYN) {
		return false
	}
	phoff, shoff := le.Uint64(d[32:]), le.Uint64(d[40:])
	phsize, phnum := uint64(le.Uint16(d[54:])), uint64(le.Uint16(d[56:]))
	shsize, shnum := uint64(le.Uint16(d[58:])), uint64(le.Uint16(d[60:]))
	if phnum == 0 || phnum > 4096 || phsize != 56 || !table(d, phoff, phnum, phsize) {
		return false
	}
	// Extended section numbering needs extra decoding and is not a release format.
	if shoff != 0 && (shnum == 0 || shnum > 4096 || shsize != 64 || !table(d, shoff, shnum, shsize)) {
		return false
	}
	if shoff == 0 && shnum != 0 {
		return false
	}
	for i := uint64(0); i < phnum; i++ {
		p := d[phoff+i*phsize:]
		if !span(d, le.Uint64(p[8:]), le.Uint64(p[32:])) {
			return false
		}
	}
	for i := uint64(0); i < shnum; i++ {
		s := d[shoff+i*shsize:]
		// The section-name string table is loaded by elf.NewFile. Refuse
		// compressed tables rather than trusting a declared decompressed size.
		if elf.SectionType(le.Uint32(s[4:])) == elf.SHT_STRTAB && (le.Uint64(s[8:])&uint64(elf.SHF_COMPRESSED) != 0 || le.Uint64(s[32:]) > 16<<20) {
			return false
		}
		if elf.SectionType(le.Uint32(s[4:])) != elf.SHT_NOBITS && !span(d, le.Uint64(s[24:]), le.Uint64(s[32:])) {
			return false
		}
	}
	f, e := elf.NewFile(bytes.NewReader(d))
	if e != nil {
		return false
	}
	defer f.Close()
	interp, entry := false, false
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			if p.Filesz < 2 || p.Filesz > 4096 {
				return false
			}
			b := d[p.Off : p.Off+p.Filesz]
			if b[0] != '/' || b[len(b)-1] != 0 {
				return false
			}
			interp = true
		}
		if p.Type == elf.PT_LOAD && p.Flags&elf.PF_X != 0 && p.Filesz > 0 && f.Entry >= p.Vaddr && f.Entry-p.Vaddr < p.Filesz {
			entry = true
		}
	}
	return entry && (typ == elf.ET_EXEC || interp)
}
func checkPE(d []byte, arch string) bool {
	if len(d) < 64 || string(d[:2]) != "MZ" {
		return false
	}
	off := uint64(le.Uint32(d[60:]))
	if !span(d, off, 24) || string(d[off:off+4]) != "PE\x00\x00" {
		return false
	}
	h := d[off+4:]
	machine := uint16(pe.IMAGE_FILE_MACHINE_AMD64)
	if arch == "arm64" {
		machine = pe.IMAGE_FILE_MACHINE_ARM64
	}
	count, opt := uint64(le.Uint16(h[2:])), uint64(le.Uint16(h[16:]))
	flags := le.Uint16(h[18:])
	if le.Uint16(h) != machine || flags&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || flags&pe.IMAGE_FILE_DLL != 0 || count == 0 || count > 96 || opt < 112 {
		return false
	}
	start := off + 24
	if !span(d, start, opt) || le.Uint16(d[start:]) != 0x20b || !table(d, start+opt, count, 40) {
		return false
	}
	dirs := uint64(le.Uint32(d[start+108:]))
	if dirs > 16 || opt != 112+dirs*8 {
		return false
	}
	symoff, syms := uint64(le.Uint32(h[8:])), uint64(le.Uint32(h[12:]))
	if syms > 1<<20 {
		return false
	}
	if syms != 0 {
		if !table(d, symoff, syms, 18) || !span(d, symoff+syms*18, 4) {
			return false
		}
		str := symoff + syms*18
		n := uint64(le.Uint32(d[str:]))
		if n < 4 || !span(d, str, n) {
			return false
		}
	}
	for i := uint64(0); i < count; i++ {
		s := d[start+opt+i*40:]
		if !span(d, uint64(le.Uint32(s[20:])), uint64(le.Uint32(s[16:]))) {
			return false
		}
	}
	f, e := pe.NewFile(bytes.NewReader(d))
	if e != nil {
		return false
	}
	defer f.Close()
	o, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		return false
	}
	for _, s := range f.Sections {
		if s.Characteristics&pe.IMAGE_SCN_MEM_EXECUTE != 0 && s.Size > 0 && o.AddressOfEntryPoint >= s.VirtualAddress && uint64(o.AddressOfEntryPoint-s.VirtualAddress) < uint64(s.Size) {
			return true
		}
	}
	return false
}

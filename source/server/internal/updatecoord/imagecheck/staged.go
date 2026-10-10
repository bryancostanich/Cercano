package imagecheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"cercano/source/server/internal/updatecoord/archivecheck"
)

var ErrStage = errors.New("imagecheck: invalid or changed staged image")

// CheckStaged binds classification to the manifest hashes and lengths produced
// by verified archive preflight. Required contains full manifest-relative names
// selected by trusted packaging policy (not an archive-supplied command).
//
// The caller supplies an authenticated manifest and a protected staging parent;
// an arbitrary caller-built manifest is not a trust root. No files are written,
// chmodded or executed. The result describes this observation only: later launch
// and activation must revalidate, not treat a successful check as a permanent
// guarantee against a same-user writer. Callers must not mutate arguments during
// this call. Directory confinement is handle-based; symbolic links are refused.
func CheckStaged(ctx context.Context, stage archivecheck.Staged, required []string, goos, goarch string) error {
	if ctx == nil || stage.Manifest == nil || !filepath.IsAbs(stage.Dir) || len(required) == 0 || len(required) > 16 || len(stage.Manifest.Members) > 10000 {
		return ErrStage
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	entries := make(map[string]archivecheck.Member, len(stage.Manifest.Members))
	for _, m := range stage.Manifest.Members {
		if !safeName(m.Name) || m.Size < 0 || !digestValid(m.SHA256) {
			return ErrStage
		}
		if _, exists := entries[m.Name]; exists {
			return ErrStage
		}
		entries[m.Name] = m
	}
	selected := make([]archivecheck.Member, 0, len(required))
	seen := map[string]bool{}
	for _, name := range required {
		m, ok := entries[name]
		if !safeName(name) || !ok || seen[name] || m.Size <= 0 || m.Size > MaxImageBytes {
			return ErrStage
		}
		seen[name] = true
		selected = append(selected, m)
	}
	before, err := os.Lstat(stage.Dir)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return ErrStage
	}
	root, err := os.OpenRoot(stage.Dir)
	if err != nil {
		return ErrStage
	}
	defer root.Close()
	bound, err := root.Stat(".")
	if err != nil || !os.SameFile(before, bound) {
		return ErrStage
	}
	for _, m := range selected {
		if err = checkMember(ctx, root, m, goos, goarch); err != nil {
			return err
		}
	}
	after, err := os.Lstat(stage.Dir)
	if err != nil || !after.IsDir() || !os.SameFile(bound, after) {
		return ErrStage
	}
	return nil
}
func checkMember(ctx context.Context, root *os.Root, m archivecheck.Member, goos, goarch string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := regularMember(root, m.Name)
	if err != nil || before.Size() != m.Size {
		return ErrStage
	}
	f, err := openMember(root, m.Name)
	if err != nil {
		return ErrStage
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !sameFileState(before, opened) {
		return ErrStage
	}
	// Allocate only after the manifest, file size and hard byte limit agree.
	data := make([]byte, int(m.Size))
	offset := 0
	for offset < len(data) {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := len(data) - offset
		if n > 256<<10 {
			n = 256 << 10
		}
		if _, err = io.ReadFull(f, data[offset:offset+n]); err != nil {
			return ErrStage
		}
		offset += n
	}
	var extra [1]byte
	n, readErr := f.Read(extra[:])
	if n != 0 || !errors.Is(readErr, io.EOF) {
		return ErrStage
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != m.SHA256 {
		return ErrStage
	}
	after, err := f.Stat()
	if err != nil || !sameFileState(opened, after) {
		return ErrStage
	}
	named, err := regularMember(root, m.Name)
	if err != nil || !sameFileState(opened, named) {
		return ErrStage
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return Check(data, goos, goarch)
}
func regularMember(root *os.Root, name string) (os.FileInfo, error) {
	parts := strings.Split(name, "/")
	path := ""
	for i, part := range parts {
		if i > 0 {
			path += "/"
		}
		path += part
		info, err := root.Lstat(path)
		if err != nil {
			return nil, ErrStage
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrStage
		}
		if i == len(parts)-1 {
			if !info.Mode().IsRegular() {
				return nil, ErrStage
			}
			return info, nil
		}
		if !info.IsDir() {
			return nil, ErrStage
		}
	}
	return nil, ErrStage
}
func sameFileState(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}
func safeName(name string) bool {
	if name == "." || len(name) > 4096 || !utf8.ValidString(name) || !fs.ValidPath(name) || strings.ContainsAny(name, "\\:") {
		return false
	}
	for _, c := range name {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}
func digestValid(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

package updater

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"

	selfupdate "github.com/creativeprojects/go-selfupdate"
)

const maxArchiveBytes int64 = 256 << 20
const maxExpandedBytes uint64 = 512 << 20

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("download exceeds size limit")
	}
	return b, nil
}

// The library extractor matches basenames. Reject ambiguous/hostile members
// before invoking it; authenticated bytes are not necessarily a safe archive.
func validateArchiveStructure(data []byte, asset string, binaries []string) error {
	if !strings.HasSuffix(asset, ".zip") || path.Base(asset) != asset {
		return fmt.Errorf("invalid archive name")
	}
	root := strings.TrimSuffix(asset, ".zip")
	allowed := map[string]bool{root + "/LICENSE": true, root + "/README.txt": true}
	for _, b := range binaries {
		if b == "" || path.Base(b) != b || strings.ContainsAny(b, "\\:") {
			return fmt.Errorf("invalid binary name")
		}
		allowed[root+"/bin/"+b] = true
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if len(zr.File) > 32 {
		return fmt.Errorf("too many archive members")
	}
	seen := map[string]bool{}
	var total uint64
	for _, f := range zr.File {
		n := f.Name
		if seen[n] {
			return fmt.Errorf("duplicate archive member: %s", n)
		}
		seen[n] = true
		if strings.ContainsAny(n, "\\:") || strings.HasPrefix(n, "/") || strings.Contains(n, "../") {
			return fmt.Errorf("unsafe archive path")
		}
		if f.Mode().IsDir() {
			if n != root+"/" && n != root+"/bin/" {
				return fmt.Errorf("unexpected directory")
			}
			continue
		}
		if !f.Mode().IsRegular() || !allowed[n] {
			return fmt.Errorf("unexpected or nonregular archive member: %s", n)
		}
		if f.UncompressedSize64 > maxExpandedBytes-total {
			return fmt.Errorf("expanded archive exceeds size limit")
		}
		total += f.UncompressedSize64
	}
	for _, b := range binaries {
		if !seen[root+"/bin/"+b] {
			return fmt.Errorf("%w: missing binary: %s", selfupdate.ErrExecutableNotFoundInArchive, b)
		}
	}
	return nil
}

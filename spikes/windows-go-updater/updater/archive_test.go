package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestStrictArchiveGate(t *testing.T) {
	const root = "cercano-1.0.0-windows-x64"
	binaries := []string{"cercano.exe", "cercano-cli.exe"}
	base := []string{root + "/bin/" + binaries[0], root + "/bin/" + binaries[1]}
	cases := []struct {
		name    string
		names   []string
		symlink bool
		valid   bool
	}{
		{"valid", base, false, true},
		{"duplicate", append(append([]string{}, base...), base[0]), false, false},
		{"missing", base[:1], false, false},
		{"traversal", []string{"../bin/cercano.exe", base[1]}, false, false},
		{"absolute", []string{"/bin/cercano.exe", base[1]}, false, false},
		{"symlink", base, true, false},
		{"wrong-root", []string{"wrong/bin/cercano.exe", base[1]}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			for i, n := range c.names {
				h := &zip.FileHeader{Name: n, Method: zip.Store}
				h.SetMode(0755)
				if c.symlink && i == 0 {
					h.SetMode(os.ModeSymlink | 0755)
				}
				w, err := zw.CreateHeader(h)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = w.Write([]byte("fixture"))
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			err := validateArchiveStructure(buf.Bytes(), root+".zip", binaries)
			if (err == nil) != c.valid {
				t.Fatalf("err=%v valid=%v", err, c.valid)
			}
		})
	}
}

func TestBoundedDownload(t *testing.T) {
	if _, err := readBounded(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("oversized download accepted")
	}
	if b, err := readBounded(strings.NewReader("1234"), 4); err != nil || string(b) != "1234" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestCoordinatorRequiresValidator(t *testing.T) {
	_, _, err := Stage(context.Background(), Options{})
	if err == nil || !strings.Contains(err.Error(), "validator") {
		t.Fatalf("missing validator accepted: %v", err)
	}
}

package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// 1x1 transparent PNG.
var onePxPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}

func TestParseImagePathsQuotingAndEscapes(t *testing.T) {
	cases := map[string][]string{
		`/a/b.png`:           {`/a/b.png`},
		`'/a/b c.png'`:       {`/a/b c.png`},
		`"/a/b c.png"`:       {`/a/b c.png`},
		`/a/b\ c.png`:        {`/a/b c.png`},
		"/a/x.png /a/y.png":  {`/a/x.png`, `/a/y.png`},
		"/a/x.png\n/a/y.png": {`/a/x.png`, `/a/y.png`},
	}
	for in, want := range cases {
		got := parseImagePaths(in)
		if len(got) != len(want) {
			t.Errorf("parseImagePaths(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("parseImagePaths(%q)[%d] = %q, want %q", in, i, got[i], want[i])
			}
		}
	}
}

func TestClassifyImagePasteRealFile(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(img, onePxPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	imgs, ok := classifyImagePaste(img)
	if !ok || len(imgs) != 1 || imgs[0].mediaType != "image/png" || imgs[0].source != img {
		t.Fatalf("expected one png image, got ok=%v imgs=%+v", ok, imgs)
	}
}

func TestClassifyImagePasteNonImageIsLiteral(t *testing.T) {
	dir := t.TempDir()
	txt := filepath.Join(dir, "notes.txt")
	os.WriteFile(txt, []byte("hello"), 0o644)
	if _, ok := classifyImagePaste(txt); ok {
		t.Fatal("a text file path must not classify as an image drop")
	}
	if _, ok := classifyImagePaste("just some pasted prose"); ok {
		t.Fatal("prose must not classify as an image drop")
	}
}

func TestLoadDroppedImageRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.png")
	// header makes it sniff as png; size over the cap.
	buf := append(append([]byte{}, onePxPNG...), make([]byte, (20<<20)+1)...)
	os.WriteFile(big, buf, 0o644)
	if _, _, err := loadDroppedImage(big); err == nil {
		t.Fatal("oversize image must be rejected")
	}
}

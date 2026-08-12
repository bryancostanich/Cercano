package builtins

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/llm"
)

// writePNG writes a tiny valid PNG to path and returns its bytes.
func writePNG(t *testing.T, path string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestViewImage_ReturnsImageBlock(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pic.png")
	raw := writePNG(t, p)

	cap := ViewImage()
	if cap.Name() != "view_image" || cap.Tier() != capabilities.TierR {
		t.Fatalf("name/tier wrong: %q %q", cap.Name(), cap.Tier())
	}

	args, _ := json.Marshal(map[string]any{"path": p})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 {
		t.Fatalf("expected 1 image block, got %d", len(res.Images))
	}
	img := res.Images[0]
	if img.Type != llm.BlockImage {
		t.Fatalf("block type = %q, want %q", img.Type, llm.BlockImage)
	}
	if img.MediaType != "image/png" {
		t.Fatalf("media type = %q, want image/png", img.MediaType)
	}
	if want := base64.StdEncoding.EncodeToString(raw); img.ImageData != want {
		t.Fatalf("image data mismatch: got %d bytes, want %d", len(img.ImageData), len(want))
	}
	if res.Text == "" {
		t.Fatalf("expected a text sidecar describing the load, got empty")
	}
}

func TestViewImage_DetectsTypeFromBytesNotExtension(t *testing.T) {
	dir := t.TempDir()
	// A PNG on disk with a misleading .jpg extension must be reported as PNG.
	p := filepath.Join(dir, "actually_png.jpg")
	writePNG(t, p)

	args, _ := json.Marshal(map[string]any{"path": p})
	res, err := ViewImage().Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.Images[0].MediaType != "image/png" {
		t.Fatalf("media type = %q, want image/png (from magic bytes)", res.Images[0].MediaType)
	}
}

func TestViewImage_RefusesNonImage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(p, []byte("just some plain text, not an image at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": p})
	_, err := ViewImage().Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected refusal for a non-image file, got nil error")
	}
	if !strings.Contains(err.Error(), "not a supported image type") {
		t.Fatalf("error = %v, want mention of unsupported type", err)
	}
}

func TestViewImage_MissingPath(t *testing.T) {
	args, _ := json.Marshal(map[string]any{})
	_, err := ViewImage().Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected 'path is required', got %v", err)
	}
}

func TestViewImage_RefusesDirectory(t *testing.T) {
	dir := t.TempDir()
	args, _ := json.Marshal(map[string]any{"path": dir})
	_, err := ViewImage().Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("expected directory refusal, got %v", err)
	}
}

func TestViewImage_Surfaces(t *testing.T) {
	s := ViewImage().Surfaces()
	if !s.Has(capabilities.SurfaceAgent) || !s.Has(capabilities.SurfaceMCP) {
		t.Fatalf("view_image should be on agent+MCP surfaces, got %v", s)
	}
}

package builtins

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/llm"
)

// maxImageBytes caps the on-disk size view_image will load. Vision providers
// reject very large images, and base64 inflates payloads ~4/3, so a hard cap
// keeps a stray multi-hundred-MB file from blowing up the request. 10 MiB
// comfortably covers screenshots and diagrams while staying under provider
// limits (Anthropic's per-image cap is ~5 MB of base64 ≈ 3.75 MB raw, so most
// providers accept well under this; oversize images should be downscaled by
// the caller, which is out of scope for a file-to-pixels on-ramp).
const maxImageBytes = 10 << 20 // 10 MiB

// supportedImageTypes is the set of media types the vision-capable providers
// accept. Detected via magic bytes, not the file extension, so a mislabeled
// file is caught. Anything outside this set is refused with a clear message.
var supportedImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// viewImageCap loads an image file from disk and returns it as a model-visible
// image block. It is the built-in on-ramp that the plain read_file tool cannot
// provide: read_file refuses binaries and only ever returns text, so a PNG on
// disk had no path into the model's context. view_image base64-encodes the
// bytes into an llm.BlockImage; the tool loop forwards it as a sibling block
// after the tool_result when the active model reports vision support (the exact
// path an MCP image tool-result already travels).
type viewImageCap struct{}

// ViewImage constructs the view_image capability (display name "ViewImage").
func ViewImage() capabilities.Capability { return viewImageCap{} }

func (viewImageCap) Name() string                  { return "view_image" }
func (viewImageCap) Tier() capabilities.Tier        { return capabilities.TierR }
func (viewImageCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (viewImageCap) Description() string {
	return "Load an image file (PNG, JPEG, GIF, or WebP) from disk and place its pixels in front of the model. Use this to actually see an image — unlike Read, which only handles text and refuses binaries. Args: {path: string}."
}
func (viewImageCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {"type": "string", "description": "Absolute or relative path to a PNG, JPEG, GIF, or WebP image."}
		}
	}`)
}

type viewImageArgs struct {
	Path string `json:"path"`
}

func (viewImageCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a viewImageArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("view_image: parse args: %w", err)
	}
	if a.Path == "" {
		return nil, errors.New("view_image: path is required")
	}
	a.Path = resolvePath(call.WorkDir, a.Path)

	info, err := os.Stat(a.Path)
	if err != nil {
		return nil, fmt.Errorf("view_image: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("view_image: %s is a directory, not an image", a.Path)
	}
	if info.Size() > maxImageBytes {
		return nil, fmt.Errorf("view_image: %s is %d bytes, exceeds the %d-byte limit; downscale it first", a.Path, info.Size(), maxImageBytes)
	}

	data, err := os.ReadFile(a.Path)
	if err != nil {
		return nil, fmt.Errorf("view_image: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("view_image: %s is empty", a.Path)
	}

	// Detect the media type from magic bytes, not the extension, so a
	// mislabeled or extensionless file is classified correctly (and a text
	// file masquerading as .png is refused rather than shipped as broken image
	// bytes). http.DetectContentType inspects the first 512 bytes.
	mediaType := detectImageType(data)
	if !supportedImageTypes[mediaType] {
		return nil, fmt.Errorf("view_image: %s is %s, not a supported image type (png, jpeg, gif, webp)", a.Path, mediaType)
	}

	res := &capabilities.Result{
		Type: capabilities.ResultText,
		Text: fmt.Sprintf("[loaded %s image: %s]", mediaType, a.Path),
		Images: []llm.Block{{
			Type:      llm.BlockImage,
			MediaType: mediaType,
			ImageData: base64.StdEncoding.EncodeToString(data),
		}},
	}
	res.Detail = mediaType
	return res, nil
}

// detectImageType returns the sniffed media type. http.DetectContentType never
// fails (it falls back to "application/octet-stream"), and it reports "image/webp"
// for RIFF/WEBP containers, so it covers the full supported set.
func detectImageType(data []byte) string {
	return http.DetectContentType(data)
}

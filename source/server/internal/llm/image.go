package llm

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
)

// maxImageBytes caps a fetched image to guard memory. Generous for screenshots.
const maxImageBytes = 20 << 20 // 20 MiB

// ResolveImageBytes returns the raw bytes of an image block: decoding ImageData
// (base64) or fetching ImageURL (http GET, bounded). Used by adapters whose
// provider can't take a URL (Ollama). NOTE: the URL fetch is an SSRF surface
// once an inbound path supplies untrusted URLs — hardening lands with that path.
func ResolveImageBytes(ctx context.Context, b Block) ([]byte, error) {
	switch {
	case b.ImageData != "":
		data, err := base64.StdEncoding.DecodeString(b.ImageData)
		if err != nil {
			return nil, fmt.Errorf("decode image base64: %w", err)
		}
		return data, nil
	case b.ImageURL != "":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.ImageURL, nil)
		if err != nil {
			return nil, fmt.Errorf("image url request: %w", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch image url: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch image url: status %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read image url: %w", err)
		}
		if len(data) > maxImageBytes {
			return nil, fmt.Errorf("image exceeds %d bytes", maxImageBytes)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("image block has neither ImageData nor ImageURL")
	}
}

package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	goopenai "github.com/sashabaranov/go-openai"
)

// normalizingDoer wraps an HTTPDoer to rewrite array-shaped error bodies into the
// object shape go-openai's ErrorResponse expects, before go-openai parses them.
// Retry is handled by the httpx.RetryTransport this wraps (see client.go). 2xx
// (streaming) responses pass through untouched — their bodies are never buffered.
type normalizingDoer struct {
	next   goopenai.HTTPDoer
	quirks Quirks
}

func (d *normalizingDoer) Do(req *http.Request) (*http.Response, error) {
	resp, err := d.next.Do(req)
	if err != nil {
		return nil, err
	}
	return d.normalize(resp), nil
}

// normalize rewrites an array-shaped error body to the object shape when the
// backend needs it. Only non-2xx bodies are read; 2xx responses pass through so
// streaming bodies are never consumed.
func (d *normalizingDoer) normalize(resp *http.Response) *http.Response {
	if !d.quirks.NormalizeErrors || resp.StatusCode < 400 {
		return resp
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		resp.ContentLength = 0
		return resp
	}
	if fixed, ok := arrayErrorToObject(body); ok {
		body = fixed
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp
}

// arrayErrorToObject unwraps a `[{...}]` error body to its first object element.
// Returns (newBody, true) only when the body is a non-empty JSON array.
func arrayErrorToObject(body []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(trimmed, &arr); err != nil || len(arr) == 0 {
		return nil, false
	}
	return arr[0], true
}

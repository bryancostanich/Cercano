package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
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
	patched, err := patchExplicitZeroTemperature(req)
	if err != nil {
		return nil, err
	}
	resp, err := d.next.Do(patched)
	if err != nil {
		return nil, err
	}
	return d.normalize(resp), nil
}

func patchExplicitZeroTemperature(req *http.Request) (*http.Request, error) {
	if req.Body == nil || req.Method == http.MethodGet {
		return req, nil
	}
	body, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		return req, nil
	}
	if raw, ok := obj["temperature"]; ok {
		var temp float64
		if json.Unmarshal(raw, &temp) == nil && temp == explicitZeroTemperatureSentinel {
			obj["temperature"] = json.RawMessage(`0`)
			body, err = json.Marshal(obj)
			if err != nil {
				return nil, err
			}
			log.Printf("[openai] patched explicit zero temperature: method=%s path=%s", req.Method, req.URL.Path)
		}
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	return req, nil
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
	if fixed, ok := stringErrorToObject(body); ok {
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

// stringErrorToObject rewrites a `{"error":"<string>"}` body — error as a bare
// string rather than the `{"error":{"message":...}}` object OpenAI uses — into
// the object shape go-openai's ErrorResponse expects. llama-server and some
// other OpenAI-compatible servers emit the string form (e.g. a 5xx carrying
// `{"error":"Compute error"}`); without this, go-openai fails to type it into
// an *APIError, so the adapter loses the HTTP status and misclassifies the
// failure as ErrUnknown instead of the transient ErrBusy a 5xx deserves.
// Returns (newBody, true) only when the body is an object whose "error" field
// is a JSON string. The message is preserved verbatim.
func stringErrorToObject(body []byte) ([]byte, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, false
	}
	raw, ok := obj["error"]
	if !ok {
		return nil, false
	}
	var msg string
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, false // already an object (or non-string) — leave it alone
	}
	obj["error"] = json.RawMessage(mustMarshal(map[string]string{"message": msg}))
	fixed, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}
	return fixed, true
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}

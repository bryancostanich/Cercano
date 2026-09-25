package builtins

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/pkg/config"
)

// deepinfra_infer runs a single inference request against DeepInfra's NATIVE
// inference endpoint — https://api.deepinfra.com/v1/inference/{model} — NOT the
// OpenAI-compatible chat-completions surface. The model id is an arbitrary
// hosted DeepInfra model path and the request body is a raw JSON object as the
// model page documents it; Cercano adds no chat wrapping.
//
// Constraints implemented here:
//   - Credentials: one request per call, sent with the API key of a configured
//     DeepInfra cloud profile (OS keychain). Credential eligibility requires an
//     EXACT api.deepinfra.com base-URL host; the provider label alone is not
//     authority. No anonymous fallback, no paid retries: exactly one attempt.
//   - Profile selector: optional `profile` picks among multiple configured
//     DeepInfra profiles; omitting it when several are configured is an
//     ambiguity error listing the candidates.
//   - Transport safety: redirects are refused, the response body is read with
//     an explicit Max+1 bound and OVERFLOW IS AN ERROR (never a silent
//     truncation), and text/event-stream responses are rejected as
//     unsupported rather than half-parsed.
//   - Bounded output: the full raw response (JSON or binary) is written to a
//     local artifact file via exclusive create; the context copy is bounded to
//     32 KiB, sanitized (base64-like or oversized values redacted), and NEVER
//     contains raw non-JSON/binary bytes — those are artifact-only metadata.
//   - Attachments give the model large or binary inputs without them passing
//     through context: local files (workdir-relative) or previously-registered
//     image attachments (attachment_id, resolved through the same narrow
//     visionattach seam inspect_image uses), bound into top-level input fields.

// deepinfraEndpoint is the native inference URL builder. A package var so
// tests can point it at an httptest server.
var deepinfraEndpoint = func(model string) string {
	return config.DeepinfraNativeBaseURL + "/v1/inference/" + model
}

const (
	// deepinfraNativeHost is the ONLY host DeepInfra credentials may be
	// presented to (checked at credential eligibility, config-side).
	deepinfraNativeHost = "api.deepinfra.com"

	deepinfraDefaultTimeoutSeconds = 120
	deepinfraMinTimeoutSeconds     = 1
	deepinfraMaxTimeoutSeconds     = 600

	// deepinfraMaxResponseBytes caps the HTTP response read at 64 MiB — a
	// sensible bound for audio/image artifacts. The read uses Max+1 so an
	// over-cap response is REJECTED explicitly, never silently truncated.
	deepinfraMaxResponseBytes = 64 << 20
	// deepinfraMaxAttachmentBytes caps one attachment's DECODED bytes.
	deepinfraMaxAttachmentBytes = 8 << 20
	// deepinfraMaxInputBytes caps the aggregate ENCODED request body
	// (input JSON + every encoded attachment) at 32 MiB.
	deepinfraMaxInputBytes = 32 << 20
	// deepinfraContextCap bounds the sanitized context copy handed to the
	// model. NewTextResult applies the same 32 KiB policy as a final guard.
	deepinfraContextCap = 32 << 10

	// redactStringLen is the length above which a JSON string value in the
	// response is redacted from the context copy (base64 blobs, long text
	// fields, embeddings). Values at or below it pass through.
	redactStringLen = 512
	// redactMinB64Len is the shortest run a base64-like pattern needs before
	// redaction, so short ids like "abc123XYZ=" never trigger it.
	redactMinB64Len = 64
)

var deepinfraB64Like = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

type deepinfraInferCap struct{}

// DeepInfraInfer constructs the deepinfra_infer capability.
func DeepInfraInfer() capabilities.Capability { return deepinfraInferCap{} }

func (deepinfraInferCap) Name() string            { return "deepinfra_infer" }
func (deepinfraInferCap) Tier() capabilities.Tier { return capabilities.TierW }

// Exposed on both surfaces: the standalone agent loop and the MCP plugin each
// get their own registration from this single capability implementation.
func (deepinfraInferCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}

func (deepinfraInferCap) Description() string {
	return ("Run one inference request against DeepInfra's NATIVE endpoint " +
		"(https://api.deepinfra.com/v1/inference/{model}) with an arbitrary model " +
		"id and a raw JSON body (not chat completions). W-tier: it spends paid " +
		"DeepInfra quota. The full response is saved to a local artifact file; a " +
		"bounded sanitized summary (base64 redacted) is returned to context for " +
		"JSON responses, and metadata only for non-JSON/binary responses. " +
		"Args: {model: string, input?: object (model-documented JSON body; " +
		"arbitrarily nested JSON allowed — only attachment bindings must be " +
		"top-level fields), profile?: string (configured DeepInfra profile; " +
		"required to disambiguate when several are configured), attachments?: " +
		"[{path XOR attachment_id, field, encoding: utf8|base64|data_uri}] bound " +
		"into top-level input fields, settings?: {timeout_seconds: 1-600 " +
		"(default 120 when omitted), output_dir?: string (workdir-relative " +
		"artifact dir; default <workdir>/.cercano/deepinfra)}}.")
}

func (deepinfraInferCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"model": {"type": "string", "description": "DeepInfra model id, e.g. 'vendor/model'. Plain slash-separated path segments only."},
			"input": {"type": "object", "description": "Raw JSON request body exactly as the model's API page documents. Numbers are preserved exactly (no float coercion)."},
			"profile": {"type": "string", "description": "Name of a configured DeepInfra cloud profile to use. Required to disambiguate when multiple DeepInfra profiles are configured."},
			"attachments": {
				"type": "array",
				"description": "Files or conversation image attachments bound into the request body as top-level input fields (e.g. field 'image'). Exactly one of path / attachment_id per entry; binding a field already present in input (or used twice) is rejected.",
				"items": {
					"type": "object",
					"properties": {
						"path": {"type": "string", "description": "Path of the local file to attach (resolved relative to the work directory when not absolute)."},
						"attachment_id": {"type": "string", "description": "ID of an image attachment previously registered in this conversation (the same seam inspect_image reads)."},
						"field": {"type": "string", "description": "Top-level JSON field of input to bind the content to."},
						"encoding": {"type": "string", "enum": ["utf8", "base64", "data_uri"], "description": "Explicit encoding: utf8 binds the file text as-is; base64 binds standard base64 of the raw bytes; data_uri binds a data:<mediatype>;base64,<…> URI."}
					},
					"required": ["field", "encoding"]
				}
			},
			"settings": {
				"type": "object",
				"properties": {
					"timeout_seconds": {"type": "integer", "minimum": 1, "maximum": 600, "description": "Single-attempt HTTP timeout. Default 120 when omitted/zero; out-of-range values are rejected, not clamped."},
					"output_dir": {"type": "string", "description": "Directory for the response artifact, resolved relative to the work directory when not absolute. Default <workdir>/.cercano/deepinfra."}
				},
				"description": "Execution settings distinct from the model input body."
			}
		},
		"required": ["model"]
	}`)
}

type deepinfraAttachment struct {
	Path         string `json:"path"`
	AttachmentID string `json:"attachment_id"`
	Field        string `json:"field"`
	Encoding     string `json:"encoding"`
}

type deepinfraSettings struct {
	TimeoutSeconds int    `json:"timeout_seconds"`
	OutputDir      string `json:"output_dir"`
}

type deepinfraArgs struct {
	Model       string                `json:"model"`
	Input       map[string]any        `json:"input"`
	Profile     string                `json:"profile"`
	Attachments []deepinfraAttachment `json:"attachments"`
	Settings    deepinfraSettings     `json:"settings"`
}

// validateDeepinfraModel accepts only a plain slash-separated model path:
// non-empty, non-dot segments; no percent-encoding, backslash, scheme/host,
// query/fragment, or control characters.
func validateDeepinfraModel(model string) error {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return fmt.Errorf("'model' is required")
	}
	if strings.ContainsFunc(model, unicode.IsSpace) {
		return fmt.Errorf("'model' must not contain whitespace (a model id is a plain path like 'vendor/model'), got %q", model)
	}
	if strings.ContainsAny(model, "%\\") || strings.ContainsAny(model, "?#@") {
		return fmt.Errorf("'model' must be a plain model path like 'vendor/model' (no percent-encoding, backslash, or URL syntax), got %q", model)
	}
	if u, err := url.Parse(model); err != nil || u.Scheme != "" || u.Host != "" || strings.Contains(model, "..") {
		return fmt.Errorf("'model' must be a plain model path like 'vendor/model', got %q", model)
	}
	for _, seg := range strings.Split(model, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("'model' must be a plain model path with non-empty, non-dot segments, got %q", model)
		}
		for _, r := range seg {
			if unicode.IsControl(r) {
				return fmt.Errorf("'model' must not contain control characters, got %q", model)
			}
		}
	}
	return nil
}

// deepinfraResolvePath resolves an agent-supplied path against the call's work
// directory when it is not absolute (paths are workdir-relative).
func deepinfraResolvePath(workDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workDir, path)
}

func (deepinfraInferCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	// UseNumber: arbitrary numbers (big ids, precise decimals) survive the
	// round trip into the request body exactly as written.
	// Pre-decode bound: oversized args are rejected before JSON decoding can
	// allocate at all.
	if len(call.Args) > deepinfraMaxInputBytes {
		return nil, fmt.Errorf("deepinfra_infer: args are %d bytes, over the %d-byte cap; refusing to decode", len(call.Args), deepinfraMaxInputBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(call.Args))
	dec.UseNumber()
	var a deepinfraArgs
	if err := dec.Decode(&a); err != nil {
		return nil, fmt.Errorf("deepinfra_infer: parse args: %w", err)
	}
	if err := validateDeepinfraModel(a.Model); err != nil {
		return nil, fmt.Errorf("deepinfra_infer: %w", err)
	}

	input := a.Input
	if input == nil {
		input = map[string]any{}
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("deepinfra_infer: encode input: %w", err)
	}
	// used tracks the running encoded body budget: the base input JSON plus
	// every encoded attachment, checked WHILE binding so peak allocation stays
	// bounded and the aggregate cap cannot be silently exceeded by encoding
	// everything first.
	used := len(body)
	if err := bindDeepinfraAttachments(input, a.Attachments, call, &used); err != nil {
		return nil, fmt.Errorf("deepinfra_infer: %w", err)
	}

	body, err = json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("deepinfra_infer: encode input: %w", err)
	}
	if len(body) > deepinfraMaxInputBytes {
		return nil, fmt.Errorf("deepinfra_infer: encoded request body is %d bytes, over the %d-byte aggregate cap (input + encoded attachments)", len(body), deepinfraMaxInputBytes)
	}

	key, profile, err := deepinfraCredential(a.Profile, call.Svc)
	if err != nil {
		return nil, err
	}

	// Out-of-range timeouts are REJECTED, not clamped; zero means "omitted →
	// documented default" and nothing else.
	timeoutSeconds := a.Settings.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = deepinfraDefaultTimeoutSeconds
	}
	if timeoutSeconds < deepinfraMinTimeoutSeconds || timeoutSeconds > deepinfraMaxTimeoutSeconds {
		return nil, fmt.Errorf("deepinfra_infer: invalid timeout_seconds %d (must be %d-%d; out-of-range values are rejected, not clamped)", timeoutSeconds, deepinfraMinTimeoutSeconds, deepinfraMaxTimeoutSeconds)
	}

	raw, contentType, status, err := deepinfraRequest(ctx, deepinfraEndpoint(a.Model), key, body, time.Duration(timeoutSeconds)*time.Second)
	if err != nil {
		return nil, fmt.Errorf("deepinfra_infer: %s: %w", a.Model, err)
	}

	// Bounded artifact: the full raw response goes to disk, never inline. A
	// save failure after a PAID call is a hard error, never a silent success.
	artifact, err := writeDeepinfraArtifact(call.WorkDir, a.Settings.OutputDir, raw, contentType)
	if err != nil {
		return nil, fmt.Errorf("deepinfra_infer: HTTP %d from %s but saving the response artifact failed: %w", status, a.Model, err)
	}

	// Context copy: sanitized JSON only. Non-JSON/binary bytes NEVER enter
	// context — the note carries artifact metadata instead.
	text := ""
	if json.Valid(raw) {
		text = sanitizeDeepinfraContext(raw, deepinfraContextCap)
	}
	// (b) The context copy must never contain the exact credential, even when
	// the provider echoes it back in the response JSON. The artifact file keeps
	// the full raw response; only the context copy is redacted.
	if key != "" {
		text = strings.ReplaceAll(text, key, "[redacted:key]")
	}
	res := capabilities.NewTextResult(text)
	if res.Text == "" {
		res.Note = fmt.Sprintf("non-JSON response (%d bytes, content-type %s) saved to %s; raw bytes withheld from context (artifact only)", len(raw), contentType, artifact)
	} else {
		res.Note = fmt.Sprintf("POST %s → HTTP %d; full response (%d bytes) saved to %s", deepinfraEndpoint(a.Model), status, len(raw), artifact)
	}
	if profile != "" {
		res.Note += fmt.Sprintf(" (profile %s)", profile)
	}
	return res, nil
}

// deepinfraCredential resolves the configured DeepInfra profile + its API key.
// Credential eligibility requires an EXACT api.deepinfra.com base-URL host
// (config.DeepinfraHost) — the provider label alone is never sufficient. Zero
// configured profiles is a configuration error; several without an explicit
// selector is an ambiguity error. No key → hard error (never anonymous), read
// from the wired SecretStore only (fail-closed when the keychain is absent).
func deepinfraCredential(selector string, svc capabilities.Services) (key, profile string, err error) {
	if svc.Config == nil {
		return "", "", fmt.Errorf("deepinfra_infer: config unavailable; cannot resolve DeepInfra credentials")
	}
	var names []string
	for _, p := range svc.Config.CloudProfiles {
		if config.DeepinfraHost(p) == deepinfraNativeHost {
			names = append(names, p.Name)
		}
	}
	sort.Strings(names)
	switch {
	case len(names) == 0:
		return "", "", fmt.Errorf("deepinfra_infer: no DeepInfra cloud profile is configured (credential eligibility requires base_url host %s); add a profile with base_url https://api.deepinfra.com/… and store its API key in the keychain", deepinfraNativeHost)
	case len(names) == 1:
		if selector != "" && selector != names[0] {
			return "", "", fmt.Errorf("deepinfra_infer: profile %q is not a configured DeepInfra profile (configured: %s)", selector, strings.Join(names, ", "))
		}
	case selector == "":
		return "", "", fmt.Errorf("deepinfra_infer: multiple DeepInfra profiles are configured (%s); pass 'profile' to choose one", strings.Join(names, ", "))
	case !containsString(names, selector):
		return "", "", fmt.Errorf("deepinfra_infer: profile %q is not a configured DeepInfra profile (configured: %s)", selector, strings.Join(names, ", "))
	}
	profile = selector
	if profile == "" {
		profile = names[0]
	}
	if svc.Secrets == nil {
		return "", "", fmt.Errorf("deepinfra_infer: credential store unavailable in this execution environment; cannot read the key for profile %q", profile)
	}
	key, keyErr := svc.Secrets.Get(profile)
	if keyErr != nil || strings.TrimSpace(key) == "" {
		return "", "", fmt.Errorf("deepinfra_infer: no API key stored for DeepInfra profile %q (store the key in the keychain; anonymous DeepInfra calls are not attempted)", profile)
	}
	return key, profile, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// bindDeepinfraAttachments binds each attachment's content as a top-level
// input field. Exactly one of path / attachment_id must be set; a field
// already present in input, used by two attachments, or otherwise colliding
// is rejected explicitly — no silent overwrite. Content stays out of the
// model context: it goes straight into the HTTP body. used tracks the running
// encoded body budget and is advanced per attachment so the aggregate cap is
// enforced while binding (bounding peak allocation), not only at the end.
func bindDeepinfraAttachments(input map[string]any, attachments []deepinfraAttachment, call *capabilities.Call, used *int) error {
	// Pre-pass: reject duplicate fields before any file/key is read.
	seen := map[string]bool{}
	for _, at := range attachments {
		if strings.TrimSpace(at.Field) == "" {
			return fmt.Errorf("attachment requires non-empty 'field'")
		}
		if seen[at.Field] {
			return fmt.Errorf("attachment field %q is used more than once (duplicates are rejected)", at.Field)
		}
		seen[at.Field] = true
		if _, exists := input[at.Field]; exists {
			return fmt.Errorf("attachment field %q collides with a key already present in 'input' (explicit collision rejection)", at.Field)
		}
		// Encoding is validated in the PRE-PASS, before any file is opened or
		// attachment byte is read, so a missing/unknown encoding is an
		// explicit-encoding error and never a surprising file-not-found.
		switch at.Encoding {
		case "utf8", "base64", "data_uri":
		default:
			return fmt.Errorf("attachment %q: encoding must be explicitly 'utf8', 'base64', or 'data_uri', got %q", at.Field, at.Encoding)
		}
	}
	for _, at := range attachments {
		data, mediaType, err := deepinfraAttachmentBytes(at, call)
		if err != nil {
			return err
		}
		val, err := deepinfraEncodeAttachment(at, data, mediaType)
		if err != nil {
			return err
		}
		// Running encoded budget: count each attachment as it is bound (key +
		// quotes overhead slack included), so binding stops at the first
		// attachment that would exceed the aggregate cap. Peak memory is the
		// budget plus at most one attachment's encoding, not the unbounded sum.
		*used += len(val) + len(at.Field) + 16
		if *used > deepinfraMaxInputBytes {
			return fmt.Errorf("attachment %q: encoded request would exceed the %d-byte aggregate cap (input + encoded attachments)", at.Field, deepinfraMaxInputBytes)
		}
		input[at.Field] = val
	}
	return nil
}

// deepinfraAttachmentBytes fetches an attachment's raw bytes from exactly one
// source: a local file (workdir-relative, bounded Max+1 read) or a
// conversation image attachment (the narrow visionattach seam inspect_image
// reads). Exactly one of path / attachment_id must be set.
func deepinfraAttachmentBytes(at deepinfraAttachment, call *capabilities.Call) ([]byte, string, error) {
	hasPath := strings.TrimSpace(at.Path) != ""
	hasID := strings.TrimSpace(at.AttachmentID) != ""
	switch {
	case hasPath && hasID:
		return nil, "", fmt.Errorf("attachment %q: set exactly one of 'path' or 'attachment_id', not both", at.Field)
	case !hasPath && !hasID:
		return nil, "", fmt.Errorf("attachment %q: requires exactly one of 'path' or 'attachment_id'", at.Field)
	case hasID:
		if call.Svc.Attachments == nil {
			return nil, "", fmt.Errorf("attachment %q: attachment lookup is unavailable in this execution environment (the conversation image store is not wired); reattach the image this turn or use 'path'", at.Field)
		}
		data, mediaType, ok := call.Svc.Attachments.LookupAttachment(call.ConversationID, strings.TrimSpace(at.AttachmentID))
		if !ok || len(data) == 0 {
			return nil, "", fmt.Errorf("attachment %q: image attachment %q is not held for this conversation (stale or unknown ID); reattach the image this turn or use 'path'", at.Field, at.AttachmentID)
		}
		// Same per-attachment cap as local files: a conversation attachment
		// must not bypass the size bound.
		if len(data) > deepinfraMaxAttachmentBytes {
			return nil, "", fmt.Errorf("attachment %q: image attachment %q is %d bytes, over the %d-byte attachment cap", at.Field, at.AttachmentID, len(data), deepinfraMaxAttachmentBytes)
		}
		return data, mediaType, nil
	}
	path := deepinfraResolvePath(call.WorkDir, at.Path)
	if call.WorkDir == "" && !filepath.IsAbs(at.Path) {
		return nil, "", fmt.Errorf("attachment %q: no work directory available to resolve relative path %q", at.Field, at.Path)
	}
	f, err := os.Open(path) // #nosec G304 -- agent-supplied path, same trust level as read_file
	if err != nil {
		return nil, "", fmt.Errorf("attachment %q: open %s: %w", at.Field, at.Path, err)
	}
	defer f.Close()
	// Bounded read: Max+1 so an over-cap attachment is rejected explicitly
	// rather than allocated unbounded.
	data, err := io.ReadAll(io.LimitReader(f, deepinfraMaxAttachmentBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("attachment %q: read %s: %w", at.Field, at.Path, err)
	}
	if len(data) > deepinfraMaxAttachmentBytes {
		return nil, "", fmt.Errorf("attachment %q: %s is over the %d-byte attachment cap", at.Field, at.Path, deepinfraMaxAttachmentBytes)
	}
	// Local data URIs detect the media type from the actual bytes (PNG →
	// image/png, …) instead of a blanket application/octet-stream.
	return data, http.DetectContentType(data), nil
}

// deepinfraEncodeAttachment renders an attachment's bytes as its explicitly
// requested encoding. Never inferred, so image inputs cannot silently arrive
// malformed. data_uri keeps the conversation store's media type.
func deepinfraEncodeAttachment(at deepinfraAttachment, data []byte, mediaType string) (string, error) {
	switch at.Encoding {
	case "utf8":
		if !utf8.Valid(data) {
			return "", fmt.Errorf("attachment %q: content is not valid UTF-8; use encoding 'base64' or 'data_uri'", at.Field)
		}
		return string(data), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(data), nil
	case "data_uri":
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
	default:
		return "", fmt.Errorf("attachment %q: encoding must be explicitly 'utf8', 'base64', or 'data_uri', got %q", at.Field, at.Encoding)
	}
}

// deepinfraRequest performs exactly ONE https POST with Bearer auth. Redirects
// are refused (DeepInfra never needs them; following one could leak the
// Authorization header to another host) and there is no retry: a failed paid
// call must not silently re-bill. Headers/status are checked BEFORE the body
// is read: a non-2xx (>= 300 or < 200, including a non-followed 3xx) or an
// SSE response is rejected status-only — never a body read, never provider
// body text in the error, never a retry. Successes are read with a Max+1
// bound — overflow is an ERROR, never a silently truncated success.
func deepinfraRequest(ctx context.Context, endpoint, key string, body []byte, timeout time.Duration) (raw []byte, contentType string, status int, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("refusing redirect (deepinfra_infer posts directly to api.deepinfra.com only)")
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		if resp != nil {
			// Non-followed redirect: Go returns the closed 3xx response
			// alongside the CheckRedirect error. Status-only failure — no
			// provider body text, no retry.
			status := resp.StatusCode
			resp.Body.Close()
			return nil, "", status, fmt.Errorf("HTTP %d: refusing redirect (deepinfra_infer posts directly to api.deepinfra.com only; no retry)", status)
		}
		return nil, "", 0, err
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	contentType = resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// Header/status checks BEFORE reading the body: a large non-2xx or SSE
	// body is never read.
	if status < 200 || status >= 300 {
		return nil, contentType, status, fmt.Errorf("HTTP %d: provider returned a non-2xx status (status-only; no retry)", status)
	}
	if strings.HasPrefix(strings.ToLower(contentType), "text/event-stream") {
		return nil, contentType, status, fmt.Errorf("HTTP %d: unsupported response content-type text/event-stream (deepinfra_infer does not stream; no retry was attempted and this is not a success)", status)
	}
	// Max+1 read: detect overflow explicitly instead of truncating.
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, deepinfraMaxResponseBytes+1))
	if readErr != nil {
		return nil, "", status, fmt.Errorf("read response: %w", readErr)
	}
	if len(raw) > deepinfraMaxResponseBytes {
		return nil, "", status, fmt.Errorf("HTTP %d: response exceeds the %d-byte cap; refusing to truncate silently (no retry)", status, deepinfraMaxResponseBytes)
	}
	return raw, contentType, status, nil
}

// deepinfraArtifactExt maps a response content type to an artifact extension.
// Unknown types land in .bin; JSON in .json. No model-specific decoding —
// bytes are stored exactly as received.
func deepinfraArtifactExt(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "application/json":
		return ".json"
	case "text/plain":
		return ".txt"
	case "text/html":
		return ".html"
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/flac":
		return ".flac"
	case "audio/mp4", "audio/aac":
		return ".m4a"
	default:
		return ".bin"
	}
}

// writeDeepinfraArtifact saves the full raw response via EXCLUSIVE create
// (os.CreateTemp — an existing file is never clobbered) and returns the
// artifact path. outputDir may be omitted (default .cercano/deepinfra under
// the workdir); when relative it resolves against the workdir. No URLs are
// fetched, nothing is re-encoded, nothing is auto-deleted.
func writeDeepinfraArtifact(workDir, outputDir string, raw []byte, contentType string) (string, error) {
	dir := strings.TrimSpace(outputDir)
	if dir == "" {
		dir = ".cercano/deepinfra"
	}
	if !filepath.IsAbs(dir) {
		if workDir == "" {
			return "", fmt.Errorf("no work directory available to resolve relative output_dir %q", dir)
		}
		dir = filepath.Join(workDir, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create artifact dir: %w", err)
	}
	ext := deepinfraArtifactExt(contentType)
	// Safe short pattern: NO model string in the pattern — model ids like
	// "vendor/model" contain a path separator and os.CreateTemp rejects
	// patterns with separators. Uniqueness comes from CreateTemp's exclusive
	// random suffix, not from the name.
	f, err := os.CreateTemp(dir, "deepinfra-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create artifact: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(raw); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("write artifact: %w", err)
	}
	return f.Name(), nil
}

// sanitizeDeepinfraContext returns a sanitized JSON rendering of the response
// for the context copy: values longer than redactStringLen or base64-like at
// or beyond redactMinB64Len are replaced with a type tag + size, so blobs
// (embeddings, generated audio/image payloads) never reach the model. Numbers
// are preserved exactly (UseNumber). Output is byte-bounded by cap.
func sanitizeDeepinfraContext(raw []byte, cap int) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return ""
	}
	sanitized, err := json.Marshal(redactDeepinfraValue(v))
	if err != nil {
		return ""
	}
	return truncateDeepinfraText(string(sanitized), cap)
}

// redactDeepinfraValue rewrites leaf strings over the redaction length or
// base64-like blobs into "[redacted:<kind>:<len>]" markers, keeping structure
// and short values (ids, scores, model names) intact.
func redactDeepinfraValue(v any) any {
	switch t := v.(type) {
	case string:
		if len(t) > redactStringLen {
			return "[redacted:string:" + fmt.Sprint(len(t)) + "]"
		}
		if len(t) >= redactMinB64Len && deepinfraB64Like.MatchString(t) {
			return "[redacted:base64:" + fmt.Sprint(len(t)) + "]"
		}
		return t
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = redactDeepinfraValue(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = redactDeepinfraValue(e)
		}
		return out
	default:
		// numbers (json.Number), bools, nil pass through untouched
		return v
	}
}

// truncateDeepinfraText bounds the context copy at a rune boundary.
func truncateDeepinfraText(text string, cap int) string {
	if len(text) <= cap {
		return text
	}
	for len(text) > cap {
		text = text[:len(text)-1]
	}
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

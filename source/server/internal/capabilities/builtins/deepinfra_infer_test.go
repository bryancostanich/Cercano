package builtins

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
)

// deepinfraTestSvc wires Services with one/multiple DeepInfra profiles and a
// memory keychain, mirroring the host and worker wiring through
// toolstack.CapDeps.Secrets.
func deepinfraTestSvc(t *testing.T, profiles ...string) (capabilities.Call, secrets.Store) {
	t.Helper()
	st := secrets.NewMemory()
	var cps []config.CloudProfile
	for i, p := range profiles {
		cps = append(cps, config.CloudProfile{
			Name:    p,
			Flavor:  "chat_completions",
			BaseURL: "https://api.deepinfra.com/v1/openai",
		})
		if err := st.Set(p, fmt.Sprintf("key-%d", i)); err != nil {
			t.Fatalf("seed key: %v", err)
		}
	}
	return capabilities.Call{
		Args:           nil,
		WorkDir:        t.TempDir(),
		ConversationID: "conv",
		Svc: capabilities.Services{
			Config:  &config.Config{CloudProfiles: cps},
			Secrets: st,
		},
	}, st
}

func runDeepinfra(t *testing.T, call capabilities.Call, args string) (*capabilities.Result, error) {
	t.Helper()
	call.Args = json.RawMessage(args)
	return DeepInfraInfer().Execute(context.Background(), &call)
}

func TestDeepInfraInferSingleProfileRunsNativeEndpoint(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{"answer": "ok"})
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL + "/v1/inference/" + model }
	defer func() { deepinfraEndpoint = old }()

	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	res, err := runDeepinfra(t, call, `{"model":"zai-org/GLM-5.3","input":{"prompt":"hi"},"settings":{"timeout_seconds":5}}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/v1/inference/zai-org/GLM-5.3" {
		t.Errorf("native inference path = %q, want /v1/inference/<model>", gotPath)
	}
	if gotAuth != "Bearer key-0" {
		t.Errorf("Authorization = %q, want configured profile key", gotAuth)
	}
	if gotBody["prompt"] != "hi" {
		t.Errorf("body = %v, want raw input passthrough", gotBody)
	}
	if res.Note == "" || !strings.Contains(res.Note, "HTTP 200") {
		t.Errorf("note = %q, want HTTP status + artifact path", res.Note)
	}
	if !strings.Contains(res.Note, ".cercano/deepinfra/") {
		t.Errorf("note = %q, want artifact path", res.Note)
	}
	start := strings.Index(res.Note, "saved to ") + len("saved to ")
	path := res.Note[start:]
	if end := strings.Index(path, " (profile"); end >= 0 {
		path = path[:end]
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("artifact file missing: %v (note %q)", err, res.Note)
	}
}

func TestDeepInfraInferNoProfileConfigured(t *testing.T) {
	call, _ := deepinfraTestSvc(t)
	_, err := runDeepinfra(t, call, `{"model":"x/y"}`)
	if err == nil || !strings.Contains(err.Error(), "no DeepInfra cloud profile is configured") {
		t.Fatalf("err = %v, want configuration guidance", err)
	}
}

func TestDeepInfraInferAmbiguousProfiles(t *testing.T) {
	call, _ := deepinfraTestSvc(t, "main-deepinfra", "dispatch-deepinfra")
	_, err := runDeepinfra(t, call, `{"model":"x/y"}`)
	// Profiles are listed in SORTED order (deepinfraCredential sorts names).
	if err == nil || !strings.Contains(err.Error(), "multiple DeepInfra profiles") || !strings.Contains(err.Error(), "dispatch-deepinfra, main-deepinfra") {
		t.Fatalf("err = %v, want ambiguity error listing profiles", err)
	}
}

func TestDeepInfraInferProfileSelectorResolves(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	call, _ := deepinfraTestSvc(t, "main-deepinfra", "dispatch-deepinfra")
	if _, err := runDeepinfra(t, call, `{"model":"x/y","profile":"dispatch-deepinfra"}`); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotAuth != "Bearer key-1" {
		t.Errorf("Authorization = %q, want dispatch-deepinfra key", gotAuth)
	}
}

func TestDeepInfraInferMissingKeyNoAnonymousFallback(t *testing.T) {
	call, st := deepinfraTestSvc(t, "main-deepinfra")
	if err := st.Delete("main-deepinfra"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := runDeepinfra(t, call, `{"model":"x/y"}`)
	if err == nil || !strings.Contains(err.Error(), "no API key stored") {
		t.Fatalf("err = %v, want missing-key error", err)
	}
}

func TestDeepInfraInferAttachmentsBinding(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	dir := t.TempDir()
	img := filepath.Join(dir, "img.bin")
	if err := os.WriteFile(img, []byte("IMAGEBYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	txt := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(txt, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	// IMAGEBYTES base64-encodes to SU1BR0VCWVRFUw==; encoding is explicit.
	args := fmt.Sprintf(`{"model":"x/y","input":{"k":1},"attachments":[{"path":%q,"field":"image","encoding":"base64"},{"path":%q,"field":"text","encoding":"utf8"}]}`, img, txt)
	if _, err := runDeepinfra(t, call, args); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotBody["image"] != "SU1BR0VCWVRFUw==" {
		t.Errorf("image field = %v, want base64 of file bytes", gotBody["image"])
	}
	if gotBody["text"] != "hello" {
		t.Errorf("utf8 field = %v, want file text", gotBody["text"])
	}
	if gotBody["k"] != float64(1) {
		t.Errorf("input passthrough lost: %v", gotBody)
	}
}

func TestDeepInfraInferAttachmentEncodingRequired(t *testing.T) {
	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	// The path does not exist on purpose: encoding is validated in the
	// pre-pass, BEFORE any file is opened, so this must be an
	// explicit-encoding error, not a file-not-found error.
	_, err := runDeepinfra(t, call, `{"model":"x/y","attachments":[{"path":"/tmp/a","field":"image"}]}`)
	if err == nil || !strings.Contains(err.Error(), "encoding must be explicitly 'utf8', 'base64', or 'data_uri'") {
		t.Fatalf("err = %v, want explicit-encoding error", err)
	}
}

func TestDeepInfraInferNoPaidRetriesSingleAttempt(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	if _, err := runDeepinfra(t, call, `{"model":"x/y"}`); err == nil {
		t.Fatal("want error on HTTP 500")
	}
	if calls != 1 {
		t.Errorf("attempts = %d, want exactly 1 (no paid retries)", calls)
	}
}

func TestDeepInfraInferRefusesRedirects(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://evil.example/infer")
		w.WriteHeader(http.StatusFound)
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	_, err := runDeepinfra(t, call, `{"model":"x/y"}`)
	if err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatalf("err = %v, want redirect refusal", err)
	}
}

func TestDeepInfraInferBase64NeverInContext(t *testing.T) {
	// 100 KiB base64-looking field in the response plus a long text field.
	b64 := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo=", 2300)
	long := strings.Repeat("x", redactStringLen+10)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": b64, "detail": long})
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	res, err := runDeepinfra(t, call, `{"model":"x/y"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(res.Text, b64) || strings.Contains(res.Text, long) {
		t.Fatal("base64/oversized values leaked into context")
	}
	if !strings.Contains(res.Text, "[redacted:") || !strings.Contains(res.Note, "saved to") {
		t.Errorf("want redaction markers + artifact note, got text=%q note=%q", res.Text, res.Note)
	}
	// The artifact holds the FULL unsanitized response.
	start := strings.Index(res.Note, "saved to ") + len("saved to ")
	path := res.Note[start:]
	if end := strings.Index(path, " (profile"); end >= 0 {
		path = path[:end]
	}
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil || len(raw) < len(b64) {
		t.Errorf("artifact missing full response: %v", err)
	}
}

func TestDeepInfraInferModelPathValidated(t *testing.T) {
	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	for _, bad := range []string{"", "https://evil.com/x", "../etc/passwd", "a b"} {
		if _, err := runDeepinfra(t, call, fmt.Sprintf(`{"model":%q}`, bad)); err == nil {
			t.Errorf("model %q: want rejection", bad)
		}
	}
}

// ─── Regression tests for current tool bugs ─────────────────────────────────

// fakeAttachmentStore implements capabilities.AttachmentLookup for tests.
type fakeAttachmentStore struct {
	data      []byte
	mediaType string
	ok        bool
}

func (f fakeAttachmentStore) LookupAttachment(convID, attachmentID string) ([]byte, string, bool) {
	return f.data, f.mediaType, f.ok
}

// TestDeepInfraInferResponseOverCapRejected proves the Max+1 read bound: a
// response one byte over the cap is an explicit ERROR, never a truncation.
func TestDeepInfraInferResponseOverCapRejected(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write deepinfraMaxResponseBytes+1 bytes without allocating them all.
		chunk := make([]byte, 64<<10)
		total := deepinfraMaxResponseBytes + 1
		for written := 0; written < total; {
			c := len(chunk)
			if rem := total - written; rem < c {
				c = rem
			}
			if _, err := w.Write(chunk[:c]); err != nil {
				return
			}
			written += c
		}
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	_, err := runDeepinfra(t, call, `{"model":"x/y"}`)
	if err == nil || !strings.Contains(err.Error(), "exceeds the") || !strings.Contains(err.Error(), "refusing to truncate") {
		t.Fatalf("err = %v, want explicit overflow rejection (never silent truncation)", err)
	}
}

// TestDeepInfraInferExactCredentialHostOnly proves credential eligibility is
// the EXACT api.deepinfra.com host: a profile labelled "deepinfra" that points
// at another host is never eligible, and never receives the key.
func TestDeepInfraInferExactCredentialHostOnly(t *testing.T) {
	// A "deepinfra"-labelled profile with a foreign host is NOT eligible.
	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	call.Svc.Config.CloudProfiles = []config.CloudProfile{{Name: "deepinfra", Flavor: "chat_completions", BaseURL: "https://evil.example/v1"}}
	_, err := runDeepinfra(t, call, `{"model":"x/y"}`)
	if err == nil || !strings.Contains(err.Error(), "no DeepInfra cloud profile is configured") {
		t.Fatalf("err = %v, want foreign-host profile rejected as ineligible", err)
	}

	// Mixed: one eligible profile + one foreign-host profile. The foreign name
	// must not resolve; the eligible one must.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer key-0" {
			t.Errorf("Authorization = %q, want the eligible profile's key only", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()
	call2, _ := deepinfraTestSvc(t, "main-deepinfra", "deepinfra")
	call2.Svc.Config.CloudProfiles[1].BaseURL = "https://evil.example/v1"
	if _, err := runDeepinfra(t, call2, `{"model":"x/y","profile":"deepinfra"}`); err == nil || !strings.Contains(err.Error(), "not a configured DeepInfra profile") {
		t.Fatalf("err = %v, want foreign-host selector rejected", err)
	}
	if _, err := runDeepinfra(t, call2, `{"model":"x/y","profile":"main-deepinfra"}`); err != nil {
		t.Fatalf("eligible profile selector rejected: %v", err)
	}
}

// TestDeepInfraInferAttachmentWorkdirBindingAndCollisions proves relative
// attachment paths resolve against the work directory and that collisions are
// rejected before anything is bound.
func TestDeepInfraInferAttachmentWorkdirBindingAndCollisions(t *testing.T) {
	var gotBody map[string]any
	var gotRaw []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRaw, _ = io.ReadAll(r.Body)
		_ = json.Unmarshal(gotRaw, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("sub", "img.bin")
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("WORKDIRBYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	// WORKDIRBYTES base64-encodes to "V09SSERSWUJZVEVT".
	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	call.WorkDir = dir
	if _, err := runDeepinfra(t, call, fmt.Sprintf(`{"model":"x/y","attachments":[{"path":%q,"field":"image","encoding":"base64"}]}`, rel)); err != nil {
		t.Fatalf("relative workdir path failed to bind: %v", err)
	}
	if gotBody["image"] != "V09SS0RJUkJZVEVT" {
		t.Errorf("image = %v, want base64 of workdir-relative file", gotBody["image"])
	}

	// Collision with an existing input key is rejected.
	if _, err := runDeepinfra(t, call, `{"model":"x/y","input":{"image":1},"attachments":[{"path":"sub/img.bin","field":"image","encoding":"base64"}]}`); err == nil || !strings.Contains(err.Error(), "collides with a key already present") {
		t.Errorf("err = %v, want input-key collision rejection", err)
	}
	// Duplicate field across two attachments is rejected.
	dup := fmt.Sprintf(`{"model":"x/y","attachments":[{"path":%q,"field":"image","encoding":"base64"},{"path":%q,"field":"image","encoding":"utf8"}]}`, rel, rel)
	if _, err := runDeepinfra(t, call, dup); err == nil || !strings.Contains(err.Error(), "used more than once") {
		t.Errorf("err = %v, want duplicate-field rejection", err)
	}
}

// TestDeepInfraInferNumericFidelity proves arbitrary numbers survive both
// directions exactly (UseNumber; no float coercion): 2^53+1 would be corrupted
// by a float64 round trip.
func TestDeepInfraInferNumericFidelity(t *testing.T) {
	var gotRaw []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRaw, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"n": 9007199254740993, "score": 0.1})
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	res, err := runDeepinfra(t, call, `{"model":"x/y","input":{"n":9007199254740993}}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(string(gotRaw), "9007199254740993") {
		t.Errorf("request body = %s, want exact big-integer preserved (no float coercion)", gotRaw)
	}
	if !strings.Contains(res.Text, "9007199254740993") {
		t.Errorf("context text = %q, want exact response integer preserved", res.Text)
	}
}

// TestDeepInfraInferContextCap proves the context copy is byte-bounded: a JSON
// response made of many small fields (which survive redaction) is still
// capped, not streamed whole into context.
func TestDeepInfraInferContextCap(t *testing.T) {
	fields := map[string]any{}
	for i := 0; i < 2000; i++ {
		fields[fmt.Sprintf("f%d", i)] = strings.Repeat("v", 30)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(fields)
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	res, err := runDeepinfra(t, call, `{"model":"x/y"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Text) > deepinfraContextCap || res.Text == "" {
		t.Errorf("context text = %d bytes, want >0 and capped at %d", len(res.Text), deepinfraContextCap)
	}
}

// TestDeepInfraInferBinaryNeverInline proves non-JSON/binary bytes NEVER enter
// context: text is empty, the note carries metadata, and the full raw bytes
// land in the artifact only.
func TestDeepInfraInferBinaryNeverInline(t *testing.T) {
	bin := []byte("BINARY\x00\x01\x02PAYLOAD")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(bin)
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	res, err := runDeepinfra(t, call, `{"model":"x/y"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Text != "" {
		t.Errorf("context text = %q, want empty (binary withheld)", res.Text)
	}
	if !strings.Contains(res.Note, "non-JSON response") || !strings.Contains(res.Note, "withheld from context") {
		t.Errorf("note = %q, want artifact-only metadata note", res.Note)
	}
	start := strings.Index(res.Note, "saved to ") + len("saved to ")
	path := res.Note[start:]
	if end := strings.IndexAny(path, ";("); end >= 0 {
		path = path[:end]
	}
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil || !bytes.Equal(raw, bin) {
		t.Errorf("artifact bytes: err=%v len=%d, want exact full binary payload", err, len(raw))
	}
	if !strings.HasSuffix(strings.TrimSpace(path), ".bin") {
		t.Errorf("artifact = %s, want .bin extension for unknown content type", path)
	}
}

// TestDeepInfraInferAttachmentIDAndDataURI proves the attachment_id seam binds
// a conversation image as an explicit data URI, and a stale/unknown ID fails
// closed.
func TestDeepInfraInferAttachmentIDAndDataURI(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	img := []byte("PNGDATA")
	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	call.Svc.Attachments = fakeAttachmentStore{data: img, mediaType: "image/png", ok: true}
	wantURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(img)
	if _, err := runDeepinfra(t, call, `{"model":"x/y","attachments":[{"attachment_id":"img1","field":"image","encoding":"data_uri"}]}`); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotBody["image"] != wantURI {
		t.Errorf("image = %v, want data URI of the conversation attachment", gotBody["image"])
	}

	// Stale/unknown attachment ID: fail closed, no anonymous substitute.
	call.Svc.Attachments = fakeAttachmentStore{ok: false}
	_, err := runDeepinfra(t, call, `{"model":"x/y","attachments":[{"attachment_id":"gone","field":"image","encoding":"data_uri"}]}`)
	if err == nil || !strings.Contains(err.Error(), "not held for this conversation") {
		t.Fatalf("err = %v, want stale attachment-ID failure", err)
	}
}

// ─── Final bounded-safety regression tests (a)–(h) ──────────────────────────

// (a) TestDeepInfraInferNon2xxStatusOnlyNoBodyEcho proves a provider error
// body (which could echo the credential or attacker-controlled text) never
// reaches the error message: the failure is status-only.
func TestDeepInfraInferNon2xxStatusOnlyNoBodyEcho(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid key: key-0"},"detail":"key-0 rejected"}`))
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	_, err := runDeepinfra(t, call, `{"model":"x/y"}`)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("err = %v, want HTTP 401 status-only failure", err)
	}
	if strings.Contains(err.Error(), "key-0") {
		t.Errorf("error message echoes provider body text: %v", err)
	}
}

// (a) TestDeepInfraInferRedirectStatusOnly proves a non-followed 3xx surfaces
// its status (and nothing from the response body): no provider text, no retry.
func TestDeepInfraInferRedirectStatusOnly(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "https://evil.example/infer")
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write([]byte(`{"detail":"key-0 redirect hint"}`))
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	_, err := runDeepinfra(t, call, `{"model":"x/y"}`)
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") || !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatalf("err = %v, want HTTP 302 status-only redirect refusal", err)
	}
	if strings.Contains(err.Error(), "key-0") {
		t.Errorf("redirect error echoes body text: %v", err)
	}
	if calls != 1 {
		t.Errorf("attempts = %d, want exactly 1 (no paid retries)", calls)
	}
}

// (b) TestDeepInfraInferCredentialNeverInErrorsOrContext proves the exact
// credential never reaches model context (even when the provider echoes it
// back in the JSON response) and never appears in a transport error.
func TestDeepInfraInferCredentialNeverInErrorsOrContext(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"echo": "key-0", "bearer": "Bearer key-0"})
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	res, err := runDeepinfra(t, call, `{"model":"x/y"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(res.Text, "key-0") {
		t.Errorf("context echoes the credential: %q", res.Text)
	}
	if !strings.Contains(res.Text, "[redacted:key]") {
		t.Errorf("want explicit key redaction marker in context, got %q", res.Text)
	}

	// Transport error path: a dead endpoint never prints the key.
	call2, _ := deepinfraTestSvc(t, "main-deepinfra")
	dead := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return "http://127.0.0.1:1/v1/inference/x/y" }
	defer func() { deepinfraEndpoint = dead }()
	_, err = runDeepinfra(t, call2, `{"model":"x/y"}`)
	if err == nil {
		t.Fatal("want transport error")
	}
	if strings.Contains(err.Error(), "key-0") {
		t.Errorf("transport error prints the credential: %v", err)
	}
}

// (c) TestDeepInfraInferAttachmentIDCap proves attachment_id content is
// subject to the same per-attachment cap as local files.
func TestDeepInfraInferAttachmentIDCap(t *testing.T) {
	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	call.Svc.Attachments = fakeAttachmentStore{data: make([]byte, deepinfraMaxAttachmentBytes+1), mediaType: "image/png", ok: true}
	_, err := runDeepinfra(t, call, `{"model":"x/y","attachments":[{"attachment_id":"big","field":"image","encoding":"base64"}]}`)
	if err == nil || !strings.Contains(err.Error(), "attachment cap") {
		t.Fatalf("err = %v, want attachment cap enforcement for attachment_id", err)
	}
}

// (d) TestDeepInfraInferAggregateBudgetRunning proves attachments are bound
// under a RUNNING encoded budget: four attachment_id blobs at the
// per-attachment cap base64-encode past the 32 MiB aggregate cap and must be
// rejected while binding (bounded peak), not after encoding everything.
func TestDeepInfraInferAggregateBudgetRunning(t *testing.T) {
	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	call.Svc.Attachments = fakeAttachmentStore{data: make([]byte, deepinfraMaxAttachmentBytes), mediaType: "image/png", ok: true}
	atts := make([]string, 4)
	for i := range atts {
		atts[i] = fmt.Sprintf(`{"attachment_id":"a%d","field":"f%d","encoding":"base64"}`, i, i)
	}
	args := `{"model":"x/y","attachments":[` + strings.Join(atts, ",") + "]}"
	_, err := runDeepinfra(t, call, args)
	if err == nil || !strings.Contains(err.Error(), "aggregate") {
		t.Fatalf("err = %v, want aggregate 32 MiB budget enforcement during binding", err)
	}
}

// (d) TestDeepInfraInferArgsCapBeforeDecode proves oversized call.Args are
// rejected before JSON decode allocates.
func TestDeepInfraInferArgsCapBeforeDecode(t *testing.T) {
	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	call.Args = json.RawMessage(make([]byte, deepinfraMaxInputBytes+1))
	_, err := DeepInfraInfer().Execute(context.Background(), &call)
	if err == nil || !strings.Contains(err.Error(), "over the") || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("err = %v, want pre-decode args cap rejection", err)
	}
}

// (e) TestDeepInfraInferLocalDataURIDetectsMIME proves a local file's data URI
// uses http.DetectContentType (PNG → image/png), not a blanket
// application/octet-stream.
func TestDeepInfraInferLocalDataURIDetectsMIME(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	pngBytes := append([]byte("\x89PNG\r\n\x1a\n"), []byte("fakeihdrdata")...)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "img.png"), pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	call.WorkDir = dir
	if _, err := runDeepinfra(t, call, `{"model":"x/y","attachments":[{"path":"img.png","field":"image","encoding":"data_uri"}]}`); err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	if gotBody["image"] != want {
		t.Errorf("data URI = %v, want detected image/png media type", gotBody["image"])
	}
}

// (f) TestDeepInfraInferModelRejectsWhitespace proves whitespace in any model
// segment is rejected (a space is not a valid model id character).
func TestDeepInfraInferModelRejectsWhitespace(t *testing.T) {
	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	for _, bad := range []string{"a b", "vendor/ model", " vendor/model", "vendor/model\n"} {
		if _, err := runDeepinfra(t, call, fmt.Sprintf(`{"model":%q}`, bad)); err == nil {
			t.Errorf("model %q: want whitespace rejection", bad)
		}
	}
}

// (g) TestDeepInfraInferSSERejectedBeforeBody proves SSE responses are
// rejected from headers alone, before any large body read — status-only, no
// paid retry, no body echo.
func TestDeepInfraInferSSERejectedBeforeBody(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"detail\":\"key-0\"}\n\n"))
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	_, err := runDeepinfra(t, call, `{"model":"x/y"}`)
	if err == nil || !strings.Contains(err.Error(), "text/event-stream") {
		t.Fatalf("err = %v, want SSE rejection", err)
	}
	if strings.Contains(err.Error(), "key-0") {
		t.Errorf("SSE error echoes body text: %v", err)
	}
	if calls != 1 {
		t.Errorf("attempts = %d, want exactly 1 (no paid retries)", calls)
	}
}

// (g) TestDeepInfraInfer3xxStatusOnly proves a non-followed 3xx without a
// Location (304) is rejected status-only, never treated as a success.
func TestDeepInfraInfer3xxStatusOnly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer ts.Close()
	old := deepinfraEndpoint
	deepinfraEndpoint = func(model string) string { return ts.URL }
	defer func() { deepinfraEndpoint = old }()

	call, _ := deepinfraTestSvc(t, "main-deepinfra")
	_, err := runDeepinfra(t, call, `{"model":"x/y"}`)
	if err == nil || !strings.Contains(err.Error(), "HTTP 304") {
		t.Fatalf("err = %v, want 3xx rejected as status-only failure", err)
	}
}

// (h) TestDeepInfraInferDescriptionTopLevelClarification guards the corrected
// wording: nested input JSON is allowed; only attachment bindings are
// top-level.
func TestDeepInfraInferDescriptionTopLevelClarification(t *testing.T) {
	desc := DeepInfraInfer().Description()
	if strings.Contains(desc, "model-documented JSON body; top-level fields only") {
		t.Errorf("description still implies nested input JSON is unsupported: %q", desc)
	}
	if !strings.Contains(desc, "arbitrarily nested JSON allowed") || !strings.Contains(desc, "attachment bindings") {
		t.Errorf("description must clarify only attachment bindings are top-level: %q", desc)
	}
}

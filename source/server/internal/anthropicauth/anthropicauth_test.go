package anthropicauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewPKCE_ChallengeIsS256OfVerifier(t *testing.T) {
	pk, err := newPKCE()
	if err != nil {
		t.Fatalf("newPKCE: %v", err)
	}
	if pk.verifier == "" || pk.challenge == "" {
		t.Fatalf("empty pkce pair: %+v", pk)
	}
	sum := sha256.Sum256([]byte(pk.verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if pk.challenge != want {
		t.Errorf("challenge = %q, want S256(verifier) = %q", pk.challenge, want)
	}
	// A second pair must differ (fresh entropy).
	pk2, _ := newPKCE()
	if pk2.verifier == pk.verifier {
		t.Error("two PKCE verifiers collided; entropy not fresh")
	}
}

func TestStart_BuildsLoopbackAuthorizeURL(t *testing.T) {
	p, err := Flow{}.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	u, err := url.Parse(p.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse AuthorizeURL: %v", err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != DefaultAuthorizeURL {
		t.Errorf("authorize base = %q, want %q", got, DefaultAuthorizeURL)
	}
	q := u.Query()
	checks := map[string]string{
		"client_id":             DefaultClientID,
		"response_type":         "code",
		"code_challenge_method": "S256",
		"code":                  "true",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("query %s = %q, want %q", k, got, want)
		}
	}
	if q.Get("scope") != strings.Join(DefaultScopes, " ") {
		t.Errorf("scope = %q, want %q", q.Get("scope"), strings.Join(DefaultScopes, " "))
	}
	if q.Get("code_challenge") == "" || q.Get("state") == "" {
		t.Error("missing code_challenge or state")
	}
	redir := q.Get("redirect_uri")
	if !strings.HasPrefix(redir, "http://localhost:") || !strings.HasSuffix(redir, "/callback") {
		t.Errorf("redirect_uri = %q, want http://localhost:<port>/callback", redir)
	}
}

func TestTokenSet_Expired(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	fresh := TokenSet{ExpiresAt: now.Add(time.Hour)}
	if fresh.Expired(now) {
		t.Error("token an hour out should not be expired")
	}
	// Inside the 30s skew window it must be treated as expired.
	near := TokenSet{ExpiresAt: now.Add(10 * time.Second)}
	if !near.Expired(now) {
		t.Error("token within skew window should be expired")
	}
}

func TestTokenSet_EncodeDecodeRoundTrip(t *testing.T) {
	in := TokenSet{Access: "a", Refresh: "r", ExpiresAt: time.Unix(1_700_000_000, 0).UTC()}
	enc, err := in.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := DecodeTokenSet(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Access != in.Access || out.Refresh != in.Refresh || !out.ExpiresAt.Equal(in.ExpiresAt) {
		t.Errorf("round trip mismatch: %+v vs %+v", out, in)
	}
}

// stubTokenServer returns an httptest server standing in for the Anthropic
// token endpoint. It records the last decoded request body and replies with
// the given token JSON.
func stubTokenServer(t *testing.T, reply map[string]any, capture *map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("token request method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("token request Content-Type = %q, want application/json", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if capture != nil {
			_ = json.Unmarshal(body, capture)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	}))
}

func TestFlow_Refresh(t *testing.T) {
	var got map[string]string
	srv := stubTokenServer(t, map[string]any{
		"access_token":  "new-access",
		"refresh_token": "new-refresh",
		"expires_in":    3600,
	}, &got)
	defer srv.Close()

	f := Flow{TokenURL: srv.URL}
	ts, err := f.Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got["grant_type"] != "refresh_token" {
		t.Errorf("grant_type = %q, want refresh_token", got["grant_type"])
	}
	if got["refresh_token"] != "old-refresh" {
		t.Errorf("refresh_token = %q, want old-refresh", got["refresh_token"])
	}
	if got["client_id"] != DefaultClientID {
		t.Errorf("client_id = %q, want %q", got["client_id"], DefaultClientID)
	}
	if ts.Access != "new-access" || ts.Refresh != "new-refresh" {
		t.Errorf("token set = %+v, want new-access/new-refresh", ts)
	}
	if ts.Expired(time.Now()) {
		t.Error("freshly refreshed token reports expired")
	}
}

func TestFlow_RefreshKeepsOldRefreshWhenOmitted(t *testing.T) {
	srv := stubTokenServer(t, map[string]any{
		"access_token": "new-access",
		"expires_in":   3600,
	}, nil)
	defer srv.Close()

	ts, err := Flow{TokenURL: srv.URL}.Refresh(context.Background(), "keep-me")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if ts.Refresh != "keep-me" {
		t.Errorf("refresh = %q, want the old token kept when the response omits one", ts.Refresh)
	}
}

// TestPending_Wait drives the full loopback round-trip: Start binds a port,
// a simulated browser hits /callback with code+state, and Wait exchanges the
// code at the stub token endpoint.
func TestPending_Wait(t *testing.T) {
	var got map[string]string
	srv := stubTokenServer(t, map[string]any{
		"access_token":  "acc",
		"refresh_token": "ref",
		"expires_in":    3600,
	}, &got)
	defer srv.Close()

	p, err := Flow{TokenURL: srv.URL}.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	u, _ := url.Parse(p.AuthorizeURL)
	state := u.Query().Get("state")
	redirect := u.Query().Get("redirect_uri")

	type res struct {
		ts  *TokenSet
		err error
	}
	resCh := make(chan res, 1)
	go func() {
		ts, err := p.Wait(context.Background())
		resCh <- res{ts, err}
	}()

	// Simulated browser redirect back to the loopback listener. Read the full
	// page body: it must arrive intact (the teardown race that showed as
	// "localhost refused to connect" would truncate or reset it) and carry
	// the success copy the user is told to act on.
	cbURL := redirect + "?code=the-code&state=" + url.QueryEscape(state)
	hresp, err := http.Get(cbURL)
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	page, err := io.ReadAll(hresp.Body)
	hresp.Body.Close()
	if err != nil {
		t.Fatalf("reading callback page body: %v", err)
	}
	if !strings.Contains(string(page), "Successfully authenticated") {
		t.Errorf("callback page missing success copy; got %q", string(page))
	}

	r := <-resCh
	if r.err != nil {
		t.Fatalf("Wait: %v", r.err)
	}
	if r.ts.Access != "acc" {
		t.Errorf("access = %q, want acc", r.ts.Access)
	}
	if got["grant_type"] != "authorization_code" || got["code"] != "the-code" {
		t.Errorf("exchange body = %+v, want authorization_code/the-code", got)
	}
	if got["code_verifier"] == "" {
		t.Error("exchange did not send code_verifier")
	}
	if got["redirect_uri"] != redirect {
		t.Errorf("exchange redirect_uri = %q, want %q", got["redirect_uri"], redirect)
	}
}

func TestPending_WaitRejectsStateMismatch(t *testing.T) {
	srv := stubTokenServer(t, map[string]any{"access_token": "x", "expires_in": 3600}, nil)
	defer srv.Close()

	p, err := Flow{TokenURL: srv.URL}.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	u, _ := url.Parse(p.AuthorizeURL)
	redirect := u.Query().Get("redirect_uri")

	resCh := make(chan error, 1)
	go func() {
		_, err := p.Wait(context.Background())
		resCh <- err
	}()

	hresp, err := http.Get(redirect + "?code=c&state=WRONG")
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	hresp.Body.Close()

	if err := <-resCh; err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Errorf("Wait err = %v, want state mismatch", err)
	}
}

func TestPending_RedeemSplitsEmbeddedState(t *testing.T) {
	var got map[string]string
	srv := stubTokenServer(t, map[string]any{"access_token": "a", "expires_in": 3600}, &got)
	defer srv.Close()

	p, err := Flow{TokenURL: srv.URL}.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := p.Redeem(context.Background(), "  code123#state456  "); err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if got["code"] != "code123" || got["state"] != "state456" {
		t.Errorf("redeem parsed code/state = %q/%q, want code123/state456", got["code"], got["state"])
	}
}

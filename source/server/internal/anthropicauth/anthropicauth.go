// Package anthropicauth implements the Claude subscription sign-in used by
// the "subscription" cloud route: an OAuth 2.0 authorization-code flow with
// PKCE against claude.ai, a loopback redirect on an ephemeral local port
// (RFC 8252), token refresh, and a keychain-backed token source.
//
// UNOFFICIAL: this rides Claude Code's public OAuth client id (there is no
// sanctioned third-party registration path) and pairs with a Claude Code
// identity system block on each Messages call. It can stop working whenever
// Anthropic tightens enforcement. The wizard labels it accordingly.
//
// Verified live 2026-07-13 against a Claude Max subscription: the authorize
// endpoint, client id, scopes, and PKCE params come from a real `claude
// login` authorize URL; a direct Bearer call to /v1/messages returned HTTP
// 200. The token/refresh ENDPOINT and request-body shape below are the
// widely-used values but were NOT captured live — they are confirmed the
// first time a real sign-in or refresh runs. Everything is injectable on
// Flow so a captured correction is a one-line change.
package anthropicauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultClientID is Claude Code's public OAuth client id (from a live
// `claude login` authorize URL, 2026-07-13).
const DefaultClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

// DefaultAuthorizeURL is where the user approves the grant (verified live).
const DefaultAuthorizeURL = "https://claude.ai/oauth/authorize"

// DefaultTokenURL is the code-exchange / refresh endpoint. UNVERIFIED — this
// is the standard Anthropic value; confirm on first real sign-in or refresh
// (or via a one-off mitmproxy capture) and correct here if wrong.
const DefaultTokenURL = "https://console.anthropic.com/v1/oauth/token"

// DefaultScopes mirrors Claude Code's requested set verbatim (from the live
// authorize URL). The load-bearing scope for us is user:inference; the rest
// are carried to match Claude Code's footprint exactly.
var DefaultScopes = []string{
	"org:create_api_key",
	"user:profile",
	"user:inference",
	"user:sessions:claude_code",
	"user:mcp_servers",
	"user:file_upload",
}

// TokenSet is the stored credential bundle for a signed-in account. It is
// JSON-encoded into the secrets store under the profile name — the same
// place API keys live, never in a client-side file. Unlike the ChatGPT flow
// there is no account id; the bearer token is self-sufficient.
type TokenSet struct {
	Access    string    `json:"access"`
	Refresh   string    `json:"refresh"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Expired reports whether the access token needs a refresh. A small skew
// window avoids handing out a token that dies mid-request.
func (t TokenSet) Expired(now time.Time) bool {
	return now.After(t.ExpiresAt.Add(-30 * time.Second))
}

// Encode/DecodeTokenSet are the secrets-store representation.
func (t TokenSet) Encode() (string, error) {
	b, err := json.Marshal(t)
	return string(b), err
}

func DecodeTokenSet(s string) (TokenSet, error) {
	var t TokenSet
	if err := json.Unmarshal([]byte(s), &t); err != nil {
		return TokenSet{}, fmt.Errorf("anthropic token decode: %w", err)
	}
	return t, nil
}

// Flow performs the PKCE loopback sign-in and token refresh. Zero value is
// usable; every endpoint and the client id default to the constants above.
type Flow struct {
	ClientID     string       // defaults to DefaultClientID
	AuthorizeURL string       // defaults to DefaultAuthorizeURL
	TokenURL     string       // defaults to DefaultTokenURL
	Scopes       []string     // defaults to DefaultScopes
	Client       *http.Client // defaults to http.DefaultClient
	UserAgent    string       // defaults to "cercano"
}

func (f Flow) clientID() string {
	if f.ClientID != "" {
		return f.ClientID
	}
	return DefaultClientID
}

func (f Flow) authorizeBase() string {
	if f.AuthorizeURL != "" {
		return f.AuthorizeURL
	}
	return DefaultAuthorizeURL
}

func (f Flow) tokenURL() string {
	if f.TokenURL != "" {
		return f.TokenURL
	}
	return DefaultTokenURL
}

func (f Flow) scopes() []string {
	if len(f.Scopes) > 0 {
		return f.Scopes
	}
	return DefaultScopes
}

func (f Flow) httpClient() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return http.DefaultClient
}

func (f Flow) userAgent() string {
	if f.UserAgent != "" {
		return f.UserAgent
	}
	return "cercano"
}

// pkcePair is a PKCE code verifier and its S256 challenge.
type pkcePair struct {
	verifier  string
	challenge string
}

// newPKCE generates a fresh verifier (32 random bytes, base64url) and its
// S256 challenge, per RFC 7636.
func newPKCE() (pkcePair, error) {
	v, err := randomToken()
	if err != nil {
		return pkcePair{}, err
	}
	sum := sha256.Sum256([]byte(v))
	return pkcePair{verifier: v, challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

// randomToken returns 32 cryptographically-random bytes, base64url-encoded.
// Used for both the PKCE verifier and the CSRF state.
func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("anthropic oauth: entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// authorizeURL builds the URL the user visits to approve the grant. code=true
// asks the authorize page to also display the code on-screen, enabling the
// manual-paste fallback for headless/SSH use where the loopback can't be hit.
func (f Flow) authorizeURL(redirectURI, challenge, state string) string {
	q := url.Values{
		"code":                  {"true"},
		"client_id":             {f.clientID()},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"scope":                 {strings.Join(f.scopes(), " ")},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	return f.authorizeBase() + "?" + q.Encode()
}

// exchange trades an approved authorization code (plus its PKCE verifier and
// the original redirect_uri/state) for a token set.
func (f Flow) exchange(ctx context.Context, code, state, verifier, redirectURI string) (*TokenSet, error) {
	return f.tokenRequest(ctx, map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"state":         state,
		"client_id":     f.clientID(),
		"redirect_uri":  redirectURI,
		"code_verifier": verifier,
	})
}

// Refresh exchanges a refresh token for a fresh access token. Anthropic's
// refresh tokens rotate (single-use); when the response omits a new one we
// keep the old, but normally a new refresh token replaces it.
func (f Flow) Refresh(ctx context.Context, refreshToken string) (*TokenSet, error) {
	ts, err := f.tokenRequest(ctx, map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     f.clientID(),
	})
	if err != nil {
		return nil, err
	}
	if ts.Refresh == "" {
		ts.Refresh = refreshToken
	}
	return ts, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// tokenRequest POSTs a JSON body to the token endpoint and decodes the token
// response. Anthropic's token endpoint takes JSON (not form-encoded).
func (f Flow) tokenRequest(ctx context.Context, body map[string]string) (*TokenSet, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURL(), bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", f.userAgent())
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("anthropic token request: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, fmt.Errorf("anthropic token request: decode: %w", err)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("anthropic token request: no access token in response")
	}
	expires := tr.ExpiresIn
	if expires <= 0 {
		expires = 3600
	}
	return &TokenSet{
		Access:    tr.AccessToken,
		Refresh:   tr.RefreshToken,
		ExpiresAt: time.Now().Add(time.Duration(expires) * time.Second),
	}, nil
}

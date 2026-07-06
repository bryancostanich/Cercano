// Package chatgptauth implements the ChatGPT subscription sign-in used by
// the "chatgpt" cloud route: OpenAI's device-authorization flow against
// auth.openai.com, token refresh, and account-ID extraction from the issued
// JWTs.
//
// UNOFFICIAL: this rides the Codex CLI's OAuth client (there is no
// third-party registration path) and can stop working at any time. The
// wizard labels it accordingly. Endpoint shapes verified against a shipping
// third-party implementation — see
// docs/research/cloud-subscription-auth/verified-findings.md.
package chatgptauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// codexClientID is the Codex CLI's public OAuth client — the only client
// the deviceauth endpoints accept.
const codexClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

// DefaultIssuer is OpenAI's auth origin. Injectable on Flow for tests.
const DefaultIssuer = "https://auth.openai.com"

// pollSafetyMargin pads the server-requested poll interval to absorb clock
// skew, mirroring what existing integrations do.
const pollSafetyMargin = 3 * time.Second

// TokenSet is the stored credential bundle for a signed-in account. It is
// JSON-encoded into the secrets store under the profile name — same place
// API keys live, never in any client-side file.
type TokenSet struct {
	Access    string    `json:"access"`
	Refresh   string    `json:"refresh"`
	ExpiresAt time.Time `json:"expires_at"`
	AccountID string    `json:"account_id,omitempty"`
}

// Expired reports whether the access token needs a refresh. A small skew
// window avoids using a token that dies mid-request.
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
		return TokenSet{}, fmt.Errorf("chatgpt token decode: %w", err)
	}
	return t, nil
}

// Flow performs the device-authorization sign-in. Zero value is usable;
// Issuer and Client default sensibly.
type Flow struct {
	Issuer string       // defaults to DefaultIssuer
	Client *http.Client // defaults to http.DefaultClient
	// UserAgent identifies us to the endpoints (they reject blank UAs).
	UserAgent string
}

func (f Flow) issuer() string {
	if f.Issuer != "" {
		return f.Issuer
	}
	return DefaultIssuer
}

func (f Flow) client() *http.Client {
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

// Pending is a started device authorization awaiting user confirmation.
type Pending struct {
	// VerificationURL is where the user signs in; UserCode is what they
	// type there. Both are for display by whatever client is attached.
	VerificationURL string
	UserCode        string

	deviceAuthID string
	interval     time.Duration
	flow         Flow
}

type deviceStartResponse struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	Interval     string `json:"interval"`
}

// Start begins a device authorization: returns the URL + code to show the
// user. Poll then blocks until they finish (or ctx cancels).
func (f Flow) Start(ctx context.Context) (*Pending, error) {
	body, _ := json.Marshal(map[string]string{"client_id": codexClientID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		f.issuer()+"/api/accounts/deviceauth/usercode", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", f.userAgent())
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("chatgpt device auth start: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chatgpt device auth start: HTTP %d", resp.StatusCode)
	}
	var ds deviceStartResponse
	if err := json.NewDecoder(resp.Body).Decode(&ds); err != nil {
		return nil, fmt.Errorf("chatgpt device auth start: %w", err)
	}
	if ds.DeviceAuthID == "" || ds.UserCode == "" {
		return nil, fmt.Errorf("chatgpt device auth start: incomplete response")
	}
	secs, _ := strconv.Atoi(ds.Interval)
	if secs < 1 {
		secs = 5
	}
	return &Pending{
		VerificationURL: f.issuer() + "/codex/device",
		UserCode:        ds.UserCode,
		deviceAuthID:    ds.DeviceAuthID,
		// The safety margin folds in here so tests can zero the whole wait.
		interval: time.Duration(secs)*time.Second + pollSafetyMargin,
		flow:     f,
	}, nil
}

type devicePollResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
}

type tokenResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// Poll blocks until the user approves (returning the tokens), the server
// rejects the attempt, or ctx is canceled. HTTP 403/404 mean "still
// pending" in this flow; anything else non-200 is terminal.
func (p *Pending) Poll(ctx context.Context) (*TokenSet, error) {
	body, _ := json.Marshal(map[string]string{
		"device_auth_id": p.deviceAuthID,
		"user_code":      p.UserCode,
	})
	wait := p.interval
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			p.flow.issuer()+"/api/accounts/deviceauth/token", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", p.flow.userAgent())
		resp, err := p.flow.client().Do(req)
		if err != nil {
			return nil, fmt.Errorf("chatgpt device auth poll: %w", err)
		}
		switch resp.StatusCode {
		case http.StatusOK:
			var dp devicePollResponse
			err := json.NewDecoder(resp.Body).Decode(&dp)
			resp.Body.Close()
			if err != nil {
				return nil, fmt.Errorf("chatgpt device auth poll: %w", err)
			}
			return p.flow.exchange(ctx, dp)
		case http.StatusForbidden, http.StatusNotFound:
			resp.Body.Close() // still pending
		default:
			resp.Body.Close()
			return nil, fmt.Errorf("chatgpt device auth rejected: HTTP %d", resp.StatusCode)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// exchange trades the approved device authorization for tokens via the
// standard authorization-code grant (the poll response carries the code and
// its PKCE verifier).
func (f Flow) exchange(ctx context.Context, dp devicePollResponse) (*TokenSet, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {dp.AuthorizationCode},
		"redirect_uri":  {f.issuer() + "/deviceauth/callback"},
		"client_id":     {codexClientID},
		"code_verifier": {dp.CodeVerifier},
	}
	return f.tokenRequest(ctx, form)
}

// Refresh exchanges a refresh token for a fresh access token.
func (f Flow) Refresh(ctx context.Context, refreshToken string) (*TokenSet, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {codexClientID},
	}
	ts, err := f.tokenRequest(ctx, form)
	if err != nil {
		return nil, err
	}
	// Some refreshes omit a new refresh token; keep the old one.
	if ts.Refresh == "" {
		ts.Refresh = refreshToken
	}
	return ts, nil
}

func (f Flow) tokenRequest(ctx context.Context, form url.Values) (*TokenSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		f.issuer()+"/oauth/token", bytes.NewReader([]byte(form.Encode())))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", f.userAgent())
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("chatgpt token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chatgpt token request: HTTP %d", resp.StatusCode)
	}
	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, fmt.Errorf("chatgpt token request: %w", err)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("chatgpt token request: no access token in response")
	}
	expires := tr.ExpiresIn
	if expires <= 0 {
		expires = 3600
	}
	ts := &TokenSet{
		Access:    tr.AccessToken,
		Refresh:   tr.RefreshToken,
		ExpiresAt: time.Now().Add(time.Duration(expires) * time.Second),
	}
	ts.AccountID = firstNonEmptyStr(accountIDFromJWT(tr.IDToken), accountIDFromJWT(tr.AccessToken))
	return ts, nil
}

// jwtClaims is the subset of OpenAI's token claims that carries the ChatGPT
// account ID (three known locations, checked in order).
type jwtClaims struct {
	ChatGPTAccountID string `json:"chatgpt_account_id"`
	Organizations    []struct {
		ID string `json:"id"`
	} `json:"organizations"`
	Auth struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
	} `json:"https://api.openai.com/auth"`
}

// accountIDFromJWT extracts the ChatGPT account ID from a JWT's payload
// segment. Returns "" on any malformed input — the ID is an optimization
// (request header), not a requirement.
func accountIDFromJWT(token string) string {
	parts := bytes.Split([]byte(token), []byte("."))
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(string(parts[1]))
	if err != nil {
		return ""
	}
	var c jwtClaims
	if json.Unmarshal(payload, &c) != nil {
		return ""
	}
	switch {
	case c.ChatGPTAccountID != "":
		return c.ChatGPTAccountID
	case c.Auth.ChatGPTAccountID != "":
		return c.Auth.ChatGPTAccountID
	case len(c.Organizations) > 0:
		return c.Organizations[0].ID
	}
	return ""
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

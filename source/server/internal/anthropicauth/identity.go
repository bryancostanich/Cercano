package anthropicauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"cercano/source/server/pkg/accountidentity"
)

// fillIdentity is advisory and runs only during sign-in, never while rendering
// settings or refreshing tokens. An unavailable profile cannot invalidate login.
func (f Flow) fillIdentity(ctx context.Context, ts *TokenSet) {
	endpoint := f.ProfileURL
	if endpoint == "" {
		// Never send a test/custom endpoint's token to another host implicitly.
		if f.TokenURL != "" && f.TokenURL != DefaultTokenURL {
			return
		}
		endpoint = "https://api.anthropic.com/api/oauth/profile"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+ts.Access)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", f.userAgent())
	// No redirects: never forward a bearer token through a profile redirect.
	client := *f.httpClient()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var profile struct {
		Account json.RawMessage `json:"account"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&profile) != nil {
		return
	}
	identity := accountidentity.Decode(profile.Account)
	if ts.Identity.Email == "" {
		ts.Identity.Email = identity.Email
	}
	if ts.Identity.Name == "" {
		ts.Identity.Name = identity.Name
	}
}

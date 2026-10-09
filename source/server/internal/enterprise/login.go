package enterprise

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

type tokenResponse struct {
	OrganizationID string `json:"organization_id"`
	MemberID       string `json:"member_id"`
	HostID         string `json:"host_id"`
	ExpiresIn      int    `json:"expires_in"`
	TokenType      string `json:"token_type"`
	Access         string `json:"access_token"`
	Refresh        string `json:"refresh_token"`
}

func (t tokenResponse) valid() bool {
	validToken := func(raw string) bool {
		org, token, ok := strings.Cut(raw, ".")
		decoded, e := base64.RawURLEncoding.DecodeString(token)
		return ok && org == t.OrganizationID && e == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == token
	}
	return t.TokenType == "Bearer" && validUUID(t.OrganizationID) && validUUID(t.MemberID) && t.ExpiresIn > 0 && t.ExpiresIn <= 600 && validToken(t.Access) && validToken(t.Refresh)
}
func randomToken() string {
	var value [32]byte
	_, _ = rand.Read(value[:])
	return base64.RawURLEncoding.EncodeToString(value[:])
}

// Login opens the system browser. Tokens are never sent through the callback;
// the one-use code is exchanged over HTTPS with the locally held PKCE verifier.
func (m *Manager) Login(ctx context.Context, server, org string, openBrowser func(string) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active > 0 {
		return ErrBusy
	}
	if m.credentials != nil {
		return errors.New("log out before switching enterprise connections")
	}
	if openBrowser == nil || !validUUID(org) {
		return errors.New("valid organization and browser opener required")
	}
	if e := m.validOrigin(server); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return errors.New("cannot open enterprise sign-in callback")
	}
	defer listener.Close()
	redirect := "http://" + listener.Addr().String() + "/enterprise/callback"
	state, verifier := randomToken(), randomToken()
	challenge := sha256.Sum256([]byte(verifier))
	codes := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /enterprise/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		q := r.URL.Query()
		code := q.Get("code")
		if r.Host != listener.Addr().String() || r.Header.Get("Origin") != "" || len(q["state"]) != 1 || len(q["code"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 || !strings.HasPrefix(code, org+".") {
			http.Error(w, "Invalid sign-in callback", 400)
			return
		}
		select {
		case codes <- code:
			_, _ = w.Write([]byte("Sign-in response received. Return to Cercano."))
		default:
			http.Error(w, "Sign-in already received", 409)
		}
	})
	callback := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	defer callback.Close()
	go func() { _ = callback.Serve(listener) }()
	target := server + "/auth/native/start?" + url.Values{"organization_id": {org}, "redirect_uri": {redirect}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}}.Encode()
	if openBrowser(target) != nil {
		return errors.New("could not open enterprise sign-in browser")
	}
	var code string
	select {
	case <-ctx.Done():
		return errors.New("enterprise sign-in canceled or timed out")
	case code = <-codes:
	}
	var grant tokenResponse
	if e = m.request(ctx, server, "POST", "/auth/native/token", "", map[string]string{"grant_type": "authorization_code", "code": code, "code_verifier": verifier, "redirect_uri": redirect}, &grant, 8192); e != nil {
		return e
	}
	if !grant.valid() || grant.OrganizationID != org || grant.HostID != "" {
		return ErrDenied
	}
	c := Credentials{Server: server, OrganizationID: org, MemberID: grant.MemberID, Access: grant.Access, Refresh: grant.Refresh, ExpiresAt: time.Now().Add(time.Duration(grant.ExpiresIn) * time.Second)}
	committed := false
	defer func() {
		if !committed {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = m.request(cleanup, server, "POST", "/auth/native/logout", c.Refresh, nil, nil, 0)
		}
	}()
	var host v1.RegisterHostResponse
	if e = m.request(ctx, server, "POST", "/v1/organizations/"+org+"/hosts", c.Access, v1.RegisterHostRequest{DisplayName: "Cercano CLI", ClientVersion: m.options.ClientVersion, Platform: runtime.GOOS + "/" + runtime.GOARCH}, &host, 8192); e != nil {
		return e
	}
	if host.SchemaVersion != v1.Version || host.Scope.OrganizationID != org || host.Scope.UserID != c.MemberID || !validUUID(host.Scope.HostID) {
		return ErrDenied
	}
	c.HostID = host.Scope.HostID
	if e = m.persist(&c); e != nil {
		return e
	}
	committed = true
	m.blocked = true
	m.bundle = nil
	return nil
}

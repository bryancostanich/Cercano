package chatgptauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeJWT builds a syntactically valid JWT carrying the given payload.
func fakeJWT(t *testing.T, payload any) string {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	seg := base64.RawURLEncoding.EncodeToString
	return seg([]byte(`{"alg":"none"}`)) + "." + seg(b) + "." + seg([]byte("sig"))
}

// fakeIssuer stands in for auth.openai.com. pendingPolls is how many poll
// attempts return 403 before approval.
func fakeIssuer(t *testing.T, pendingPolls int32) *httptest.Server {
	t.Helper()
	var polls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["client_id"] != codexClientID {
			http.Error(w, "bad client", http.StatusBadRequest)
			return
		}
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "no ua", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"device_auth_id": "dev-123",
			"user_code":      "ABCD-1234",
			"interval":       "0",
		})
	})
	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["device_auth_id"] != "dev-123" || body["user_code"] != "ABCD-1234" {
			http.Error(w, "unknown device auth", http.StatusBadRequest)
			return
		}
		if atomic.AddInt32(&polls, 1) <= pendingPolls {
			http.Error(w, "pending", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_code": "code-xyz",
			"code_verifier":      "verifier-xyz",
		})
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		grant := r.PostForm.Get("grant_type")
		switch grant {
		case "authorization_code":
			if r.PostForm.Get("code") != "code-xyz" || r.PostForm.Get("code_verifier") != "verifier-xyz" {
				http.Error(w, "bad code", http.StatusBadRequest)
				return
			}
		case "refresh_token":
			if r.PostForm.Get("refresh_token") != "refresh-old" {
				http.Error(w, "bad refresh", http.StatusBadRequest)
				return
			}
		default:
			http.Error(w, "bad grant "+grant, http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"access_token":  "access-" + grant,
			"expires_in":    3600,
			"id_token":      "",
			"refresh_token": "",
		}
		if grant == "authorization_code" {
			resp["refresh_token"] = "refresh-new"
			resp["id_token"] = fakeJWTStatic
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	return httptest.NewServer(mux)
}

// fakeJWTStatic is set in TestMain-ish fashion by the first test that needs
// it; simpler than threading *testing.T through the mux.
var fakeJWTStatic string

func TestDeviceFlowEndToEnd(t *testing.T) {
	fakeJWTStatic = fakeJWT(t, map[string]any{"chatgpt_account_id": "acct-42"})
	srv := fakeIssuer(t, 2) // two pending polls before approval
	defer srv.Close()

	f := Flow{Issuer: srv.URL}
	p, err := f.Start(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if p.UserCode != "ABCD-1234" {
		t.Errorf("user code: got %q", p.UserCode)
	}
	if !strings.HasPrefix(p.VerificationURL, srv.URL) || !strings.HasSuffix(p.VerificationURL, "/codex/device") {
		t.Errorf("verification url: got %q", p.VerificationURL)
	}

	p.interval = 0 // no wait between poll retries in tests
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ts, err := p.Poll(ctx)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if ts.Access != "access-authorization_code" {
		t.Errorf("access: got %q", ts.Access)
	}
	if ts.Refresh != "refresh-new" {
		t.Errorf("refresh: got %q", ts.Refresh)
	}
	if ts.AccountID != "acct-42" {
		t.Errorf("account id: got %q", ts.AccountID)
	}
	if ts.Expired(time.Now()) {
		t.Error("fresh token reports expired")
	}
	if !ts.Expired(time.Now().Add(2 * time.Hour)) {
		t.Error("2h-old token reports valid")
	}
}

func TestRefreshKeepsOldRefreshTokenWhenOmitted(t *testing.T) {
	fakeJWTStatic = fakeJWT(t, map[string]any{"chatgpt_account_id": "acct-42"})
	srv := fakeIssuer(t, 0)
	defer srv.Close()

	f := Flow{Issuer: srv.URL}
	ts, err := f.Refresh(context.Background(), "refresh-old")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if ts.Access != "access-refresh_token" {
		t.Errorf("access: got %q", ts.Access)
	}
	if ts.Refresh != "refresh-old" {
		t.Errorf("refresh: want old token preserved, got %q", ts.Refresh)
	}
}

func TestPollTerminalRejection(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"device_auth_id": "dev-123", "user_code": "ABCD-1234", "interval": "0",
		})
	})
	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusUnauthorized) // terminal, not pending
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := Flow{Issuer: srv.URL}
	p, err := f.Start(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	p.interval = 0
	if _, err := p.Poll(context.Background()); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("want terminal rejection, got %v", err)
	}
}

func TestPollHonorsContextCancel(t *testing.T) {
	fakeJWTStatic = fakeJWT(t, nil)
	srv := fakeIssuer(t, 1<<30) // never approves
	defer srv.Close()

	f := Flow{Issuer: srv.URL}
	p, err := f.Start(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	p.interval = 0
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := p.Poll(ctx); err == nil {
		t.Fatal("want ctx error")
	}
}

func TestAccountIDClaimLocations(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		want    string
	}{
		{"top-level", map[string]any{"chatgpt_account_id": "a1"}, "a1"},
		{"auth-namespace", map[string]any{"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "a2"}}, "a2"},
		{"organizations", map[string]any{"organizations": []map[string]string{{"id": "org-3"}}}, "org-3"},
		{"none", map[string]any{"sub": "x"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := accountIDFromJWT(fakeJWT(t, tc.payload)); got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
	if got := accountIDFromJWT("not-a-jwt"); got != "" {
		t.Errorf("malformed jwt: want empty, got %q", got)
	}
}

func TestTokenSetRoundTrip(t *testing.T) {
	in := TokenSet{Access: "a", Refresh: "r", ExpiresAt: time.Now().Round(time.Second), AccountID: "acct"}
	enc, err := in.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := DecodeTokenSet(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Access != in.Access || out.Refresh != in.Refresh || out.AccountID != in.AccountID || !out.ExpiresAt.Equal(in.ExpiresAt) {
		t.Errorf("round trip mismatch: %+v vs %+v", out, in)
	}
	if _, err := DecodeTokenSet("{broken"); err == nil {
		t.Error("want decode error")
	}
}

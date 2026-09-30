package chatgptauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestDisplayIdentityClaims(t *testing.T) {
	cases := []struct{ payload, email, name string }{
		{`{"email":"chat@example.com","name":"Person"}`, "chat@example.com", "Person"},
		{`{"https://api.openai.com/profile":{"email":"nested@example.com","name":"Nested"}}`, "nested@example.com", "Nested"},
		{`{"email":27,"name":[]}`, "", ""},
		{`{"email":"  clean@example.com\n","name":"P\u001b\u202eerson"}`, "clean@example.com", "Person"},
		{`not json`, "", ""},
	}
	for _, tc := range cases {
		token := "header." + base64.RawURLEncoding.EncodeToString([]byte(tc.payload)) + ".signature"
		got := identityFromJWT(token)
		if got.Email != tc.email || got.Name != tc.name {
			t.Fatalf("identity=%+v for %s", got, tc.payload)
		}
	}
	for _, token := range []string{"", "opaque-access-token", "a.%not-base64%.b"} {
		if got := identityFromJWT(token); got.Display() != "" {
			t.Fatal("invalid token produced label")
		}
	}
}

func TestTokenExchangeIdentityPrefersEmailAcrossTokens(t *testing.T) {
	jwt := func(payload string) string {
		return "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"access_token": jwt(`{"email":"access@example.com"}`), "id_token": jwt(`{"name":"Person","chatgpt_account_id":"stable-account"}`), "refresh_token": "r"})
	}))
	defer srv.Close()
	ts, err := (Flow{Issuer: srv.URL}).tokenRequest(context.Background(), url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	if ts.Identity.Email != "access@example.com" || ts.Identity.Name != "Person" || ts.AccountID != "stable-account" {
		t.Fatalf("identity=%+v", ts.Identity)
	}
}

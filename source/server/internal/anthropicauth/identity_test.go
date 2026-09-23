package anthropicauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIdentityFromTokenOrAdvisoryProfile(t *testing.T) {
	cases := []struct {
		name, account, profile, email string
		status                        int
	}{
		{"token", `{"email_address":"claude@example.com"}`, "", "claude@example.com", 200},
		{"profile", "null", `{"account":{"email":"profile@example.com","display_name":"Person"}}`, "profile@example.com", 200},
		{"profile-fails", "null", `failure`, "", 503},
		{"profile-malformed", "null", `not json`, "", 200},
		{"optional-fields-malformed", `{"email_address":17}`, `{"account":{"email":[]}}`, "", 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookups := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					fmt.Fprintf(w, `{"access_token":"test-access","refresh_token":"test-refresh","account":%s}`, tc.account)
					return
				}
				lookups++
				if r.Header.Get("Authorization") != "Bearer test-access" {
					t.Error("missing profile authorization")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.profile)
			}))
			defer srv.Close()
			flow := Flow{TokenURL: srv.URL + "/token", ProfileURL: srv.URL + "/profile"}
			ts, err := flow.exchange(context.Background(), "code", "state", "verifier", "redirect")
			if err != nil {
				t.Fatal(err)
			}
			if ts.Access != "test-access" || ts.Identity.Email != tc.email {
				t.Fatalf("incorrect token/identity: identity=%+v", ts.Identity)
			}
			if tc.name == "token" && lookups != 0 {
				t.Fatal("unnecessary profile request")
			}
			encoded, err := ts.Encode()
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeTokenSet(encoded)
			if err != nil || decoded.Identity != ts.Identity {
				t.Fatal("identity not retained")
			}
		})
	}
}
func TestIdentityLookupDoesNotFollowRedirects(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	ts := &TokenSet{Access: "test-token"}
	(Flow{ProfileURL: redirect.URL}).fillIdentity(context.Background(), ts)
	if reached {
		t.Fatal("followed identity redirect")
	}
}
func TestIdentityLookupCancellationIsOptional(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	ts := &TokenSet{Access: "still-valid"}
	(Flow{ProfileURL: srv.URL}).fillIdentity(ctx, ts)
	if ts.Access != "still-valid" || ts.Identity.Display() != "" {
		t.Fatal("lookup failure invalidated token")
	}
}

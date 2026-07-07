package chatgptauth

import (
	"context"
	"testing"
	"time"

	"cercano/source/server/internal/secrets"
)

func TestSourceReturnsStoredTokenWhenValid(t *testing.T) {
	store := secrets.NewMemory()
	ts := TokenSet{Access: "acc", Refresh: "ref", AccountID: "acct-1", ExpiresAt: time.Now().Add(time.Hour)}
	if err := Save(store, "p", ts); err != nil {
		t.Fatal(err)
	}
	// A flow pointing at a dead issuer proves no refresh is attempted.
	s := NewSource(store, "p", Flow{Issuer: "http://127.0.0.1:0"})
	access, acct, err := s.Token(context.Background())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if access != "acc" || acct != "acct-1" {
		t.Errorf("got access=%q acct=%q", access, acct)
	}
}

func TestSourceRefreshesExpiredToken(t *testing.T) {
	srv := fakeIssuer(t, 0)
	defer srv.Close()
	store := secrets.NewMemory()
	// Expired token with the refresh token the fake issuer accepts, plus an
	// account id the refresh response won't carry (must be preserved).
	ts := TokenSet{Access: "old", Refresh: "refresh-old", AccountID: "acct-keep", ExpiresAt: time.Now().Add(-time.Hour)}
	if err := Save(store, "p", ts); err != nil {
		t.Fatal(err)
	}
	s := NewSource(store, "p", Flow{Issuer: srv.URL})
	access, acct, err := s.Token(context.Background())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if access != "access-refresh_token" {
		t.Errorf("access: got %q", access)
	}
	if acct != "acct-keep" {
		t.Errorf("account id not preserved: got %q", acct)
	}
	// The refreshed set must be persisted: next load is valid and non-expired.
	raw, _ := store.Get("p")
	got, err := DecodeTokenSet(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Access != "access-refresh_token" || got.Refresh != "refresh-old" || got.Expired(time.Now()) {
		t.Errorf("persisted set wrong: %+v", got)
	}
}

func TestSourceErrorsWhenExpiredNoRefresh(t *testing.T) {
	store := secrets.NewMemory()
	ts := TokenSet{Access: "old", Refresh: "", ExpiresAt: time.Now().Add(-time.Hour)}
	if err := Save(store, "p", ts); err != nil {
		t.Fatal(err)
	}
	s := NewSource(store, "p", Flow{})
	if _, _, err := s.Token(context.Background()); err == nil {
		t.Fatal("want error for expired token with no refresh")
	}
}

func TestSourceErrorsWhenProfileMissing(t *testing.T) {
	s := NewSource(secrets.NewMemory(), "absent", Flow{})
	if _, _, err := s.Token(context.Background()); err == nil {
		t.Fatal("want error for missing profile")
	}
}

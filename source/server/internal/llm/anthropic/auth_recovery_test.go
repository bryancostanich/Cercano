package anthropic

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/secrets"
)

// Exercise the actual source and SDK wrapping, rather than a string-shaped
// imitation of an OAuth failure.
func TestAuthRecoveryExpiredCredentialThroughSDK(t *testing.T) {
	store := secrets.NewMemory()
	if err := anthropicauth.Save(store, "work-claude", anthropicauth.TokenSet{Access: "expired", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	source := anthropicauth.NewSource(store, "work-claude", anthropicauth.Flow{})
	c := NewClient(Config{Route: "subscription", BaseURL: "http://127.0.0.1:1", TokenSource: source})
	err := chatErr(t, c)
	if got := llm.ClassOf(err); got != llm.ErrLoginRequired {
		t.Fatalf("class=%s error=%v", got, err)
	}
}

func TestAuthRecoveryRefreshRejectionThroughSDK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"revoked"}`))
	}))
	defer srv.Close()
	store := secrets.NewMemory()
	anthropicauth.Save(store, "work-claude", anthropicauth.TokenSet{Access: "expired", Refresh: "old", ExpiresAt: time.Now().Add(-time.Hour)})
	c := NewClient(Config{Route: "subscription", BaseURL: "http://127.0.0.1:1", TokenSource: anthropicauth.NewSource(store, "work-claude", anthropicauth.Flow{TokenURL: srv.URL})})
	if err := chatErr(t, c); llm.ClassOf(err) != llm.ErrLoginRequired {
		t.Fatalf("class=%s error=%v", llm.ClassOf(err), err)
	}
}

func TestAuthRecoverySubscriptionPermissionDenial(t *testing.T) {
	c, _ := fixture(t, 403, nil, `{"type":"error","error":{"type":"permission_error","message":"forbidden"}}`)
	if err := chatErr(t, c); llm.ClassOf(err) != llm.ErrPermission {
		t.Fatalf("class=%s error=%v", llm.ClassOf(err), err)
	}
}

func TestAuthRecoveryMissingSourceIsConfigurationFailure(t *testing.T) {
	c := NewClient(Config{Route: RouteSubscription, BaseURL: "http://127.0.0.1:1"})
	err := chatErr(t, c)
	if llm.ClassOf(err) != llm.ErrCredential {
		t.Fatalf("class=%s error=%v", llm.ClassOf(err), err)
	}
}

func TestAuthRecoveryHTTP401UsesSubscriptionIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"sensitive-diagnostic"}}`))
	}))
	defer srv.Close()
	store := secrets.NewMemory()
	if err := anthropicauth.Save(store, "work-claude", anthropicauth.TokenSet{Access: "fresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	c := NewClient(Config{Route: RouteSubscription, BaseURL: srv.URL, TokenSource: anthropicauth.NewSource(store, "work-claude", anthropicauth.Flow{})})
	err := chatErr(t, c)
	var credential *llm.CredentialError
	if !errors.As(err, &credential) || credential.Profile != "work-claude" || llm.ClassOf(err) != llm.ErrLoginRequired {
		t.Fatalf("lost identity: %v", err)
	}
	if strings.Contains(err.Error(), "sensitive-diagnostic") {
		t.Fatal("unsafe diagnostic")
	}
}

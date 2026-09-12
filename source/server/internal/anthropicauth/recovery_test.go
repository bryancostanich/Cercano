package anthropicauth

import (
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/secrets"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type unavailableStore struct{}

func (unavailableStore) Get(string) (string, error) { return "", errors.New("keychain secret-detail") }
func (unavailableStore) Set(string, string) error   { return errors.New("keychain secret-detail") }
func TestRecoveryCredentialSourceReasons(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		class     llm.ErrorClass
		reason    string
	}{
		{"absent", "", llm.ErrLoginRequired, llm.CredentialMissing},
		{"corrupt", "not-json-secret", llm.ErrCredential, llm.CredentialMalformed},
		{"empty object", "{}", llm.ErrCredential, llm.CredentialMalformed},
		{"expired", `{"access":"old","expires_at":"2000-01-01T00:00:00Z"}`, llm.ErrLoginRequired, llm.CredentialExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := secrets.NewMemory()
			if tc.raw != "" {
				store.Set("work", tc.raw)
			}
			source := NewSource(store, "work", Flow{})
			_, err := source.Token(context.Background())
			var failure *llm.CredentialError
			if !errors.As(err, &failure) || failure.Class != tc.class || failure.Reason != tc.reason || failure.Profile != "work" || failure.Method != llm.AuthSubscription {
				t.Fatalf("got %+v", err)
			}
			if strings.Contains(err.Error(), "not-json-secret") {
				t.Fatal("secret leaked")
			}
		})
	}
	source := NewSource(unavailableStore{}, "work", Flow{})
	_, err := source.Token(context.Background())
	if llm.ClassOf(err) != llm.ErrCredential || strings.Contains(err.Error(), "secret-detail") {
		t.Fatalf("store error: %v", err)
	}
}
func TestRecoveryRefreshEndpointClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		class  llm.ErrorClass
	}{
		{"rejected", 400, `{"error":"invalid_grant","error_description":"secret"}`, llm.ErrLoginRequired},
		{"bad client", 401, `{"error":"invalid_client"}`, llm.ErrCredential},
		{"outage", 503, `{"error":"server_error"}`, llm.ErrBusy},
		{"unknown", 400, `{"error":"secret"}`, llm.ErrCredential},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) }))
			defer srv.Close()
			store := secrets.NewMemory()
			if err := Save(store, "work", TokenSet{Access: "expired", Refresh: "secret", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
				t.Fatal(err)
			}
			source := NewSource(store, "work", Flow{TokenURL: srv.URL})
			_, err := source.Token(context.Background())
			if llm.ClassOf(err) != tc.class || strings.Contains(err.Error(), "secret") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

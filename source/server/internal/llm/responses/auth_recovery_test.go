package responses

import (
	"cercano/source/server/internal/chatgptauth"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/secrets"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

type recoveryFailedTokens struct{ err error }

func (s recoveryFailedTokens) Token(context.Context) (string, string, error) { return "", "", s.err }
func TestAuthRecoveryRefreshFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want llm.ErrorClass
	}{
		{"network", &url.Error{Op: "Post", URL: "https://example.invalid/token", Err: syscall.ECONNRESET}, llm.ErrNetwork},
		{"cancel", context.Canceled, ""},
		{"store", errors.New("keychain unavailable"), llm.ErrCredential},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{tokens: recoveryFailedTokens{tc.err}}
			req, _ := http.NewRequest("POST", "http://example.invalid", nil)
			err := c.authorize(context.Background(), req)
			if tc.name == "cancel" {
				if err != context.Canceled {
					t.Fatalf("cancellation wrapped: %v", err)
				}
				return
			}
			if llm.ClassOf(err) != tc.want {
				t.Fatalf("class=%s want=%s error=%v", llm.ClassOf(err), tc.want, err)
			}
		})
	}
}

func TestAuthRecoveryMissingSourceIsConfigurationFailure(t *testing.T) {
	c := NewClient(Config{Route: RouteChatGPT})
	req, _ := http.NewRequest("POST", "http://example.invalid", nil)
	err := c.authorize(context.Background(), req)
	if llm.ClassOf(err) != llm.ErrCredential || req.Header.Get("Authorization") != "" {
		t.Fatalf("error=%v authorization=%q", err, req.Header.Get("Authorization"))
	}
}

func TestAuthRecoveryHTTPStatusAndIdentity(t *testing.T) {
	source := chatgptauth.NewSource(secrets.NewMemory(), "work-chatgpt", chatgptauth.Flow{})
	for _, tc := range []struct {
		name   string
		tokens TokenSource
		status int
		want   llm.ErrorClass
	}{
		{"subscription rejected", source, 401, llm.ErrLoginRequired},
		{"subscription forbidden", source, 403, llm.ErrPermission},
		{"API key rejected", nil, 401, llm.ErrAuth},
		{"API key forbidden", nil, 403, llm.ErrPermission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{tokens: tc.tokens}
			err := c.normalizeHTTP(&http.Response{StatusCode: tc.status, Header: http.Header{}}, []byte(`{"error":{"message":"sensitive-diagnostic"}}`))
			if llm.ClassOf(err) != tc.want {
				t.Fatalf("error=%v", err)
			}
			var credential *llm.CredentialError
			if tc.want == llm.ErrLoginRequired {
				if !errors.As(err, &credential) || credential.Profile != "work-chatgpt" || credential.Method != llm.AuthSubscription || strings.Contains(err.Error(), "sensitive-diagnostic") {
					t.Fatalf("incorrect recovery metadata: %v", err)
				}
			} else if errors.As(err, &credential) {
				t.Fatal("permission/API key failure offered subscription recovery")
			}
		})
	}
}

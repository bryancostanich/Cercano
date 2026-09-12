package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/chatgptauth"
	"cercano/source/server/internal/cloudfactory"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/secrets"
	pkgcfg "cercano/source/server/pkg/config"
)

func TestWorkerRefreshAndHostLoginShareCredentialOwner(t *testing.T) {
	for _, profile := range []pkgcfg.CloudProfile{
		{Name: "named", Flavor: cloudfactory.FlavorMessages, Route: cloudfactory.RouteSubscription},
		{Name: "named", Flavor: cloudfactory.FlavorResponses, Route: cloudfactory.RouteChatGPT},
	} {
		t.Run(profile.Route, func(t *testing.T) {
			cfg := pkgcfg.Defaults()
			cfg.CloudProfiles = []pkgcfg.CloudProfile{profile}
			cfg.ActiveCloudProfile = profile.Name
			owner := cfgsvc.New("", cfg, secrets.NewMemory())
			store := owner.Secrets()
			if profile.Route == cloudfactory.RouteSubscription {
				if err := anthropicauth.Save(store, "named", anthropicauth.TokenSet{Access: "expired", Refresh: "old", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := chatgptauth.Save(store, "named", chatgptauth.TokenSet{Access: "expired", Refresh: "old", AccountID: "old-account", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			entered := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"access_token":"stale","refresh_token":"stale-refresh","expires_in":3600}`))
			}))
			defer srv.Close()
			defer unblock()
			worker := &workerRunner{cfg: owner, anthFlow: anthropicauth.Flow{TokenURL: srv.URL}, chatFlow: chatgptauth.Flow{Issuer: srv.URL}}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			type result struct {
				access, account string
				err             error
			}
			done := make(chan result, 1)
			go func() { a, b, e := worker.resolveCredential(ctx, cfg, "named"); done <- result{a, b, e} }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("worker refresh did not start")
			}
			// This is the login handlers' actual write path, not a private worker cache.
			if profile.Route == cloudfactory.RouteSubscription {
				if err := anthropicauth.Save(owner.Secrets(), "named", anthropicauth.TokenSet{Access: "new-login", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := chatgptauth.Save(owner.Secrets(), "named", chatgptauth.TokenSet{Access: "new-login", AccountID: "new-account", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case got := <-done:
				if got.err != nil || got.access != "new-login" {
					t.Fatalf("worker used old owner: %+v", got)
				}
				if profile.Route == cloudfactory.RouteChatGPT && got.account != "new-account" {
					t.Fatalf("old account leaked: %q", got.account)
				}
			case <-ctx.Done():
				t.Fatal("worker waited for obsolete network refresh")
			}
			unblock()
			// A rebuilt host view sees exactly the same credential generation.
			if profile.Route == cloudfactory.RouteSubscription {
				if a, e := owner.Credentials().Anthropic("named", anthropicauth.Flow{}).Token(ctx); e != nil || a != "new-login" {
					t.Fatalf("host view mismatch: %q %v", a, e)
				}
			} else {
				if a, b, e := owner.Credentials().ChatGPT("named", chatgptauth.Flow{}).Token(ctx); e != nil || a != "new-login" || b != "new-account" {
					t.Fatalf("host view mismatch: %q %q %v", a, b, e)
				}
			}
		})
	}
}

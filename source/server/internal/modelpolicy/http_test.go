package modelpolicy_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cercano/source/server/internal/modelpolicy"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

func TestIdentityAndLiveRestrictions(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(200) }))
	defer server.Close()
	route := v1.Route{Provider: "openai", Endpoint: server.URL + "/v1", Model: "approved", Placement: "external"}
	policy := v1.Policy{AllowedRoutes: []v1.Route{route}}
	ctx := modelpolicy.WithAuthority(context.Background(), modelpolicy.AuthorizeFunc(func(ctx context.Context, a modelpolicy.Attempt) error {
		if !modelpolicy.Allowed(policy, a) {
			return modelpolicy.Deny(a, "not approved")
		}
		return nil
	}))
	send := func(endpoint, provider, placement, model string) error {
		r, _ := http.NewRequestWithContext(ctx, "POST", endpoint+"/chat/completions", strings.NewReader(`{"model":"`+model+`","messages":[]}`))
		response, err := modelpolicy.Client(server.Client(), provider, placement, modelpolicy.OpenAI).Do(r)
		if response != nil {
			response.Body.Close()
		}
		return err
	}
	if err := send(route.Endpoint, route.Provider, route.Placement, route.Model); err != nil {
		t.Fatal(err)
	}
	for _, r := range []v1.Route{
		{Provider: "anthropic", Endpoint: route.Endpoint, Model: route.Model, Placement: route.Placement},
		{Provider: route.Provider, Endpoint: server.URL + "/other", Model: route.Model, Placement: route.Placement},
		{Provider: route.Provider, Endpoint: route.Endpoint, Model: "forbidden", Placement: route.Placement},
		{Provider: route.Provider, Endpoint: route.Endpoint, Model: route.Model, Placement: "local"},
	} {
		if err := send(r.Endpoint, r.Provider, r.Placement, r.Model); err == nil {
			t.Fatal("accepted another physical route")
		}
	}
	policy.AllowedRoutes = nil
	if err := send(route.Endpoint, route.Provider, route.Placement, route.Model); err == nil {
		t.Fatal("accepted revoked route")
	}
	if calls != 1 {
		t.Fatalf("provider received %d calls", calls)
	}
}

func TestManagedRedirectNeverReachesAnotherEndpoint(t *testing.T) {
	hits := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer other.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/v1/chat/completions", 307)
	}))
	defer origin.Close()
	allow := modelpolicy.AuthorizeFunc(func(context.Context, modelpolicy.Attempt) error { return nil })
	ctx := modelpolicy.WithAuthority(context.Background(), allow)
	r, _ := http.NewRequestWithContext(ctx, "POST", origin.URL+"/v1/chat/completions", strings.NewReader(`{"model":"approved"}`))
	_, err := modelpolicy.Client(nil, "openai", "external", modelpolicy.OpenAI).Do(r)
	var denied *modelpolicy.Denial
	if !errors.As(err, &denied) || hits != 0 {
		t.Fatalf("redirect escaped: error=%v hits=%d", err, hits)
	}
}

func TestProcessAuthorityCannotBeOverriddenOrDropped(t *testing.T) {
	cleanup := modelpolicy.Install(modelpolicy.AuthorizeFunc(func(context.Context, modelpolicy.Attempt) error {
		return modelpolicy.Deny(modelpolicy.Attempt{}, "denied")
	}))
	defer cleanup()
	ctx := modelpolicy.WithAuthority(context.Background(), modelpolicy.AuthorizeFunc(func(context.Context, modelpolicy.Attempt) error { return nil }))
	if modelpolicy.Check(ctx, modelpolicy.Attempt{}) == nil || modelpolicy.Check(context.Background(), modelpolicy.Attempt{}) == nil {
		t.Fatal("lost process boundary")
	}
}

func TestBodyIsUnchangedAndStandaloneDoesNotRequireModel(t *testing.T) {
	want := []byte(`{"model":"approved","messages":[{"content":"private code"}]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if !bytes.Equal(got, want) {
			t.Error("request changed")
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	client := modelpolicy.Client(server.Client(), "openai", "external", modelpolicy.OpenAI)
	for _, managed := range []bool{true, false} {
		ctx := context.Background()
		if managed {
			ctx = modelpolicy.WithAuthority(ctx, modelpolicy.AuthorizeFunc(func(_ context.Context, a modelpolicy.Attempt) error {
				if a.Model != "approved" {
					t.Error("wrong model")
				}
				return nil
			}))
		}
		req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/chat/completions", bytes.NewReader(want))
		if !managed {
			req.GetBody = nil
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
}

func TestLocalAliasResolvesAgainAfterModelChange(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.WriteHeader(200) }))
	defer server.Close()
	actual := "catalog:approved"
	authority := modelpolicy.AuthorizeFunc(func(_ context.Context, a modelpolicy.Attempt) error {
		if a.Model != "catalog:approved" {
			return modelpolicy.Deny(a, "loaded model is not approved")
		}
		return nil
	})
	client := modelpolicy.Client(server.Client(), "mistralrs", "local", modelpolicy.OpenAI, func(context.Context, string, string) (string, error) { return actual, nil })
	send := func() error {
		req, _ := http.NewRequestWithContext(modelpolicy.WithAuthority(context.Background(), authority), "POST", server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"default"}`))
		r, err := client.Do(req)
		if r != nil {
			r.Body.Close()
		}
		return err
	}
	if err := send(); err != nil {
		t.Fatal(err)
	}
	actual = "catalog:forbidden"
	if err := send(); err == nil || hits != 1 {
		t.Fatal("wire alias authorized a changed physical model")
	}
}

func TestManagedWorkerCannotLoseItsHostProxy(t *testing.T) {
	cleanup := modelpolicy.InstallWorkerGuard()
	defer cleanup()
	ctx := modelpolicy.WithAuthority(context.Background(), modelpolicy.AuthorizeFunc(func(context.Context, modelpolicy.Attempt) error { return nil }))
	if err := modelpolicy.Check(ctx, modelpolicy.Attempt{}); err != nil {
		t.Fatal(err)
	}
	if !modelpolicy.Managed(context.Background()) || modelpolicy.Check(context.Background(), modelpolicy.Attempt{}) == nil {
		t.Fatal("worker lost policy boundary when context was dropped")
	}
}

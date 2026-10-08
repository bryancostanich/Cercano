package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// cerebrasServer returns a test double standing in for
// https://api.cerebras.ai/v1 plus a profile pointed at it. The Provider field
// is set explicitly; the hostname path is covered by config/cloudfactory
// classification tests because httptest cannot control its own hostname.
func cerebrasServer(t *testing.T, cfg *Server, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	fixture := httptest.NewServer(handler)
	t.Cleanup(fixture.Close)
	cfg.cfgSvc.Set(config.Config{
		ActiveCloudProfile: "cerebras",
		CloudProfiles: []config.CloudProfile{{
			Name:     "cerebras",
			Provider: "cerebras",
			Flavor:   "chat_completions",
			BaseURL:  fixture.URL + "/v1",
		}},
	})
	return fixture
}

func TestListCloudProfileModels_CerebrasParsesOpenAIShape(t *testing.T) {
	s, _ := newTestServer()
	if err := s.cfgSvc.Secrets().Set("cerebras", "csk-test"); err != nil {
		t.Fatal(err)
	}
	var sawAuth, sawPath string
	cerebrasServer(t, s, func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"llama-3.3-70b"},{"id":"qwen-3-32b"},{"id":"gpt-oss-120b"}]}`))
	})
	resp, err := s.ListCloudProfileModels(context.Background(), &proto.ListCloudProfileModelsRequest{})
	if err != nil {
		t.Fatalf("ListCloudProfileModels: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if sawPath != "/v1/models" {
		t.Errorf("catalog path: got %q want /v1/models", sawPath)
	}
	if sawAuth != "Bearer csk-test" {
		t.Errorf("catalog auth: got %q want Bearer from keychain", sawAuth)
	}
	if len(resp.Models) != 3 {
		t.Fatalf("got %d models, want 3: %+v", len(resp.Models), resp.Models)
	}
	// Only what the endpoint returned — display names echo the ids and no
	// capability or pricing metadata is invented.
	for i, m := range resp.Models {
		if m.DisplayName != m.Id {
			t.Errorf("model %d: display name %q does not echo id %q", i, m.DisplayName, m.Id)
		}
	}
}

func TestListCloudProfileModels_CerebrasMissingKeyReturnsEndpointError(t *testing.T) {
	s, _ := newTestServer()
	var sawAuth atomic.Value
	cerebrasServer(t, s, func(w http.ResponseWriter, r *http.Request) {
		sawAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid api key"}`))
	})
	resp, err := s.ListCloudProfileModels(context.Background(), &proto.ListCloudProfileModelsRequest{})
	if err != nil {
		t.Fatalf("ListCloudProfileModels: %v", err)
	}
	if resp.Error == "" {
		t.Error("unauthorized endpoint must surface an error, not an empty catalog")
	}
	if got, _ := sawAuth.Load().(string); got != "" {
		t.Errorf("no key stored: got Authorization %q, want absent", got)
	}
}

// The bearer token must never follow a redirect: a 302 pointing at another
// host is refused outright and the redirect target must never be contacted.
func TestListCloudProfileModels_CerebrasRefusesRedirect(t *testing.T) {
	s, _ := newTestServer()
	if err := s.cfgSvc.Secrets().Set("cerebras", "csk-test"); err != nil {
		t.Fatal(err)
	}
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		if r.Header.Get("Authorization") != "" {
			t.Errorf("bearer token leaked to redirect target")
		}
	}))
	t.Cleanup(target.Close)
	cerebrasServer(t, s, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/steal", http.StatusFound)
	})
	resp, err := s.ListCloudProfileModels(context.Background(), &proto.ListCloudProfileModelsRequest{})
	if err != nil {
		t.Fatalf("ListCloudProfileModels: %v", err)
	}
	if resp.Error == "" {
		t.Error("redirected catalog request must surface an error")
	}
	if atomic.LoadInt32(&targetHits) != 0 {
		t.Errorf("redirect target was contacted %d times", targetHits)
	}
}

func TestListCloudProfileModels_CerebrasBoundedBodyAndCount(t *testing.T) {
	s, _ := newTestServer()
	payload := `{"data":[`
	for i := 0; i < 600; i++ {
		if i > 0 {
			payload += ","
		}
		payload += `{"id":"m-` + strconv.Itoa(i) + `"}`
	}
	payload += `]}`
	cerebrasServer(t, s, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	})
	resp, err := s.ListCloudProfileModels(context.Background(), &proto.ListCloudProfileModelsRequest{})
	if err != nil {
		t.Fatalf("ListCloudProfileModels: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if len(resp.Models) != 500 {
		t.Errorf("got %d models, want the 500-model cap", len(resp.Models))
	}
}

// A cerebras-hosted profile on the messages flavor must keep the standard
// flavor-gated error instead of being parsed as an OpenAI catalog.
func TestListCloudProfileModels_CerebrasWrongFlavorStillErrors(t *testing.T) {
	s, _ := newTestServer()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("catalog fetch must not run for a non-chat_completions flavor")
	}))
	t.Cleanup(fixture.Close)
	s.cfgSvc.Set(config.Config{
		CloudProfiles: []config.CloudProfile{{
			Name:     "cerebras",
			Provider: "cerebras",
			Flavor:   "messages",
			BaseURL:  fixture.URL + "/v1",
		}},
	})
	resp, err := s.ListCloudProfileModels(context.Background(), &proto.ListCloudProfileModelsRequest{})
	if err != nil {
		t.Fatalf("ListCloudProfileModels: %v", err)
	}
	if resp.Error == "" {
		t.Error("messages-flavor cerebras profile must surface the flavor error")
	}
}

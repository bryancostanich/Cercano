package server

import (
	"cercano/source/server/internal/hostsvc/credentials"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/anthropicauth"
	"cercano/source/server/internal/chatgptauth"
	"cercano/source/server/internal/cloudfactory"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type cloudLoginTestStream struct {
	proto.Agent_ReauthenticateCloudServer
	ctx          context.Context
	events       []*proto.CloudLoginEvent
	callbackDone chan error
}

func (s *cloudLoginTestStream) Context() context.Context { return s.ctx }
func (s *cloudLoginTestStream) Send(e *proto.CloudLoginEvent) error {
	s.events = append(s.events, e)
	if e.AuthorizeUrl != "" {
		auth, err := url.Parse(e.AuthorizeUrl)
		if err != nil {
			return err
		}
		redirect, err := url.Parse(auth.Query().Get("redirect_uri"))
		if err != nil {
			return err
		}
		if redirect.Hostname() != "127.0.0.1" && redirect.Hostname() != "localhost" {
			return errors.New("test refuses external callback")
		}
		q := redirect.Query()
		q.Set("code", "synthetic-code")
		q.Set("state", auth.Query().Get("state"))
		redirect.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(s.ctx, "GET", redirect.String(), nil)
		if err != nil {
			return err
		}
		s.callbackDone = make(chan error, 1)
		go func() {
			response, err := (&http.Client{Timeout: time.Second}).Do(req)
			if err == nil {
				response.Body.Close()
			}
			s.callbackDone <- err
		}()

	}
	return nil
}
func TestReauthenticationRunsBothFlowsWithoutConfigChanges(t *testing.T) {
	for _, p := range []config.CloudProfile{
		{Name: "named", Flavor: cloudfactory.FlavorMessages, Route: cloudfactory.RouteSubscription, Model: "pinned", ModelPinned: true, BaseURL: "https://proxy.invalid"},
		{Name: "named", Flavor: cloudfactory.FlavorResponses, Route: cloudfactory.RouteChatGPT, Model: "pinned", ModelPinned: true, BaseURL: "https://proxy.invalid"},
	} {
		t.Run(p.Route, func(t *testing.T) {
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/accounts/deviceauth/usercode":
					w.Write([]byte(`{"device_auth_id":"device","user_code":"user","interval":"1"}`))
				case "/api/accounts/deviceauth/token":
					w.Write([]byte(`{"authorization_code":"code","code_verifier":"verifier"}`))
				default:
					w.Write([]byte(`{"access_token":"synthetic-access","refresh_token":"synthetic-refresh","expires_in":3600}`))
				}
			}))
			defer endpoint.Close()
			cfg := config.Defaults()
			cfg.CloudProfiles = []config.CloudProfile{p, {Name: "other"}}
			cfg.ActiveCloudProfile = "other"
			cfg.BackupCloudProfile = "named"
			server := &Server{cfgSvc: cfgsvc.New("", cfg, secrets.NewMemory())}
			before := server.cfgSvc.Get()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			stream := &cloudLoginTestStream{ctx: ctx}
			err := server.runCloudReauthentication(&proto.CloudReauthenticationRequest{ProfileName: "named", AttemptId: "attempt-123"}, stream, anthropicauth.Flow{AuthorizeURL: endpoint.URL + "/authorize", TokenURL: endpoint.URL + "/token"}, chatgptauth.Flow{Issuer: endpoint.URL})
			if err != nil {
				t.Fatal(err)
			}
			if stream.callbackDone != nil {
				if err := <-stream.callbackDone; err != nil {
					t.Fatal(err)
				}
			}
			if len(stream.events) != 2 || !stream.events[1].Done || !stream.events[1].Ok {
				t.Fatalf("events=%+v", stream.events)
			}
			for _, event := range stream.events {
				if event.AttemptId != "attempt-123" || event.ProfileName != "named" {
					t.Fatalf("lost identity: %+v", event)
				}
			}
			if !reflect.DeepEqual(before, server.cfgSvc.Get()) {
				t.Fatal("reauthentication changed config")
			}
			raw, err := server.cfgSvc.Secrets().Get("named")
			if err != nil || !strings.Contains(raw, "synthetic-access") {
				t.Fatalf("credential save failed: %v", err)
			}
		})
	}
}
func TestReauthenticationRejectsInvalidProfilesBeforeLogin(t *testing.T) {
	cfg := config.Defaults()
	cfg.CloudProfiles = []config.CloudProfile{{Name: "api", Flavor: cloudfactory.FlavorMessages, Route: "api"}}
	server := &Server{cfgSvc: cfgsvc.New("", cfg, secrets.NewMemory())}
	for _, tc := range []struct {
		profile, id string
		code        codes.Code
	}{{"", "id", codes.InvalidArgument}, {"api", "", codes.InvalidArgument}, {"missing", "id", codes.NotFound}, {"api", "id", codes.FailedPrecondition}} {
		stream := &cloudLoginTestStream{ctx: context.Background()}
		err := server.ReauthenticateCloud(&proto.CloudReauthenticationRequest{ProfileName: tc.profile, AttemptId: tc.id}, stream)
		if status.Code(err) != tc.code || len(stream.events) != 0 {
			t.Fatalf("%s: %v", tc.profile, err)
		}
	}
}
func TestLoginFailureNeverEmitsUnstructuredSecrets(t *testing.T) {
	for _, err := range []error{errors.New("refresh_token=secret"), &url.Error{Op: "Post", URL: "https://endpoint.invalid?code=secret", Err: errors.New("secret")}, &llm.CredentialError{Provider: "anthropic", Class: llm.ErrCredential, Reason: llm.CredentialStore, Cause: errors.New("secret")}, &llm.TokenEndpointError{StatusCode: 400, Code: "invalid_grant"}} {
		if got := loginFailure(err); strings.Contains(got, "secret") {
			t.Fatalf("unsafe login error: %s", got)
		}
	}
}

type supersedingLoginStream struct {
	proto.Agent_ReauthenticateCloudServer
	server *Server
	events []*proto.CloudLoginEvent
	newer  *credentials.LoginAttempt
}

func (s *supersedingLoginStream) Context() context.Context { return context.Background() }
func (s *supersedingLoginStream) Send(e *proto.CloudLoginEvent) error {
	s.events = append(s.events, e)
	if e.VerificationUrl != "" {
		var err error
		s.newer, err = s.server.cfgSvc.Credentials().BeginLogin(context.Background(), e.ProfileName, "openai-responses")
		return err
	}
	return nil
}
func TestSupersededDeviceLoginStopsBeforePollingOrSaving(t *testing.T) {
	var polls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/accounts/deviceauth/usercode" {
			w.Write([]byte(`{"device_auth_id":"device","user_code":"user","interval":"1"}`))
			return
		}
		polls.Add(1)
		w.WriteHeader(403)
	}))
	defer endpoint.Close()
	cfg := config.Defaults()
	cfg.CloudProfiles = []config.CloudProfile{{Name: "work", Flavor: cloudfactory.FlavorResponses, Route: cloudfactory.RouteChatGPT}}
	server := &Server{cfgSvc: cfgsvc.New("", cfg, secrets.NewMemory())}
	server.cfgSvc.Secrets().Set("work", "original")
	stream := &supersedingLoginStream{server: server}
	err := server.runCloudReauthentication(&proto.CloudReauthenticationRequest{ProfileName: "work", AttemptId: "old"}, stream, anthropicauth.Flow{}, chatgptauth.Flow{Issuer: endpoint.URL})
	if stream.newer != nil {
		defer stream.newer.Close()
	}
	if err != nil || len(stream.events) != 2 || !stream.events[1].Done || stream.events[1].Ok || polls.Load() != 0 {
		t.Fatalf("superseded login continued: err=%v events=%+v polls=%d", err, stream.events, polls.Load())
	}
	if stored, _ := server.cfgSvc.Secrets().Get("work"); stored != "original" {
		t.Fatal("superseded login saved credentials")
	}
}

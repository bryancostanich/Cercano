package server

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

type failedClaudeLoginStream struct {
	proto.Agent_StartClaudeLoginServer
	address string
	failure error
}

func (s *failedClaudeLoginStream) Context() context.Context { return context.Background() }
func (s *failedClaudeLoginStream) Send(event *proto.StartClaudeLoginEvent) error {
	authorize, err := url.Parse(event.GetAuthorizeUrl())
	if err != nil {
		return err
	}
	redirect, err := url.Parse(authorize.Query().Get("redirect_uri"))
	if err != nil {
		return err
	}
	s.address = redirect.Host
	return s.failure
}
func TestClaudeLoginSendFailureClosesLoopback(t *testing.T) {
	sentinel := errors.New("client disconnected")
	stream := &failedClaudeLoginStream{failure: sentinel}
	server := &Server{cfgSvc: cfgsvc.New("", config.Defaults(), secrets.NewMemory())}
	if err := server.StartClaudeLogin(&proto.StartClaudeLoginRequest{}, stream); !errors.Is(err, sentinel) {
		t.Fatalf("handler error=%v", err)
	}
	if stream.address == "" {
		t.Fatal("no loopback address captured")
	}
	conn, err := net.DialTimeout("tcp", stream.address, time.Second)
	if err == nil {
		conn.Close()
		t.Fatal("loopback listener still accepts connections after failed stream send")
	}
}

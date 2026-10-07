package main

import (
	"context"
	"os"
	"testing"

	"cercano/source/server/internal/server"
	"cercano/source/server/pkg/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// No server startup, real models, network, config or self-signals in these tests.
func TestNilStopRequestOwnerRefusesSafeStop(t *testing.T) {
	s := &server.Server{}
	configureProcessStopRequester(s, nil)
	result, err := s.ShutdownAgentWhenIdle(context.Background(), &proto.ShutdownAgentWhenIdleRequest{ExpectedPid: int64(os.Getpid())})
	if status.Code(err) != codes.FailedPrecondition || result != nil {
		t.Fatalf("unowned stop falsely accepted: %+v %v", result, err)
	}
}
func TestOwnedStopRequestIsConsumedOnce(t *testing.T) {
	s := &server.Server{}
	owner := newProcessStopRequest()
	configureProcessStopRequester(s, owner)
	req := &proto.ShutdownAgentWhenIdleRequest{ExpectedPid: int64(os.Getpid())}
	for i := 0; i < 2; i++ {
		r, e := s.ShutdownAgentWhenIdle(context.Background(), req)
		if e != nil || !r.Accepted {
			t.Fatalf("owned request: %+v %v", r, e)
		}
	}
	select {
	case <-owner.wait():
	default:
		t.Fatal("owner received no stop request")
	}
	select {
	case <-owner.wait():
		t.Fatal("repeat request duplicated stop")
	default:
	}
}

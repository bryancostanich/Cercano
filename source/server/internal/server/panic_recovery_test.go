package server

// Tests for the C1 regression fix: panic isolation in the RunTurn goroutine.
//
// A panic inside RunTurn previously crashed the whole process (no recovery on
// the child goroutine). The fix wraps the goroutine body in a recover that logs
// the stack and writes codes.Internal to doneCh so the caller returns cleanly.
//
// This file verifies:
//   1. A panicking TurnRunner returns codes.Internal to the caller of
//      streamProcessRequestWithToolLoop — the server survives.
//   2. HasActiveTurn reports false after the handler returns (no broker leak).

import (
	"context"
	"testing"

	"cercano/source/server/internal/broker"
	runnersvc "cercano/source/server/internal/runner"
	"cercano/source/server/pkg/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// panicTurnRunner is a fake TurnRunner that panics unconditionally.
type panicTurnRunner struct{}

func (p panicTurnRunner) RunTurn(
	_ context.Context,
	_ runnersvc.Request,
	_ runnersvc.EventSink,
	_ runnersvc.PermissionRequester,
	_ runnersvc.PersistFunc,
) (runnersvc.Result, error) {
	panic("synthetic test panic from RunTurn")
}

// discardStream is a minimal implementation of
// grpc.ServerStreamingServer[proto.StreamProcessResponse].
// Send is a no-op; Context returns a background context.
type discardStream struct {
	ctx context.Context
}

func (d *discardStream) Send(*proto.StreamProcessResponse) error   { return nil }
func (d *discardStream) Context() context.Context                  { return d.ctx }
func (d *discardStream) SetHeader(metadata.MD) error               { return nil }
func (d *discardStream) SendHeader(metadata.MD) error              { return nil }
func (d *discardStream) SetTrailer(metadata.MD)                    {}
func (d *discardStream) SendMsg(m any) error                       { return nil }
func (d *discardStream) RecvMsg(m any) error                       { return nil }

// Compile-time check: discardStream satisfies grpc.ServerStream.
var _ grpc.ServerStream = (*discardStream)(nil)

// TestRunTurnPanic_ReturnsCodesInternal verifies that a panic inside RunTurn
// does NOT crash the server process and returns a gRPC codes.Internal error to
// the caller of streamProcessRequestWithToolLoop.
func TestRunTurnPanic_ReturnsCodesInternal(t *testing.T) {
	b := broker.New()
	srv := &Server{
		turnBroker: b,
		turnRunner: panicTurnRunner{},
		// permBroker is nil — requester closure checks permBroker != nil before use.
	}

	stream := &discardStream{ctx: context.Background()}
	req := &proto.ProcessRequestRequest{
		ConversationId: "conv-panic-test",
		Input:          "hello",
	}

	err := srv.streamProcessRequestWithToolLoop(req, stream)
	if err == nil {
		t.Fatal("expected an error from a panicking runner, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error is not a gRPC status: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("got code %v, want codes.Internal", st.Code())
	}
}

// TestRunTurnPanic_NoTurnLeak verifies that after a panicking RunTurn returns,
// the broker no longer shows an active turn for the conversation (the defer
// releaseTurn() ran — no resource leak).
func TestRunTurnPanic_NoTurnLeak(t *testing.T) {
	b := broker.New()
	srv := &Server{
		turnBroker: b,
		turnRunner: panicTurnRunner{},
	}
	const convID = "conv-panic-leak"

	stream := &discardStream{ctx: context.Background()}
	req := &proto.ProcessRequestRequest{
		ConversationId: convID,
		Input:          "hello",
	}

	_ = srv.streamProcessRequestWithToolLoop(req, stream)

	if b.HasActiveTurn(convID) {
		t.Error("HasActiveTurn = true after panicking handler returned — turn handle leaked")
	}
}

package server

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A panic in a unary handler must be converted into a codes.Internal error and
// must NOT propagate — otherwise a single bad RPC crashes the whole singleton
// agent and every connected client's stream dies with "Unavailable ... EOF".
func TestRecoveryUnaryInterceptor_PanicBecomesInternal(t *testing.T) {
	interceptor := RecoveryUnaryInterceptor()
	handler := func(ctx context.Context, req any) (any, error) {
		panic("send on closed channel")
	}

	resp, err := interceptor(
		context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/cercano.Agent/Test"},
		handler,
	)

	if resp != nil {
		t.Fatalf("expected nil response after panic, got %v", resp)
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected a gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != codes.Internal {
		t.Fatalf("expected codes.Internal, got %v", st.Code())
	}
}

// Same guarantee for streaming handlers — this is the path StreamProcessRequest
// (the chat stream) runs on, where the telemetry send-on-closed panic originated.
func TestRecoveryStreamInterceptor_PanicBecomesInternal(t *testing.T) {
	interceptor := RecoveryStreamInterceptor()
	handler := func(srv any, ss grpc.ServerStream) error {
		panic("send on closed channel")
	}

	err := interceptor(
		nil, nil,
		&grpc.StreamServerInfo{FullMethod: "/cercano.Agent/StreamProcessRequest"},
		handler,
	)

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected a gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != codes.Internal {
		t.Fatalf("expected codes.Internal, got %v", st.Code())
	}
}

// A handler that returns normally must pass through untouched — the interceptor
// only intervenes on panic.
func TestRecoveryUnaryInterceptor_PassesThroughNormalReturn(t *testing.T) {
	interceptor := RecoveryUnaryInterceptor()
	want := "ok"
	handler := func(ctx context.Context, req any) (any, error) {
		return want, nil
	}

	resp, err := interceptor(
		context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/cercano.Agent/Test"},
		handler,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != want {
		t.Fatalf("expected %q passed through, got %v", want, resp)
	}
}

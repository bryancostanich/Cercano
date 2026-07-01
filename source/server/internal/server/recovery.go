package server

import (
	"context"
	"log"
	"runtime/debug"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The agent is a singleton shared by every client (CLI, VS Code, headless run).
// grpc-go does not recover handler panics, so without these interceptors a single
// panic — e.g. a send-on-closed-channel during a shutdown-during-stream race —
// tears down the whole process and every open stream reports "Unavailable ... EOF".
// The interceptors turn a panic into a codes.Internal error for that one RPC,
// keeping the server (and all other clients' streams) alive.

// RecoveryUnaryInterceptor returns a grpc.UnaryServerInterceptor that recovers
// panics in unary handlers, logs the stack, and returns codes.Internal.
func RecoveryUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic recovered in %s: %v\n%s", info.FullMethod, r, debug.Stack())
				resp = nil
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

// RecoveryStreamInterceptor returns a grpc.StreamServerInterceptor that recovers
// panics in streaming handlers, logs the stack, and returns codes.Internal.
func RecoveryStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic recovered in %s: %v\n%s", info.FullMethod, r, debug.Stack())
				err = status.Errorf(codes.Internal, "internal server error")
			}
		}()
		return handler(srv, ss)
	}
}

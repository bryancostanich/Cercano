package server

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

func TestIsTurnCancellationRecognizesWrappedContextCanceled(t *testing.T) {
	err := fmt.Errorf("tool loop error: Post %q: %w", "https://chatgpt.com/backend-api/codex/responses", context.Canceled)
	if !isTurnCancellation(err) {
		t.Fatalf("wrapped context.Canceled should be treated as graceful turn cancellation: %v", err)
	}
}

func TestIsTurnCancellationRecognizesStringifiedWorkerCancellation(t *testing.T) {
	err := fmt.Errorf("tool loop error: Post %q: context canceled", "https://chatgpt.com/backend-api/codex/responses")
	if !isTurnCancellation(err) {
		t.Fatalf("stringified worker cancellation should be graceful: %v", err)
	}
}

func TestIsTurnCancellationRecognizesGRPCCanceled(t *testing.T) {
	err := grpcstatus.Error(codes.Canceled, "tool loop error: context canceled")
	if !isTurnCancellation(err) {
		t.Fatalf("gRPC canceled should be treated as graceful turn cancellation: %v", err)
	}
}

func TestIsTurnCancellationDoesNotHideProviderErrors(t *testing.T) {
	err := fmt.Errorf("tool loop error: provider unavailable")
	if isTurnCancellation(err) {
		t.Fatalf("non-cancellation provider error must still surface: %v", err)
	}
}

package server

import (
	"context"
	"os"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Pin the state visible to a competing RPC between idle sealing and safe-stop
// commit. A retryable refusal here is distinct from a repeat after commitment.
func TestSafeStopPrecommitSealIsRetryableWithoutCallback(t *testing.T) {
	f := startSafeStopServer(t, nil)
	release, err := f.srv.waitForUpdateIdle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if f.srv.safeStopAlreadyCommitted() {
		t.Fatal("fixture already committed")
	}
	req := safeStopReq(int64(os.Getpid()))
	resp, err := f.client.ShutdownAgentWhenIdle(context.Background(), req)
	if status.Code(err) != codes.Aborted || resp != nil {
		t.Fatalf("precommit response: %+v %v", resp, err)
	}
	if f.stop.invocations() != 0 || f.srv.safeStopAlreadyCommitted() {
		t.Fatal("refused request caused shutdown")
	}
	release()
	resp, err = f.client.ShutdownAgentWhenIdle(context.Background(), req)
	if err != nil || !resp.GetAccepted() {
		t.Fatalf("retry: %+v %v", resp, err)
	}
	resp, err = f.client.ShutdownAgentWhenIdle(context.Background(), req)
	if err != nil || !resp.GetAccepted() || f.stop.invocations() != 1 {
		t.Fatalf("committed repeat: %+v %v callbacks=%d", resp, err, f.stop.invocations())
	}
}

package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/procx"
)

// A clean run reports ok.
func TestRunValidator_Success(t *testing.T) {
	out, ok, err := runValidator(context.Background(), 10*time.Second, t.TempDir(), "echo", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("ok should be true for exit 0")
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("out = %q, want it to contain %q", out, "hello")
	}
}

// A non-zero exit is an ordinary validation failure, NOT a runner error: err
// stays nil so callers can tell "your build is broken" from "the gate broke".
func TestRunValidator_NonZeroExitIsNotRunnerError(t *testing.T) {
	out, ok, err := runValidator(context.Background(), 10*time.Second, t.TempDir(),
		"sh", "-c", "echo boom 1>&2; exit 1")
	if err != nil {
		t.Fatalf("non-zero exit should not be a runner error, got: %v", err)
	}
	if ok {
		t.Error("ok should be false for a non-zero exit")
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("out = %q, want it to contain stderr", out)
	}
}

// The point of the whole exercise: a wedged gate is bounded, and its partial
// output survives so the failure is diagnosable.
func TestRunValidator_TimeoutIsEnforced(t *testing.T) {
	start := time.Now()
	out, ok, err := runValidator(context.Background(), 1*time.Second, t.TempDir(),
		"sh", "-c", "echo starting; sleep 30")
	elapsed := time.Since(start)

	if !errors.Is(err, procx.ErrTimeout) {
		t.Fatalf("expected procx.ErrTimeout, got: %v", err)
	}
	if ok {
		t.Error("ok must be false on timeout")
	}
	if elapsed > 15*time.Second {
		t.Fatalf("timeout not enforced: returned after %s", elapsed.Round(time.Millisecond))
	}
	if !strings.Contains(out, "starting") {
		t.Errorf("partial output lost on timeout: %q", out)
	}
}

// A validator must not outlive a cancelled turn.
func TestRunValidator_HonoursCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, ok, err := runValidator(ctx, 5*time.Minute, t.TempDir(), "sh", "-c", "sleep 300")
	elapsed := time.Since(start)

	if !errors.Is(err, procx.ErrCanceled) {
		t.Fatalf("expected procx.ErrCanceled, got: %v", err)
	}
	if ok {
		t.Error("ok must be false on cancel")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("cancel not honoured: returned after %s", elapsed.Round(time.Millisecond))
	}
}

// The custom validator is the one that runs arbitrary user shell, so confirm
// the timeout reaches it end to end rather than only testing the helper.
func TestCustomValidator_TimesOutRatherThanHanging(t *testing.T) {
	v := NewCustomValidator("echo running; sleep 300")

	// Cancel stands in for the (much longer) production timeout, exercising
	// the same bounded-exit path without a 15-minute test.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	decision, err := v.Validate(ctx, t.TempDir())
	elapsed := time.Since(start)

	if decision != Failed {
		t.Errorf("decision = %v, want Failed", decision)
	}
	if err == nil {
		t.Fatal("expected an error")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("custom validator hung: returned after %s", elapsed.Round(time.Millisecond))
	}
}

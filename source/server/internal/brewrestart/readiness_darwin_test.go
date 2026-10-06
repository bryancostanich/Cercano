//go:build darwin && cgo

package brewrestart

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"testing"
	"time"
)

func TestReadinessRetriesTransientSocketSnapshots(t *testing.T) {
	for _, code := range []error{syscall.EBADF, syscall.EAGAIN} {
		for _, failAt := range []int{1, 2} {
			t.Run(fmt.Sprintf("%v/probe%d", code, failAt), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				calls, dials := 0, 0
				err := pollReplacementReady(ctx, func() (bool, error) {
					calls++
					if calls == failAt {
						return false, fmt.Errorf("inspect process listener: %w", code)
					}
					return true, nil
				}, func(context.Context) error { dials++; return nil })
				if err != nil {
					t.Fatalf("transient snapshot aborted readiness: %v", err)
				}
				if calls != failAt+2 || dials != failAt {
					t.Fatalf("did not repeat full ownership/connection check: probes=%d dials=%d", calls, dials)
				}
			})
		}
	}
}

func TestReadinessDoesNotRetryPermanentFailures(t *testing.T) {
	for _, code := range []error{syscall.EPERM, syscall.ESRCH, errors.New("process identity changed")} {
		for _, failAt := range []int{1, 2} {
			calls := 0
			err := pollReplacementReady(context.Background(), func() (bool, error) {
				calls++
				if calls == failAt {
					return false, code
				}
				return true, nil
			}, func(context.Context) error { return nil })
			if !errors.Is(err, code) || calls != failAt {
				t.Fatalf("permanent error not preserved: %v (%d calls)", err, calls)
			}
		}
	}
}

func TestReadinessTransientErrorsRespectDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	calls := 0
	err := pollReplacementReady(ctx, func() (bool, error) { calls++; return false, syscall.EBADF }, func(context.Context) error { t.Fatal("dialed without ownership"); return nil })
	if !errors.Is(err, context.DeadlineExceeded) || calls < 2 {
		t.Fatalf("err=%v probes=%d", err, calls)
	}
}

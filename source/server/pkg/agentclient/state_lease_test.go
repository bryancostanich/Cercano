//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package agentclient

import (
	"cercano/source/server/pkg/statelease"
	"context"
	"errors"
	"testing"
	"time"
)

func TestResetBlocksClientDialBeforeTransport(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, err := statelease.DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := statelease.TryReset(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if c, err := DialExisting(context.Background(), "invalid-address"); !errors.Is(err, statelease.ErrBusy) {
		if c != nil {
			c.Close()
		}
		t.Fatalf("dial did not honor reset: %v", err)
	}
}
func TestFailedClientDialReleasesLease(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c, err := DialExisting(ctx, "127.0.0.1:1"); err == nil {
		c.Close()
		t.Fatal("cancelled dial succeeded")
	}
	root, _ := statelease.DefaultRoot()
	l, err := statelease.TryReset(root)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
}
func TestClientCloseRetainsLeaseUntilReconnectQuiesces(t *testing.T) {
	root := t.TempDir()
	l, err := statelease.Participate(root)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{stateLease: l, stopWatch: make(chan struct{})}
	c.reconnectMu.Lock()
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-c.stopWatch:
	case <-time.After(3 * time.Second):
		t.Fatal("close did not signal watcher")
	}
	if x, err := statelease.TryReset(root); !errors.Is(err, statelease.ErrBusy) {
		if x != nil {
			x.Close()
		}
		t.Fatalf("close released while reconnect live: %v", err)
	}
	c.reconnectMu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("close did not finish")
	}
	x, err := statelease.TryReset(root)
	if err != nil {
		t.Fatal(err)
	}
	x.Close()
	c.Close()
}
func TestNeverDialedClientCannotReconnectAfterClose(t *testing.T) {
	c := &Client{stateBroker: newStateBroker()}
	c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.reconnect(ctx); !errors.Is(err, errClientClosed) {
		t.Fatalf("closed client reentered reconnect: %v", err)
	}
}

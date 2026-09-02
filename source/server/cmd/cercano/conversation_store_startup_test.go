package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
)

func TestOpenConversationStoreWithRetryRetriesSQLiteLocks(t *testing.T) {
	var attempts int
	var sleeps []time.Duration

	store, err := openConversationStoreWithRetry(
		":memory:",
		func(path string) (conversation.Store, error) {
			attempts++
			if attempts == 1 {
				return nil, errors.New("pragma setup: database is locked (261)")
			}
			return conversation.Open(path)
		},
		func(d time.Duration) {
			sleeps = append(sleeps, d)
		},
		[]time.Duration{10 * time.Millisecond},
	)
	if err != nil {
		t.Fatalf("openConversationStoreWithRetry() error = %v", err)
	}
	if store == nil {
		t.Fatal("openConversationStoreWithRetry() returned nil store")
	}
	t.Cleanup(func() { _ = store.Close() })
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if len(sleeps) != 1 || sleeps[0] != 10*time.Millisecond {
		t.Fatalf("sleeps = %v, want [10ms]", sleeps)
	}
}

func TestOpenConversationStoreWithRetryFailsFastForPermanentErrors(t *testing.T) {
	var attempts int

	_, err := openConversationStoreWithRetry(
		"/unwritable/conversations.db",
		func(string) (conversation.Store, error) {
			attempts++
			return nil, errors.New("permission denied")
		},
		func(time.Duration) {
			t.Fatal("sleep called for permanent error")
		},
		[]time.Duration{10 * time.Millisecond},
	)
	if err == nil {
		t.Fatal("openConversationStoreWithRetry() error = nil, want failure")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("error = %v, want permission detail", err)
	}
}

func TestOpenConversationStoreWithRetryStopsAfterBoundedRetries(t *testing.T) {
	var attempts int

	_, err := openConversationStoreWithRetry(
		"/locked/conversations.db",
		func(string) (conversation.Store, error) {
			attempts++
			return nil, errors.New("sqlite_busy: database is busy")
		},
		func(time.Duration) {},
		[]time.Duration{
			10 * time.Millisecond,
			20 * time.Millisecond,
		},
	)
	if err == nil {
		t.Fatal("openConversationStoreWithRetry() error = nil, want failure")
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if !strings.Contains(err.Error(), "after 3 attempts") {
		t.Fatalf("error = %v, want attempt count", err)
	}
}

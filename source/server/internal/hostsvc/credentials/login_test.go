package credentials

import (
	"context"
	"errors"
	"testing"

	"cercano/source/server/internal/secrets"
)

func TestLoginNewAttemptOwnsCommit(t *testing.T) {
	s := New(secrets.NewMemory())
	first, err := s.BeginLogin(context.Background(), "work", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.BeginLogin(context.Background(), "work", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if first.Context().Err() == nil {
		t.Fatal("superseded login was not canceled")
	}
	if err := second.Commit("new-login"); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit("old-login"); err == nil {
		t.Fatal("obsolete login committed")
	}
	first.Close() // must not undo the later result
	if raw, _ := s.Get("work"); raw != "new-login" {
		t.Fatal("older login overwrote newer credentials")
	}
	if err := second.Commit("duplicate"); err == nil {
		t.Fatal("duplicate commit accepted")
	}
}
func TestLoginCancellationAndReplacement(t *testing.T) {
	for _, operation := range []string{"cancel", "close", "write", "delete", "backend"} {
		t.Run(operation, func(t *testing.T) {
			s := New(secrets.NewMemory())
			s.Set("work", "original")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			attempt, err := s.BeginLogin(ctx, "work", "anthropic")
			if err != nil {
				t.Fatal(err)
			}
			defer attempt.Close()
			switch operation {
			case "cancel":
				cancel()
			case "close":
				attempt.Close()
			case "write":
				s.Set("work", "replacement")
			case "delete":
				s.Delete("work")
			case "backend":
				s.ReplaceStore(secrets.NewMemory())
			}
			if err := attempt.Commit("late-login"); err == nil {
				t.Fatal("obsolete login accepted")
			}
			if raw, _ := s.Get("work"); raw == "late-login" {
				t.Fatal("canceled login mutated credentials")
			}
		})
	}
}
func TestClosingOldLoginDoesNotCancelNewOne(t *testing.T) {
	s := New(secrets.NewMemory())
	old, _ := s.BeginLogin(context.Background(), "work", "anthropic")
	current, _ := s.BeginLogin(context.Background(), "work", "anthropic")
	defer current.Close()
	old.Close()
	if err := current.Commit("new"); err != nil {
		t.Fatal(err)
	}
}
func TestDifferentProfilesHaveIndependentLogins(t *testing.T) {
	s := New(secrets.NewMemory())
	a, _ := s.BeginLogin(context.Background(), "a", "anthropic")
	b, _ := s.BeginLogin(context.Background(), "b", "openai-responses")
	defer a.Close()
	defer b.Close()
	a.Close()
	if err := b.Commit("b-token"); err != nil {
		t.Fatal(err)
	}
}
func TestLoginRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(secrets.NewMemory()).BeginLogin(ctx, "work", "anthropic"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

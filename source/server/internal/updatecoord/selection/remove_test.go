package selection

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/updatecoord/exclusion"
)

func TestRemoveExactSelection(t *testing.T) {
	for _, scenario := range []string{"success", "stale", "canceled", "identical-replacement", "after-remove-failure"} {
		t.Run(scenario, func(t *testing.T) {
			dir := newPrivateDir(t)
			lock := acquireUpdateLock(t, dir)
			sel := testSelection(1, "1.0.0")
			digest := writeFixture(t, dir, sel)
			unrelated := filepath.Join(dir, "keep")
			if e := os.WriteFile(unrelated, []byte("keep"), 0600); e != nil {
				t.Fatal(e)
			}
			before := readDest(t, dir)
			expected := Expected{Selection: sel, Digest: digest}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "stale":
				expected.Digest = testDigest("wrong")
			case "canceled":
				testHookBeforeRemove = func() error { cancel(); return nil }
			case "identical-replacement":
				testHookBeforeRemove = func() error {
					path := filepath.Join(dir, FileName)
					if e := os.Rename(path, filepath.Join(dir, "original")); e != nil {
						return e
					}
					return os.WriteFile(path, before, 0600)
				}
			case "after-remove-failure":
				testHookAfterRemove = func() error { return errors.New("injected post-effect uncertainty") }
			}
			t.Cleanup(func() { testHookBeforeRemove = nil; testHookAfterRemove = nil })
			var result Result
			e := lock.GuardUpdateSession(dir, func(g exclusion.GuardSession) error {
				var err error
				result, err = removeExactGuarded(ctx, g, dir, expected)
				return err
			})
			if scenario == "success" || scenario == "after-remove-failure" {
				if e != nil {
					t.Fatal(e)
				}
				want := CommitConfirmed
				if scenario == "after-remove-failure" {
					want = CommitDurabilityUncertain
				}
				if result.State != want {
					t.Fatalf("wrong outcome %+v", result)
				}
				if _, e = os.Lstat(filepath.Join(dir, FileName)); !os.IsNotExist(e) {
					t.Fatal("entry not removed", e)
				}
			} else {
				if e == nil || result.State != 0 {
					t.Fatalf("unsafe removal: %+v %v", result, e)
				}
				if !bytes.Equal(readDest(t, dir), before) {
					t.Fatal("selection changed")
				}
			}
			got, e := os.ReadFile(unrelated)
			if e != nil || string(got) != "keep" {
				t.Fatal("unrelated file touched", e)
			}
		})
	}
}
func TestRemoveRefusesUnguardedOrAbsentExpectation(t *testing.T) {
	dir := newPrivateDir(t)
	sel := testSelection(1, "1.0.0")
	digest := writeFixture(t, dir, sel)
	if _, e := removeExactGuarded(context.Background(), exclusion.GuardSession{}, dir, Expected{Selection: sel, Digest: digest}); !errors.Is(e, ErrInvalidRequest) {
		t.Fatal(e)
	}
	lock := acquireUpdateLock(t, dir)
	e := lock.GuardUpdateSession(dir, func(g exclusion.GuardSession) error {
		_, e := removeExactGuarded(context.Background(), g, dir, Expected{Absent: true})
		return e
	})
	if !errors.Is(e, ErrInvalidRequest) {
		t.Fatal(e)
	}
	if len(readDest(t, dir)) == 0 {
		t.Fatal("entry removed")
	}
}

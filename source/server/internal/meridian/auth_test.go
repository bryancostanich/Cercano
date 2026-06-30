package meridian

import (
	"errors"
	"runtime"
	"testing"
)

func TestHasClaudeAuthWith_FoundReturnsTrue(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("keychain probe is darwin-only")
	}
	got := hasClaudeAuthWith(func(name string, arg ...string) ([]byte, error) {
		return []byte("ignored"), nil
	})
	if !got {
		t.Fatalf("HasClaudeAuth = false, want true when security exits 0")
	}
}

func TestHasClaudeAuthWith_AbsentReturnsFalse(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("keychain probe is darwin-only")
	}
	got := hasClaudeAuthWith(func(name string, arg ...string) ([]byte, error) {
		return nil, errors.New("exit status 44: SecKeychainSearchCopyNext: The specified item could not be found")
	})
	if got {
		t.Fatalf("HasClaudeAuth = true, want false when security exits non-zero")
	}
}

func TestHasClaudeAuthWith_PassesServiceAndAccount(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("keychain probe is darwin-only")
	}
	var gotName string
	var gotArgs []string
	hasClaudeAuthWith(func(name string, arg ...string) ([]byte, error) {
		gotName = name
		gotArgs = arg
		return []byte{}, nil
	})
	if gotName != "/usr/bin/security" {
		t.Errorf("ran %q, want /usr/bin/security", gotName)
	}
	want := []string{"find-generic-password", "-s", keychainServiceClaude, "-a"}
	for i, w := range want {
		if i >= len(gotArgs) || gotArgs[i] != w {
			t.Errorf("arg[%d] = %q, want %q (full: %v)", i, gotArgs[i], w, gotArgs)
		}
	}
	if len(gotArgs) < 5 || gotArgs[len(gotArgs)-1] != "-w" {
		t.Errorf("last arg = %q, want -w (full: %v)", gotArgs[len(gotArgs)-1], gotArgs)
	}
}

func TestHasClaudeAuth_NonDarwinReturnsFalse(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("this case is for non-darwin platforms")
	}
	if HasClaudeAuth() {
		t.Fatalf("HasClaudeAuth = true on %s, want false (only darwin supported)", runtime.GOOS)
	}
}

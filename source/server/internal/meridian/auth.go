// Package meridian manages the local Meridian proxy subprocess and the
// prerequisites (Node.js, claude-code CLI, OAuth token) it needs to talk to
// Claude. Meridian itself reads its OAuth token from a well-known macOS
// keychain entry produced by `claude login`; this package probes for the same
// entry to surface "needs auth" before we even try to spawn the proxy.
package meridian

import (
	"os/exec"
	"os/user"
	"runtime"
)

// keychainServiceClaude is the service name that the Claude Code CLI writes
// its OAuth credentials under, and that Meridian reads from. See
// node_modules/@rynfar/meridian/dist/cli-7k1fcprd.js line 75:
//
//	var KEYCHAIN_SERVICE = "Claude Code-credentials";
const keychainServiceClaude = "Claude Code-credentials"

// execFn matches the signature of exec.Command(...).Output, factored out so
// tests can inject a fake without spawning /usr/bin/security.
type execFn func(name string, arg ...string) ([]byte, error)

func realExec(name string, arg ...string) ([]byte, error) {
	return exec.Command(name, arg...).Output()
}

// HasClaudeAuth returns true if a Claude OAuth credential is present in the
// macOS keychain for the current user. False on any other platform (Meridian
// only supports macOS today via this path), or if the keychain entry is
// absent / unreadable.
//
// This is a fast, side-effect-free probe — it never opens a browser or
// triggers a login flow. Callers use it to decide whether to render the
// "Sign in to Claude" path before attempting to start Meridian.
func HasClaudeAuth() bool {
	return hasClaudeAuthWith(realExec)
}

func hasClaudeAuthWith(run execFn) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	u, err := user.Current()
	if err != nil || u.Username == "" {
		return false
	}
	// `security find-generic-password -s <service> -a <account> -w` prints the
	// password to stdout and exits 0 if found, non-zero otherwise. We don't
	// actually read the password — exit code is the whole signal.
	_, err = run("/usr/bin/security",
		"find-generic-password",
		"-s", keychainServiceClaude,
		"-a", u.Username,
		"-w",
	)
	return err == nil
}

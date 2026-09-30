package tools

import (
	"context"
	"time"

	"cercano/source/server/internal/procx"
)

// Validator build/test gates shell out to toolchains that can legitimately run
// for a long time on a cold cache, so these bounds are generous. They exist to
// catch a *wedged* gate — a build waiting on a credential prompt, a test
// blocked on stdin, a lock held by another process — not to cap honest work.
// Per-toolchain rather than one global value, because "too long" differs by an
// order of magnitude between compileall and a cargo cold build.
const (
	goValidateTimeout     = 10 * time.Minute
	pyValidateTimeout     = 5 * time.Minute
	nodeValidateTimeout   = 15 * time.Minute
	rustValidateTimeout   = 30 * time.Minute
	dotnetValidateTimeout = 15 * time.Minute
	customValidateTimeout = 15 * time.Minute
)

// runValidator executes a validator command in dir under a timeout, with the
// process group reaped on timeout or cancellation.
//
// It collapses procx's two failure channels into the one the validators care
// about: ok is false for a non-zero exit, a timeout, or a cancellation. The
// combined output is always returned so the caller can surface it — on a
// timeout that partial output is the only evidence of where the gate stuck.
// err is non-nil only when the command did not run to completion, letting a
// caller distinguish "your build is broken" from "the gate itself failed".
func runValidator(ctx context.Context, timeout time.Duration, dir string, args ...string) (out string, ok bool, err error) {
	res, runErr := procx.Run(ctx, procx.Options{
		Args:    args,
		Dir:     dir,
		Timeout: timeout,
	})
	out = cleanOutput(string(res.Combined()))
	if runErr != nil {
		return out, false, runErr
	}
	return out, res.ExitCode == 0, nil
}

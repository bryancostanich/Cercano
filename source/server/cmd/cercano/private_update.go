package main

// Private updater execution entrypoint and updater-protocol capability
// query. This file is PURE early-entry dispatch: it must run before any
// config load, DB open, provider start, or agent start in main, and it must
// never fall through into normal startup once a reserved private argument
// has been seen.
//
// Namespace reservation: any first argument beginning with the reserved
// double-underscore prefix "__" is CLAIMED here. "__internal-update" parses
// into a oneshot.Request; any other "__"-prefixed token is rejected as
// unknown rather than falling through. This matters for version skew: an
// OLDER cercano binary treats an unknown first argument as normal startup
// and would boot the agent, so an updater must NEVER probe an old binary
// with the private execution flag. Probe age with the capability query
// `cercano version --updater-protocol` instead — older binaries' `version`
// subcommand already returns without startup while ignoring extra
// arguments, so absence of the versioned JSON distinguishes them safely.
//
// Scope note: this dispatch executes nothing on its own. Production main
// passes a nil runner and reports explicit unavailability; only tests
// inject a runner, and it is only ever called with parsed, validated
// inputs. No temporary copy of the binary is made here.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"cercano/source/server/internal/updatecoord/oneshot"
	"cercano/source/server/internal/updatecoord/state"
)

// privateUpdateNamespace is the single reserved private subcommand. The
// double-underscore prefix reserves the "__" namespace for updater-internal
// process entrypoints that must never reach user-facing dispatch.
const privateUpdateNamespace = "__internal-update"

// privateArgPrefix reserves every first argument that starts with "__".
// Anything in this namespace is claimed here and never falls through to the
// normal subcommand switch (or, on an older binary, into agent startup).
const privateArgPrefix = "__"

// Exit codes for the private dispatch. They are distinct and documented so
// the updater can classify outcomes without parsing prose:
//
//	0  — injected runner completed the operation
//	1  — injected runner reported a failure
//	2  — malformed/unknown private invocation or invalid identifiers
//	3  — valid invocation, but execution is unavailable in this build
const (
	privateExitExecution   = 1
	privateExitUsage       = 2
	privateExitUnavailable = 3
)

// updaterProtocolVersion is the protocol the capability query advertises.
const updaterProtocolVersion = 1

// privateUpdateRunner executes one parsed, validated update request
// in-process. It is the oneshot boundary's caller contract: the request
// carries only the installation and operation identifiers, never commands,
// paths, or release-provided instructions. Production main passes nil; only
// tests inject a non-nil runner.
type privateUpdateRunner func(ctx context.Context, req oneshot.Request) (oneshot.Result, error)

// updaterProtocolInfo is the versioned capability document emitted by
// `cercano version --updater-protocol`.
type updaterProtocolInfo struct {
	// Component identifies the binary answering the query.
	Component string `json:"component"`
	// Protocol is the updater-protocol revision this binary speaks.
	Protocol int `json:"protocol"`
	// ExecutionReady reports whether private update execution is wired up.
	// It is false until execution is actually available; an updater must
	// not attempt the private flag against a binary reporting false, and
	// must never attempt it against a binary producing no document at all.
	ExecutionReady bool `json:"execution_ready"`
}

// printUpdaterProtocol emits the capability document on out.
func printUpdaterProtocol(out io.Writer) error {
	return json.NewEncoder(out).Encode(updaterProtocolInfo{
		Component:      "cercano",
		Protocol:       updaterProtocolVersion,
		ExecutionReady: false,
	})
}

// dispatchPrivateUpdate claims and handles reserved private first
// arguments. It must be called FIRST in main, before the existing
// subcommand switch and before any config, DB, provider, or agent work.
//
// args is os.Args[1:]. The returned bool reports whether the invocation was
// claimed: when true the caller must exit with the returned code and must
// NOT proceed into normal startup, whatever the arguments looked like.
// When false (no reserved prefix) the caller continues unchanged.
//
// The accepted execution form is the exact canonical spelling
//
//	__internal-update --install-id ID --operation-id N
//
// with fixed flag order and no other tokens. Anything else — wrong order,
// duplicated flags, missing values, extra feed/command tokens — is rejected
// with exit 2 BEFORE any runner callback is considered. The install ID must
// pass state.ValidateInstallID; the operation ID must be a canonical
// positive base-10 int64 (no sign, no leading zeros, no non-digits, no
// overflow).
func dispatchPrivateUpdate(args []string, out, errOut io.Writer, runner privateUpdateRunner) (int, bool) {
	if len(args) == 0 || !strings.HasPrefix(args[0], privateArgPrefix) {
		return 0, false
	}
	if args[0] != privateUpdateNamespace {
		// Reserved namespace with an unknown spelling: reject rather than
		// fall through, so a typo never boots the agent.
		fmt.Fprintf(errOut, "cercano: unknown private command %q (reserved namespace)\n", args[0])
		return privateExitUsage, true
	}

	req, err := parsePrivateUpdateArgs(args[1:])
	if err != nil {
		fmt.Fprintf(errOut, "cercano: %v\nusage: cercano %s --install-id ID --operation-id N\n", err, privateUpdateNamespace)
		return privateExitUsage, true
	}

	if runner == nil {
		// Production build: execution is not wired up. Fail explicitly
		// rather than silently succeeding or starting the agent.
		fmt.Fprintln(errOut, "cercano: update execution is unavailable in this build")
		return privateExitUnavailable, true
	}

	res, runErr := runner(context.Background(), req)
	if runErr != nil {
		fmt.Fprintf(errOut, "cercano: update execution failed (operation %d)\n", req.OperationID)
		return privateExitExecution, true
	}
	// Success: the persisted outcome is reported by the runner's result;
	// this dispatch prints nothing further to keep stdout stable for
	// future machine consumption.
	_ = res
	return 0, true
}

// parsePrivateUpdateArgs parses the tokens after the namespace token into a
// oneshot.Request. Only the exact canonical fixed-order form is accepted.
func parsePrivateUpdateArgs(rest []string) (oneshot.Request, error) {
	const usage = "expected exact form --install-id ID --operation-id N"
	if len(rest) != 4 || rest[0] != "--install-id" || rest[2] != "--operation-id" {
		return oneshot.Request{}, fmt.Errorf("malformed %s invocation: %s", privateUpdateNamespace, usage)
	}
	if err := state.ValidateInstallID(rest[1]); err != nil {
		return oneshot.Request{}, fmt.Errorf("invalid install id: %w", err)
	}
	opID, err := parseCanonicalOperationID(rest[3])
	if err != nil {
		return oneshot.Request{}, fmt.Errorf("invalid operation id: %w", err)
	}
	return oneshot.Request{InstallID: rest[1], OperationID: opID}, nil
}

// parseCanonicalOperationID parses a positive base-10 int64 in its canonical
// spelling: ASCII digits only (no sign, no whitespace, no separators), no
// leading zeros, and no overflow past int64.
func parseCanonicalOperationID(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	if s[0] == '0' {
		return 0, fmt.Errorf("leading zero or zero value %q", s)
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("non-digit %q", s[i:i+1])
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("out of int64 range")
	}
	if n <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return n, nil
}

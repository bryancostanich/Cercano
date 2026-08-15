#!/usr/bin/env bash
# Cercano dev launcher.
#
# Cercano is now two binaries:
#   * cercano      — the agent gRPC server (a singleton; built from source/server)
#   * cercano-cli  — the terminal UI (built from source/clients/cli)
# The CLI auto-launches `cercano agent` when no server is listening, finding the
# server binary as a sibling next to its own executable. This launcher builds
# both, co-locates them in a libexec dir so that sibling lookup works, then
# exec's the CLI.
#
# Behaviors:
#   1. Rebuild-if-stale: rebuild either binary whose sources changed since it was
#      last built. The CLI depends on the server module (pkg/proto, pkg/config,
#      pkg/update) via a replace directive, so any .go change under source/
#      restages the CLI.
#   2. Kill stale agents: any running `cercano agent` whose start time predates
#      the freshly built server binary is killed (it's running outdated code; the
#      next CLI launch spawns a fresh one).
#   3. Exec the CLI with the original args.
#
# Install: run scripts/install-launcher.sh (or: make launcher, from source/server)
#
# Override the source repo path via CERCANO_REPO env var if needed.

set -euo pipefail

REPO="${CERCANO_REPO:-$HOME/git_repos/bryan_costanich/cercano}"
# Exported so the CLI (and its /d development mode) can find the repo.
export CERCANO_REPO="$REPO"
SERVER_DIR="$REPO/source/server"
CLI_DIR="$REPO/source/clients/cli"

# Both binaries live co-located in a hidden libexec dir so the CLI's sibling
# lookup (next to its own executable) finds the `cercano` server binary. Keeping
# them out of ~/bin avoids clashing with this launcher script (also ~/bin/cercano).
LIBEXEC="$HOME/bin/.cercano-libexec"
SERVER_BIN="$LIBEXEC/cercano"
CLI_BIN="$LIBEXEC/cercano-cli"

mkdir -p "$LIBEXEC"

# stale BIN SRC_GLOB_ROOT — true if BIN is missing or any .go under SRC_GLOB_ROOT
# is newer than BIN.
stale() {
    local bin="$1" root="$2"
    [[ ! -x "$bin" ]] && return 0
    find "$root" -name '*.go' -newer "$bin" -print -quit 2>/dev/null | grep -q .
}

# build_install BIN BUILD_DIR PKG — build PKG into a temp sibling of BIN, sign
# it, then mv it over BIN. Never rewrite the installed path in place: if an
# older instance still has that inode mapped, an in-place `go build -o` or
# `codesign --force` poisons the kernel's per-vnode signature cache and every
# later exec of the path dies with SIGKILL until the file is replaced. The mv
# gives the path a fresh inode on every install, which sidesteps that entirely.
build_install() {
    local bin="$1" dir="$2" pkg="$3"
    local tmp="$bin.new.$$"
    if ! (cd "$dir" && go build -o "$tmp" "$pkg"); then
        rm -f "$tmp"
        return 1
    fi
    "$SERVER_DIR/scripts/codesign-if-available.sh" "$tmp"
    mv -f "$tmp" "$bin"
}

# 1a. Rebuild the server binary if its sources changed.
if stale "$SERVER_BIN" "$SERVER_DIR"; then
    echo "[cercano] rebuilding agent server..." >&2
    if ! build_install "$SERVER_BIN" "$SERVER_DIR" ./cmd/cercano/; then
        echo "[cercano] server build failed" >&2
        exit 1
    fi
fi

# 1b. Rebuild the CLI if anything under source/ changed (it depends on the
#     server module via a replace directive).
if stale "$CLI_BIN" "$REPO/source"; then
    echo "[cercano] rebuilding CLI..." >&2
    if ! build_install "$CLI_BIN" "$CLI_DIR" .; then
        echo "[cercano] CLI build failed" >&2
        exit 1
    fi
fi

# 2. Kill any stale `cercano agent` processes (started before current server binary).
bin_mtime=$(stat -f %m "$SERVER_BIN")

for pid in $(pgrep -f "cercano agent" 2>/dev/null); do
    [[ -z "$pid" ]] && continue
    # Don't kill ourselves or our own subprocesses (defensive — pgrep -f could
    # match a parent shell line that happens to contain "cercano agent").
    [[ "$pid" == "$$" ]] && continue

    lstart=$(ps -o lstart= -p "$pid" 2>/dev/null | sed 's/^ *//')
    [[ -z "$lstart" ]] && continue

    # macOS ps lstart format: "Sun Jun 21 21:13:42 2026"
    proc_epoch=$(date -j -f "%a %b %e %H:%M:%S %Y" "$lstart" "+%s" 2>/dev/null || echo "")
    [[ -z "$proc_epoch" ]] && continue

    if (( proc_epoch < bin_mtime )); then
        echo "[cercano] killing stale agent pid=$pid (started $lstart, before binary built)" >&2
        kill "$pid" 2>/dev/null || true
        # Give the agent time to run its cleanup (SIGTERM → Shutdown → kills the
        # Meridian child group). Escalate to SIGKILL only if it hangs — a hard
        # kill skips cleanup and orphans Meridian (the next agent's reaper will
        # catch it, but prefer not to create the orphan at all).
        for _ in $(seq 1 20); do
            kill -0 "$pid" 2>/dev/null || break
            sleep 0.1
        done
        if kill -0 "$pid" 2>/dev/null; then
            echo "[cercano] agent pid=$pid ignored SIGTERM; escalating to SIGKILL" >&2
            kill -9 "$pid" 2>/dev/null || true
            sleep 0.2
        fi
    fi
done

# 3. Exec the CLI with original args. It dials the server, auto-launching
#    `$SERVER_BIN agent` (found as its sibling) if none is listening.
exec "$CLI_BIN" "$@"

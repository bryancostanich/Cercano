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
# Install:
#   cp source/server/scripts/cercano-launcher.sh ~/bin/cercano
#   chmod +x ~/bin/cercano
#
# Override the source repo path via CERCANO_REPO env var if needed.

set -euo pipefail

REPO="${CERCANO_REPO:-$HOME/git_repos/bryan_costanich/cercano}"
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

# 1a. Rebuild the server binary if its sources changed.
if stale "$SERVER_BIN" "$SERVER_DIR"; then
    echo "[cercano] rebuilding agent server..." >&2
    if ! (cd "$SERVER_DIR" && go build -o "$SERVER_BIN" ./cmd/cercano/); then
        echo "[cercano] server build failed" >&2
        exit 1
    fi
fi

# 1b. Rebuild the CLI if anything under source/ changed (it depends on the
#     server module via a replace directive).
if stale "$CLI_BIN" "$REPO/source"; then
    echo "[cercano] rebuilding CLI..." >&2
    if ! (cd "$CLI_DIR" && go build -o "$CLI_BIN" .); then
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
        sleep 0.2
    fi
done

# 3. Exec the CLI with original args. It dials the server, auto-launching
#    `$SERVER_BIN agent` (found as its sibling) if none is listening.
exec "$CLI_BIN" "$@"

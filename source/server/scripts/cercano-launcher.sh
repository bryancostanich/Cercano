#!/usr/bin/env bash
# Cercano dev launcher.
#
# Behaviors:
#   1. Rebuild-if-stale: if any .go file under source/server/ is newer than the
#      installed binary, rebuild from source before invoking.
#   2. Kill stale agents: any running `cercano agent` process whose start time
#      predates the current binary's mtime is killed (it's running outdated
#      code; the next CLI launch will spawn a fresh one).
#   3. Enable TUI key-debug tracing by default for prompt-selection debugging.
#      Set CERCANO_DEBUG_KEYS=0 to disable it.
#   4. Exec the binary with the original args. TTY detection in the binary
#      itself picks CLI vs agent mode.
#
# Install:
#   cp source/server/scripts/cercano-launcher.sh ~/bin/cercano
#   chmod +x ~/bin/cercano
#
# Override the source repo path via CERCANO_REPO env var if needed.

set -euo pipefail

REPO="${CERCANO_REPO:-$HOME/git_repos/bryan_costanich/cercano}"
SRC_DIR="$REPO/source/server"
BIN="$HOME/bin/.cercano-bin"

mkdir -p "$(dirname "$BIN")"

# Temporary prompt-selection diagnostic: the TUI writes key events to
# /tmp/cercano-key-debug.log when enabled. Keep this in the dev launcher so the
# normal launch path captures macOS Terminal's actual Shift/Option/Cmd events.
export CERCANO_DEBUG_KEYS="${CERCANO_DEBUG_KEYS:-1}"

# 1. Rebuild if stale.
rebuild=0
if [[ ! -x "$BIN" ]]; then
    rebuild=1
elif find "$SRC_DIR" -name '*.go' -newer "$BIN" -print -quit 2>/dev/null | grep -q .; then
    rebuild=1
fi

if (( rebuild )); then
    echo "[cercano] rebuilding..." >&2
    if ! (cd "$SRC_DIR" && go build -o "$BIN" ./cmd/cercano/); then
        echo "[cercano] build failed" >&2
        exit 1
    fi
fi

# 2. Kill any stale `cercano agent` processes (started before current binary).
bin_mtime=$(stat -f %m "$BIN")

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

# 3. Exec the binary with original args.
exec "$BIN" "$@"

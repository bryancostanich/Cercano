#!/usr/bin/env bash
set -euo pipefail
ROOT="/Users/bryancostanich/git_repos/bryan_costanich/Cercano"
SERVER="$ROOT/source/server"
OUT="$ROOT/efforts/llama-server-memory-guard/restart-verify/run-branch.log"
CLIENT_GO="$ROOT/efforts/llama-server-memory-guard/restart-verify/start_glm_client.go"
AGENT_BIN="$ROOT/efforts/llama-server-memory-guard/restart-verify/cercano-under-test"
PORT="50052"

log() { printf '\n[%s] %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*" | tee -a "$OUT"; }
run() { log "$*"; bash -lc "$*" 2>&1 | tee -a "$OUT"; }
llamas() { ps -Ao pid,ppid,pgid,rss,comm,args | awk '/\/llama-server --model/ {print}'; }
agents() { ps -Ao pid,ppid,pgid,rss,comm,args | grep -E '[c]ercano(.*/)? agent|[.]cercano-libexec/cercano agent' || true; }

: > "$OUT"
log "branch restart verifier starting"
run "cd '$ROOT' && git rev-parse --abbrev-ref HEAD && git rev-parse HEAD"
log "building branch binary under test"
(cd "$SERVER" && go build -o "$AGENT_BIN" ./cmd/cercano) 2>&1 | tee -a "$OUT"
BASE_LINES=$(wc -l < "$HOME/.config/cercano/crash.log" 2>/dev/null || echo 0)
log "baseline crash.log lines=$BASE_LINES"
run "ps -Ao pid,ppid,pgid,rss,comm,args | grep -E '[c]ercano(.*/)? agent|[.]cercano-libexec/cercano agent' || true"
log "baseline llama-server processes"
llamas | tee -a "$OUT" || true

OLD_AGENT=$(ps -Ao pid,args | awk '/[.]cercano-libexec\/cercano agent|restart-verify\/cercano-under-test agent/ {print $1; exit}')
if [[ -z "${OLD_AGENT:-}" ]]; then
  log "no active agent found; starting branch agent"
else
  log "sending SIGTERM to old agent $OLD_AGENT"
  kill -TERM "$OLD_AGENT" || true
fi

log "sampling llama-server processes during shutdown window"
for i in $(seq 1 48); do
  printf 'sample=%02d ' "$i" | tee -a "$OUT"
  L=$(llamas || true)
  if [[ -n "$L" ]]; then printf '%s\n' "$L" | tee -a "$OUT"; else printf 'no llama-server\n' | tee -a "$OUT"; fi
  sleep 0.25
done

log "starting branch agent under test"
nohup "$AGENT_BIN" agent >> "$OUT.agent.stdout" 2>&1 &
echo $! > "$ROOT/efforts/llama-server-memory-guard/restart-verify/new-branch-agent.pid"

log "waiting for agent port $PORT"
for i in $(seq 1 120); do
  if nc -z 127.0.0.1 "$PORT" >/dev/null 2>&1; then
    log "agent port is listening"
    break
  fi
  sleep 0.25
  if [[ "$i" == "120" ]]; then
    log "agent port did not come back"
    exit 1
  fi
done
run "ps -Ao pid,ppid,pgid,rss,comm,args | grep -E '[c]ercano(.*/)? agent|[.]cercano-libexec/cercano agent|restart-verify/cercano-under-test agent' || true"
log "llama-server processes before GLM restart"
llamas | tee -a "$OUT" || true

cat > "$CLIENT_GO" <<'EOF'
package main
import (
  "context"
  "fmt"
  "strings"
  "time"
  "cercano/source/server/pkg/agentclient"
)
func main() {
  ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
  defer cancel()
  c, err := agentclient.Dial(ctx, "localhost:50052")
  if err != nil { panic(err) }
  defer c.Close()
  cat, err := c.ListRuntimeModels(ctx)
  if err != nil { panic(err) }
  var id string
  for _, m := range cat.Models {
    if strings.Contains(strings.ToLower(m.DisplayName), "glm-4.5-air") || strings.Contains(strings.ToLower(m.ID), "glm-4.5-air") {
      id = m.ID
      fmt.Printf("glm_model_id=%s display=%q state=%s\n", m.ID, m.DisplayName, m.RuntimeState)
      break
    }
  }
  if id == "" { panic("GLM model not found") }
  inst, err := c.StartRuntimeModel(ctx, "llama_server", id)
  if err != nil { panic(err) }
  fmt.Printf("start_ok instance_id=%s pid=%d port=%d state=%s\n", inst.ID, inst.PID, inst.Port, inst.State)
}
EOF

log "starting GLM through StartRuntimeModel after restart"
(cd "$SERVER" && go run "$CLIENT_GO") 2>&1 | tee -a "$OUT"

log "sampling llama-server processes after GLM start"
for i in $(seq 1 30); do
  printf 'post_sample=%02d ' "$i" | tee -a "$OUT"
  L=$(llamas || true)
  if [[ -n "$L" ]]; then printf '%s\n' "$L" | tee -a "$OUT"; else printf 'no llama-server\n' | tee -a "$OUT"; fi
  sleep 0.5
done

log "runtime events since baseline"
tail -n +$((BASE_LINES+1)) "$HOME/.config/cercano/crash.log" 2>/dev/null | grep '"kind":"runtime_event"' | tee -a "$OUT" || true

LLAMA_COUNT=$(llamas | wc -l | tr -d ' ')
log "final_llama_count=$LLAMA_COUNT"
if [[ "$LLAMA_COUNT" -gt 1 ]]; then
  log "FAIL: more than one llama-server remains"
  exit 2
fi
log "PASS: branch restart verifier completed without overlapping final llama-server"

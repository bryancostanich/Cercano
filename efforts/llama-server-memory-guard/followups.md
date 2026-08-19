# Deferred follow-ups

## Registry-wide cross-process lock

The biggest residual risk is the cross-process check-then-spawn race recorded in
`spec.md`: the llama-server registry is a set of per-owner PID files guarded by a
per-process mutex only. Two independent Cercano agents can both sample memory, both
conclude a large model fits, and both spawn.

Fix: add a filesystem lock (for example `flock` on a registry-wide lock file) held
across the pre-spawn reap barrier, memory projection, registry update, and `cmd.Start`.
This intentionally was not done in this effort because it changes the registry's
ownership model.

## Guard override

Decision 4 deliberately omitted an override. The false-refusal cost is a failed model
start with numbers; the false-permit cost is a hard machine lock. After collecting
projected-vs-actual data from `runtime_event` records, consider a narrowly-scoped
escape hatch if the 10 GiB margin proves too conservative.

## Live restart verification

The remaining Phase 5 check requires terminating/restarting the agent process that is
currently serving the conversation (`/Users/bryancostanich/bin/.cercano-libexec/cercano agent`, PID observed as 10805 during verification). That is disruptive and was left blocked in the plan. Run it from an external terminal after checkpointing:

1. Ensure GLM-4.5-Air is live.
2. Restart the agent.
3. Confirm `~/.config/cercano/crash.log` records reap/stop behavior and that there is never more than one GLM llama-server process resident.

## Dispatch sub-agent context overflow

During investigation, a delegated sub-agent failed preflight with
`context_overflow (94632 tokens vs 32768 limit)`, implying it received the full
main-thread history instead of just its task. That defeats delegation's purpose and
should be investigated separately.

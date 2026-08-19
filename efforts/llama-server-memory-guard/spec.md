# llama-server Memory Guard and Durable Runtime Events

## Problem / motivation

On 19 Aug 2026 at ~08:54 the machine hard-locked: no kernel panic report, no Jetsam
kill report, forced power cycle. The evidence that survived (an append-only dispatch
log and `~/.config/cercano/crash.log`) shows an agent restart seconds before the
freeze while a llama-server was live on port 60891.

The numbers explain the freeze. A single GLM-4.5-Air Q4_K_M llama-server measured
**68.2 GB resident** on a **128 GB** machine — 53% of physical RAM for one process.
Two overlapping servers require ~136 GB against 128 GB physical. There is no headroom
for even a momentary overlap.

Critically, llama-server is launched with `--gpu-layers` (provider.go:572) and **no
`--no-mmap`**, so on Apple Silicon the weights land in Metal buffers in unified
memory, which are **wired** (non-evictable). That is why the machine froze rather
than merely paging: with no reclaimable pages left, the kernel had nowhere to go.
Had that 68 GB been ordinary evictable page cache, the symptom would have been
slowness and Jetsam kills, not a hard lock.

Two defects combine to allow the overlap:

1. **Teardown reports success on signal delivery, not on process death.**
   `killProcess` (`process_unix.go:19`) sends SIGTERM and returns immediately.
   This is what the normal shutdown path uses (`Server.Shutdown` →
   `stopRuntimeInstances` → `Provider.Stop` → `killProcess`), so the old agent
   declares its 68 GB server "stopped" while the memory is still wired.
   `terminateGroup` (the orphan path) is better — it polls for 2 s after SIGTERM —
   but then fires SIGKILL and returns without confirming death.
2. **Nothing checks memory before a spawn.** `startProcess` goes straight from
   `exec.Command` to `cmd.Start()`. `SweepOrphans` cannot help here because it
   explicitly skips any owner still alive (`if ownerAlive(state) { continue }`),
   and during a restart the old owner is typically still draining.

Compounding both: **the runtime is blind at exactly the point where it can take the
machine down.** Every lifecycle message (`spawn`, `reuse`, `adopt`, `reap`) goes
through `Provider.emit`, which only calls `sink.WriteLog` — an in-memory ring buffer
in `localruntime.manager` feeding the UI. It evaporates on process exit. A grep for
`starting llama-server` across the entire 40 MB / 57,000-line dispatch log going back
to 11 Aug returns **zero matches**. Separately, `cmd/agent/main.go` never calls
`setupDispatchLogFile` (only `cmd/cercano` does), so the agent binary's `log.Printf`
output is not durably captured either.

## Goals

- Make llama-server lifecycle and memory events durable, structured, and correlated
  with crash/signal records on one timeline.
- Close the spawn/teardown overlap window so two large llama-servers cannot be
  simultaneously resident.
- Refuse a spawn that is projected to exhaust non-evictable memory, with an error
  that explains itself in real numbers.
- Record projected-vs-actual memory on every spawn so the headroom margin can be
  calibrated from data rather than guesswork.

## Non-goals

- Do not build a general memory-management or model-eviction policy.
- Do not add a runtime memory dashboard or UI surface in this effort.
- Do not change `--gpu-layers` / mmap launch behavior to make weights evictable.
- Do not add a guard override / escape hatch yet (see Decision 4 rationale).
- Do not fix the separate `dispatch` sub-agent context-overflow bug observed during
  investigation (sub-agent receives full main-thread history); that is its own effort.
- Do not rotate or prune the crash log beyond what `internal/crashlog` already does.

## Constraints

- `internal/crashlog` already writes newline-delimited JSON to
  `~/.config/cercano/crash.log`, append-only with fsync-per-write. It survived the
  reboot that destroyed everything else. Reuse that durability; do not reimplement it.
- Only one caller may `cmd.Wait()` a child process. The `watch()` goroutine already
  owns `cmd.Wait()` for our own children, so synchronous stop must **not** call
  `cmd.Wait()` again — it must poll `processAlive` or be signalled by the watcher.
  Getting this wrong yields `wait: no child processes` or a hang.
- `processAlive(pid)` (`kill(pid, 0)`) already exists and is the correct primitive
  for waiting on a process we do not own.
- Waiting must never be done while holding `p.mu`, or a slow teardown deadlocks
  every concurrent `Start`.
- Existing reuse guards are sound and must be preserved: `adoptLiveSibling`, the
  double-checked `liveInstanceForLocked` inside the insert's critical section, and
  `SweepOrphans`. This effort adds to them; it does not replace them.
- Memory probes must soft-fail: on error return "unknown" and fall back, matching
  the convention `sysram.Total()` already uses (returns 0, callers render unknown
  rather than a wrong verdict).
- `sysram` uses per-OS build tags (`_darwin`, `_linux`, `_other`); a new probe must
  follow the same structure.
- The guard must not fire on evictable page cache. On macOS free memory is
  routinely tiny by design; a free-memory-based check would refuse nearly every
  legitimate spawn.

## Decisions

### Decision 1 — Durable lifecycle/memory events go into `internal/crashlog`

Chosen option: extend `internal/crashlog` with a runtime-event kind; spawn, reuse,
adopt, reap, and memory records write to `~/.config/cercano/crash.log`.

| Axis | Extend `crashlog` | New runtime-events log | Stdlib logger → dispatch log |
|---|---|---|---|
| Cost | Small: one new Kind, one method | Small, but duplicates ~120 lines of writer plumbing | Smallest: one line |
| Survives process death | Yes — append-only, fsync-per-write, proven across the reboot | Yes | Yes, but only for `cmd/cercano`; agent binary never wires it up |
| Correlation with crash | **Same file, same clock as signal/panic records** | Manual timestamp joining across two files | Separate file again |
| Structured fields | Yes, via existing `Extra map[string]any` | Yes | No — free text, grep-only |
| Risk | Slight semantic stretch: "crash log" holds non-crash events | None | Loses structure; silently does nothing in the agent binary |
| Volume | Per-spawn (rare), not per-request — negligible | Same | Same |

Rationale: the only reason there is any evidence about this lockup is that
`crash.log` is append-only and survived the reboot. That durability is already built
and tested. The decisive argument is correlation: the question asked after the next
incident is "what was the runtime doing in the seconds before this signal?", which is
only answerable if both record types share one file and one clock. The semantic
stretch is paid down by amending the package doc comment to describe a durable
operational event log rather than strictly crashes. The stdlib-logger option is
rejected outright: unstructured, and not initialized in the very binary that spawns
llama-servers.

### Decision 2 — Close the overlap from both ends: synchronous teardown *and* a pre-spawn reap barrier

Chosen option: both. Teardown waits for actual process death; `Start` additionally
confirms no doomed-but-resident server is holding memory before spawning.

| Axis | Synchronous teardown only | Pre-spawn reap barrier only | Both |
|---|---|---|---|
| Covers polite restart (old agent gets SIGTERM) | Yes | Yes | Yes |
| Covers old agent SIGKILLed / crashed | **No** — no code runs in a dead agent | Yes — the survivor defends itself | Yes |
| Covers concurrent independent agent | No | Partly — needs the `ownerAlive` predicate widened | Yes |
| Deadlock/hang risk | Moderate: must not hold `p.mu`, must not double-`Wait()` | Low: polls foreign PIDs, no lock held | Moderate, same care |
| Slows shutdown | Yes, up to the grace period | No | Yes |
| Slows first spawn | No | Only when a doomed server is present | Same |

Rationale: the barrier is the load-bearing half, because it runs in the *surviving*
process at the exact moment of danger. Synchronous teardown alone has a fatal gap —
it only helps when the dying agent gets to run code, and any SIGKILL, crash, or
force-quit bypasses it entirely. `self-dev.md` already documents that killing the
agent does not kill its llama-server children, and this dev loop restarts agents
constantly. Synchronous teardown is still worth doing: a shutdown path that claims
completion while 68 GB is still wired is a lie that will bite elsewhere, and it makes
the barrier trip far less often. Two deliberate details: widen the reap predicate
beyond `ownerAlive` (the old owner is frequently still draining — precisely why the
current sweeper skipped it), and confirm death by polling `processAlive` rather than
trusting the signal.

### Decision 3 — The fit check measures wired + compressed memory, with registry arithmetic as cross-check and fallback

Chosen option: projected non-evictable footprint versus physical RAM. Registry
arithmetic (`SizeBytes` sum over live instances) is demoted to a cross-check and to
the fallback when the probe returns unknown.

| Axis | Wired + compressed vs. total | Registry arithmetic only | Wired as rule, registry as cross-check |
|---|---|---|---|
| Measures what actually causes the freeze | **Yes** | No — proxies via on-disk size | Yes |
| Sees non-Cercano wired memory (other GPU apps, VMs) | Yes | No | Yes |
| Sees a dying-but-still-wired server | Yes, directly — wired until the process truly dies | Only while still in the registry | Yes |
| False refusals from page cache | None — cache is not wired | None | None |
| Predicts the new process's demand | Needs `SizeBytes` for the delta | That is all it has | Uses `SizeBytes` for the delta |
| Complexity | Per-OS probe with build tags | Trivial | Both |
| Failure mode | Probe unavailable → needs a fallback | Blind to the rest of the machine | Falls back to registry arithmetic |

Rationale: an earlier draft of this decision framed the OS probe as "free/available
memory" and rejected it as noisy on macOS. That rejection was valid for *free* memory
but discarded the quantity that actually governs the failure. With Metal offload and
mmap'd weights, the model's memory is wired and non-evictable; free memory was never
the binding constraint. The guard is therefore:

```
projected = currentWiredAndCompressed + model.SizeBytes + (KVBytesPerToken × ctxSize)
refuse if projected > sysram.Total() − headroom
```

`SizeBytes` is a good estimate of the weights delta (68 GB on disk produced 68.2 GB
resident for GLM-4.5-Air). It under-counts the KV cache, which is not negligible at
large `--ctx-size`, so the projection adds the KV term — `internal/gguf` already
exposes `KVBytesPerToken`, used by `model_ram_estimate.go`, and effective context
size is available via `EffectiveContextSize` (`context_size.go`). This decision also
supersedes an earlier proposal to consult
`kern.memorystatus_vm_pressure_level` as a veto: pressure level is a coarse, lagging
signal derived from available pages, and reading wired directly gives the leading
indicator instead.

Known unknowns, to be resolved during implementation rather than assumed:
- The exact page accounting — whether to include the compressor pool, and how
  `hw.memsize` relates to the `vm_stat` page-count sum — must be verified against a
  live `vm_stat` as the first implementation step.
- The headroom margin needs a real number. Start conservative (~8–10 GB, or a
  percentage of total) and log projected-vs-actual on every spawn to calibrate.

### Decision 4 — A tripped guard hard-blocks the spawn

Chosen option: return an error from `Start`; no process is spawned. No override.

| Axis | Hard block | Warn and proceed | Block with override |
|---|---|---|---|
| Prevents the lockup | Yes | **No** — identical to today, just documented | Yes, unless overridden |
| Bad-estimate failure mode | Refuses a load that would have fit | Never blocks | Refuses, with an escape hatch |
| Surfacing | Reuses `InstanceFailed` + `LastError`, already rendered | Log-only; invisible unless you go digging | Same as block, plus override named |
| Risk | Too-conservative margin becomes an obstacle | Machine can still hard-lock | Override becomes a reflex, silently restoring risk |
| Effort | Smallest | Smallest | Small |

Rationale: asymmetry of consequences. A false refusal costs one failed model start
with a clear message. A false permit costs the entire machine — every unsaved buffer,
a hard power cycle. Those are not comparable, so the guard is tuned toward the
recoverable failure. "Warn and proceed" is rejected because it leaves the bug fully
live and the warning is only readable after rebooting and going digging — that is
today's behavior. The override is deliberately deferred: it is cheap insurance
against a bad margin, but it only earns its keep once there is evidence the margin is
wrong, and the Decision 1 instrumentation is exactly what will produce that evidence.
Adding it now mostly creates a reflex to use it; if the margin proves annoying, it is
a small follow-up informed by real numbers.

Regardless of option, the error text must carry the actual figures — projected wired,
current wired, model size, KV estimate, headroom, limit — and name the other instance
holding the memory when the registry cross-check identifies one, since the common case
will be a stale server the user can simply stop.

## Known residual risks

These are *not* solved by this effort. They are recorded so they are not mistaken for
coverage.

- **Cross-process check-then-spawn race.** The PID registry is a set of per-owner
  files (`~/.cercano/run/llamaserver/<pid>.json`) guarded by a *per-process* mutex
  (`pidRegistry.mu`, orphans.go). There is no cross-process lock. Two independent
  Cercano agents can therefore both sample memory, both conclude the model fits, and
  both spawn — reproducing the very overlap the guard exists to prevent. This effort
  narrows the window (each agent reaps and confirms death first) but does not close
  it. Closing it properly needs a filesystem lock (e.g. `flock` on a registry-wide
  file) held across check-and-spawn, which changes the registry's ownership model and
  is deliberately deferred. The Decision 1 instrumentation will reveal whether this
  race actually occurs in practice.
- **Guard-to-`cmd.Start()` window within a single process.** Memory is sampled
  shortly before `exec`, and the wired footprint grows as the model loads, so a
  marginal spawn can still overcommit. The headroom margin absorbs this — another
  reason to start conservative.
- **`SizeBytes` is a proxy, not a measurement.** It is on-disk weight size. It tracked
  resident size closely for GLM-4.5-Air (68 GB on disk → 68.2 GB resident) under mmap
  with full GPU offload, but that is not guaranteed for other quantizations, partial
  offload, or multi-file models. The projected-vs-actual logging exists precisely to
  detect where this proxy breaks down.
- **Descendants outside the tracked process group.** Reaping targets the recorded PID
  and its group; a llama-server descendant that escaped the group would not be waited
  on. No evidence this happens today, but the confirm-death poll only proves the
  *recorded* PID is gone.

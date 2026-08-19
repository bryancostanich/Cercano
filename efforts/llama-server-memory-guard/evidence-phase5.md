# Phase 5 evidence

Captured 2026-08-19 after implementing Phases 1–4.

## Live GLM spawn record exists in durable log

`~/.config/cercano/crash.log` now contains a structured `runtime_event` spawn record:

```json
{
  "kind": "runtime_event",
  "event": "spawn",
  "reason": "started llama-server for GLM-4.5-Air Q4_K_M",
  "runtime": {
    "runtime": "llama_server",
    "instance_id": "llama_server:690083de81a3:59339",
    "model_id": "llama_server:catalog:glm-4.5-air-q4_k_m",
    "pid": 10810,
    "port": 59339
  },
  "extra": {
    "context_tokens": 32768,
    "current_bytes": 16553541632,
    "current_probe_ok": true,
    "fallback_to_registry": false,
    "headroom_bytes": 10737418240,
    "kv_bytes": 6308233216,
    "kv_bytes_per_token": 192512,
    "limit_bytes": 126701535232,
    "model_bytes": 72975748384,
    "model_path": "/Users/bryancostanich/.cercano/models/glm-4.5-air-q4_k_m/GLM-4.5-Air-Q4_K_M-00001-of-00002.gguf",
    "projected_bytes": 95837523232,
    "registry_bytes": 0,
    "total_bytes": 137438953472
  }
}
```

This directly verifies Phase 1 and Phase 4's durable spawn logging: before this effort,
grepping the full dispatch history for `starting llama-server` returned zero matches.

## Projection versus measured live RSS

Current process:

```
PID 10810
/opt/homebrew/bin/llama-server --model ...GLM-4.5-Air-Q4_K_M-00001-of-00002.gguf --host 127.0.0.1 --port 59339 --gpu-layers auto --jinja --ctx-size 32768 --cache-type-k q8_0 --cache-type-v q8_0
RSS 77646528 KiB = 74.05 GiB
```

Projection reconciliation:

| Quantity | Bytes | GiB |
|---|---:|---:|
| Spawn-time baseline non-evictable | 16553541632 | 15.42 |
| Model + KV projected delta | 79283981600 | 73.84 |
| Spawn projection total | 95837523232 | 89.26 |
| Measured live RSS | 79510044672 | 74.05 |
| Baseline + live RSS | 96063586304 | 89.47 |
| Delta versus projection | +226063072 | +0.21 |

The projection is within **0.24%** of baseline + measured live RSS. That is strong evidence
that `model.SizeBytes + KVBytesPerToken × ctxSize` is the right first-order estimate for
this model and launch mode.

## Public RPC refusal check

To exercise the same surface the dashboard uses, I created a sparse 60 GiB fake GGUF:

```
~/.cercano/models/memory-guard-e2e/Memory-Guard-E2E-Refusal-Q4_K_M.gguf
```

Discovery accepted it by `.gguf` extension and apparent file size, but because the guard
runs before `cmd.Start`, the file could not allocate RAM or launch anything. The file was
removed immediately after the RPC returned.

The public `StartRuntimeModel` RPC refused it with the full numeric error:

```
llama-server memory guard refused to start Memory-Guard-E2E-Refusal-Q4_K_M:
projected non-evictable memory 150.01 GiB exceeds limit 118.00 GiB
(current 90.01 GiB, model 60.00 GiB, KV 0 B for 16384 ctx tokens,
headroom 10.00 GiB, total 128.00 GiB,
largest live instance llama_server:690083de81a3:59339 pid 10810 port 59339)
```

The durable `refused` event landed in `~/.config/cercano/crash.log`:

```json
{
  "kind": "runtime_event",
  "event": "refused",
  "reason": "llama-server memory guard refused to start Memory-Guard-E2E-Refusal-Q4_K_M: projected non-evictable memory 150.01 GiB exceeds limit 118.00 GiB (current 90.01 GiB, model 60.00 GiB, KV 0 B for 16384 ctx tokens, headroom 10.00 GiB, total 128.00 GiB, largest live instance llama_server:690083de81a3:59339 pid 10810 port 59339)",
  "runtime": {
    "runtime": "llama_server",
    "model_id": "llama_server:99e21099a724"
  },
  "extra": {
    "blocking_instance_id": "llama_server:690083de81a3:59339",
    "blocking_instance_pid": 10810,
    "blocking_instance_port": 59339,
    "context_tokens": 16384,
    "current_bytes": 96648069120,
    "current_probe_ok": true,
    "fallback_to_registry": false,
    "headroom_bytes": 10737418240,
    "kv_bytes": 0,
    "kv_bytes_per_token": 0,
    "limit_bytes": 126701535232,
    "model_bytes": 64424509440,
    "projected_bytes": 161072578560,
    "registry_bytes": 72975748384,
    "total_bytes": 137438953472
  }
}
```

Post-check cleanup verified:

```
fake-file-removed
no-fake-process
```

The live process table still showed exactly one real llama-server, PID 10810, the GLM
instance. No second process was created.

## Bug found by Phase 5: reuse must precede the guard

A follow-up live probe attempted to start the already-running GLM again through
`StartRuntimeModel`, expecting reuse. The still-running agent refused it instead:

```
llama-server memory guard refused to start GLM-4.5-Air Q4_K_M:
projected non-evictable memory 163.13 GiB exceeds limit 118.00 GiB
(current 89.29 GiB, model 67.96 GiB, KV 5.88 GiB for 32768 ctx tokens,
headroom 10.00 GiB, total 128.00 GiB,
largest live instance llama_server:690083de81a3:59339 pid 10810 port 59339)
```

Root cause: `adoptLiveSibling` only considers sibling registry files; the provider's own
`p.running` reuse check was still after the guard. The fix adds an early local reuse
check inside `spawnMu`, before the reap barrier and memory guard, while preserving the
later double-check before insertion. A new unit test pins this:
`TestStart_ReusesLiveInstanceBeforeMemoryGuard`.

The live agent that produced this refusal has not been restarted since the fix, so this
specific live reuse check is verified by unit test, not by the currently running binary.

## Remaining end-to-end check

The live restart scenario is still pending. It would require terminating/restarting the
agent process that is currently serving this conversation (`/Users/bryancostanich/bin/.cercano-libexec/cercano agent`, PID 10805), so it crosses a disruption boundary and should be done only with explicit acknowledgement that the session may drop. Unit/integration tests cover the reap barrier; this evidence file records that the full live restart was not executed in-session.

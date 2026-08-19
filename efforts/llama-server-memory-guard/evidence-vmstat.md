# Phase 2 evidence — live `vm_stat` page accounting

Captured 2026-08-19 on the 128 GB machine that locked up, with one
GLM-4.5-Air llama-server running (PID 2326).

## Raw figures

```
page size (hw.pagesize)          16384 bytes
hw.memsize                       137438953472  = 128.00 GiB

Pages free                          101177
Pages active                       1305470
Pages inactive                     1298924
Pages speculative                     5386
Pages wired down                   4913635
Pages purgeable                      22890
Pages stored in compressor         1726986
Pages occupied by compressor        682369
```

## Derived, and cross-checked against `top`

| Quantity | Computed | `top -l 1` says |
|---|---|---|
| Wired | 74.98 GiB | `75G wired` |
| Compressor (occupied) | 10.41 GiB | `10G compressor` |
| **Non-evictable (wired + compressor occupied)** | **85.39 GiB** | — |
| Non-evictable as share of physical | 66.7% | — |

The computed wired and compressor figures match `top`'s independently
rendered numbers, so the page-class selection and the page-size multiply are
correct.

## Decisions this settles

1. **Use `Pages occupied by compressor`, not `Pages stored in compressor`.**
   "Stored" is 1726986 pages = 26.35 GiB, which is the *uncompressed* volume
   of data held in the compressor. "Occupied" is 682369 pages = 10.41 GiB,
   the physical RAM the compressor actually holds. Only "occupied" is real
   memory; using "stored" would over-count by ~16 GiB and cause spurious
   refusals. `top`'s agreement with the "occupied" figure confirms this.

2. **Do not sum all page classes and compare to `hw.memsize`.**
   free + active + inactive + speculative + wired + compressor-occupied =
   126.75 GiB against a `hw.memsize` of 128.00 GiB — a 1.25 GiB shortfall.
   The page classes do not tile physical memory exactly, so the probe must
   report non-evictable bytes directly and let the caller compare against
   `sysram.Total()`. It must not try to derive "available" by subtraction.

3. **Purgeable pages are excluded.** They are 22890 pages (0.35 GiB) and are
   by definition reclaimable, so they do not belong in a non-evictable total.

## Why the guard is necessary — measured, not theoretical

The single live llama-server (PID 2326) has an RSS of **77.14 GiB**. Note this
is *higher* than the 68.2 GB observed earlier in the same session: resident
size grows with KV-cache usage as the context fills, which is exactly why the
projection must include a KV term and not just on-disk weight size.

With non-evictable already at 85.39 GiB, a second server of the same size
would require **162.52 GiB against 128 GiB physical — an overshoot of
34.52 GiB.** There is no headroom whatsoever; the machine would lock up
exactly as it did on 19 Aug.

## Implementation note

`vm.page_free_count` exists as a sysctl, but the wired and compressor
counters do not have stable `vm.*` sysctl equivalents. The supported route is
the `host_statistics64` Mach call with `HOST_VM_INFO64`, which returns
`vm_statistics64_data_t` — the same struct `vm_stat` itself renders. Fields
needed: `wire_count` and `compressor_page_count`.

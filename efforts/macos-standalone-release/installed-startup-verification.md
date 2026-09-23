# Installed binary discovery — Phase 2 probe verification

Date: 2026-09-22 · Scope: bounded Phase 2 discovery/asset probes only. No production lookup code was changed (no bug found); no plan statuses changed; no agent launched; no developer live agent, credentials, or installed binaries touched. No handshake or version gating was added (none exists in `Dial`/`connect`, per the plan addendum).

## 1. Do the existing tests truly exercise executables and symlinks?

**Yes — verified by direct probe, not assumed.** A temporary probe (created, run, then deleted; no residue) copied this test binary into a fake keg, exposed it through `prefix/bin/` symlinks exactly like `brew link`, and ran the copy as a real subprocess with filtered PATH, printing `os.Executable()`, `os.Getwd()`, and `findCercanoBinary()` from inside it. Results on this host (darwin/arm64, go1.26.3):

- Invoked via `…/bin/cercano-cli` (symlink): `os.Executable()` returned the **symlink path**, and discovery returned `…/bin/cercano` — the sibling symlink — resolving to the Cellar agent.
- Invoked via `…/Cellar/cercano/1.0.0/bin/cercano-cli` (real path): `os.Executable()` returned the **Cellar path**, and discovery returned the real Cellar sibling.

So `TestBinaryDiscovery/homebrew_symlinks` (`binary_discovery_test.go`) does run a real executable through a real prefix symlink and its `os.SameFile` assertions are non-vacuous. **No path bug was observed; no production fix was made.**

## 2. Gaps found in existing coverage

1. Only the symlink invocation direction was covered; direct Cellar-path invocation (what a fully resolved executable path yields, e.g. Linux `/proc/self/exe`) was not.
2. `path_fallback` has no sibling present, so **sibling-over-PATH priority was never proven** — no test had a colocated sibling and a PATH candidate simultaneously.
3. The working directory was outside the checkout but no decoy agent lived there, so cwd-independence was unasserted.

## 3. New focused integration test

Added `source/server/pkg/agentclient/binary_discovery_installed_test.go` — `TestBinaryDiscoveryInstalledLayout` (+ subprocess-only `TestBinaryDiscoveryInstalledHelper`). One shared Homebrew-style layout (`Cellar/cercano/1.0.0/bin/` keg + `bin/` prefix symlinks + keg agent fixture), then three subtests, each running the **real copied test executable** as a subprocess that calls the production `findCercanoBinary`:

- `prefix_symlink_invocation` — CLI launched via `bin/cercano-cli` symlink; found agent must resolve (`os.SameFile`) to the keg `cercano`.
- `cellar_direct_invocation` — CLI launched via the real Cellar path; found agent must be the keg sibling. Cross-assertion: both directions resolve to the **same** keg agent file.
- `sibling_preferred_over_hostile_path` — same symlink layout plus an executable hostile `cercano` **first in PATH** (a marker script); discovery must return the keg sibling, must not resolve to the hostile file, and the marker file must remain absent (never executed).
- All modes: cwd is an isolated directory outside the checkout containing a **decoy** `cercano` (a cwd-based lookup would fail `os.SameFile` against the keg agent); helper asserts the subprocess executable is the invoked path and reports exe/wd/found, which the parent asserts against the fixtures — non-vacuous (a stale-path failure was observed firing during development before the fixture was shared).

Isolation: PATH and HOME are filtered and pointed at empty temp dirs; agent/decoy/hostile fixtures are only ever stat'ed or LookPath'd — never executed — so no agent, socket, port, or config is touched; 15 s subprocess timeout. One in-test defect was found and fixed during development (per-subtest `t.TempDir()` cleanup invalidated the cross-direction comparison; fixed by sharing one keg layout, which is also the realistic install shape). No production changes.

## 4. Exact commands and results

```
cd source/server && go vet ./pkg/agentclient
# (clean, exit 0)

cd source/server && go test ./pkg/agentclient -run TestBinaryDiscoveryInstalledLayout -count=1 -v
# PASS: prefix_symlink_invocation, cellar_direct_invocation,
#       sibling_preferred_over_hostile_path (+ parent cross-direction assert)

cd source/server && go test ./pkg/agentclient -run TestBinaryDiscovery -count=1 -v
# PASS: new 3 subtests + existing homebrew_symlinks / path_fallback / missing
# (helpers SKIP in parent as designed)

cd source/server && go test ./pkg/agentclient -count=1
# ok  cercano/source/server/pkg/agentclient  (full package green)
```

## 5. Entrypoint direction

These tests cover **terminal client → agent binary discovery**. The two invocation forms above are symlink-path versus direct-keg-path invocation, not two executable lookup directions. In this release branch, `source/server/cmd/cercano/main.go` runs the agent for bare `cercano`; it does not discover or delegate to `cercano-cli`. There is therefore no reverse discovery function to test. No entrypoint behavior was changed.

Parent verification: `go test ./pkg/agentclient -count=1` passed after inspecting the new tests and the agent entrypoint.

## 6. Limitations and remaining Phase 2 gaps

- Discovery is proven at the `findCercanoBinary` level only; the full auto-launch path (`ensureServerLaunched` / `autoLaunchServer` / launch lock / `waitForPort`) was **not** exercised — that needs an isolated agent subprocess with isolated HOME/config/socket/ports and is not covered here.
- No live `brew install` against a real Homebrew prefix/keg was performed; layout is a faithful fixture, not a real bottle install (matches release-audit §"Non-blockers").
- Darwin-only evidence for the symlink `os.Executable()` direction (Linux resolves `/proc/self/exe` to the real path; the `cellar_direct` subtest models that direction on any host).
- Still open in the plan (untouched by this bounded work): compatible/incompatible already-running agent coverage, restart guidance, self-update vs Homebrew-managed install conflict, and config/conversation data preservation across replacement.

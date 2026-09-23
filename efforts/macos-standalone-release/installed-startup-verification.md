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

## 6. Running-agent reuse (non-disruptive upgrade contract)

`source/server/pkg/agentclient/installed_reuse_test.go` — `TestInstalledAgentReuse` exercises the production auto-launch decision (`ensureServerLaunched`) from a real copied client invoked through a Homebrew-style prefix symlink, in two directions:

- `running_agent_is_reused` — a real gRPC listener on an ephemeral loopback port stands in for the running agent. Production reports `launched=false`, no spawn log, no error, and the installed agent fixture is **never executed** (marker file absent). This is the upgrade contract: a replaced client attaches to the agent already running rather than starting a competing one. No version gate is consulted; none exists and none was added.
- `absent_agent_is_launched` — control. With nothing listening, the same fixture **is** executed (marker file appears), proving the reuse assertion above cannot pass from a dead fixture. The reported `port did not become listenable within 2s` error is expected: the fixture deliberately never binds.

Isolation: the subprocess runs with `TMPDIR` pointed at a private directory, so the auto-launch flock (`cercano-agent-launch.lock`) and the spawn log are isolated from the developer's live agent; PATH and HOME are filtered and emptied. Confirmed after the run that the shared `$TMPDIR` lock was untouched (mtime unchanged, prior day) and the only spawn log written by the test was inside the isolated directory. The sole executable that can run is a throwaway shell fixture; no real agent was started, signaled, or connected to.

Parent verification: `go vet ./pkg/agentclient` clean; `go test ./pkg/agentclient -run TestInstalledAgentReuse -count=1 -v` PASS (both directions); `go test ./pkg/agentclient -count=1` ok (full package).

## 7. User data preserved across binary replacement

`source/server/internal/conversation/upgrade_preservation_test.go` — `TestUpgradePreservesUserData` proves the data-safety half of the upgrade contract. It seeds a real conversation (two turns) and a `config.yaml` at the **production-resolved** location under an isolated HOME, then performs a realistic bottle upgrade: new files written to a new version keg, prefix symlinks repointed, old keg deleted. Replacement is asserted to be genuine (different inodes via `os.SameFile`, old keg gone) before checking that `DefaultPath` still resolves to the same database and that the conversation, its turn contents, its project listing, and the config bytes all survive unchanged.

Non-vacuity: a temporary mutation probe that deleted the database and config after the upgrade made the test fail with `conversation lost across upgrade: sql: no rows in result set`. The probe was then removed; the passing result reflects preservation, not a missing assertion. An in-test bug was also found and fixed during development — the HOME-containment guard treated the leading dot of `.config` as an escape; it now detects `..` traversal instead.

Isolation: HOME is redirected with `t.Setenv` and `CERCANO_CONVERSATIONS_DB` is explicitly cleared, so an ambient developer override cannot redirect the test onto the real database. Confirmed afterwards that the developer's real `~/.config/cercano/config.yaml` was untouched. The "binaries" are inert text fixtures, never executed; no agent was started.

Note this establishes user data lives outside the install prefix and survives replacement. It does not exercise a live agent writing during an upgrade; that belongs to the Phase 6 clean-Mac rehearsal.

Parent verification: `gofmt -l` clean; `go vet ./internal/conversation ./pkg/agentclient` clean; `go test ./internal/conversation -count=1` ok.

## 8. Limitations and remaining Phase 2 gaps

- Discovery is proven at the `findCercanoBinary` level only; the full auto-launch path (`ensureServerLaunched` / `autoLaunchServer` / launch lock / `waitForPort`) was **not** exercised — that needs an isolated agent subprocess with isolated HOME/config/socket/ports and is not covered here.
- No live `brew install` against a real Homebrew prefix/keg was performed; layout is a faithful fixture, not a real bottle install (matches release-audit §"Non-blockers").
- Darwin-only evidence for the symlink `os.Executable()` direction (Linux resolves `/proc/self/exe` to the real path; the `cellar_direct` subtest models that direction on any host).
- Still open in the plan (untouched by this bounded work): compatible/incompatible already-running agent coverage, restart guidance, self-update vs Homebrew-managed install conflict, and config/conversation data preservation across replacement.

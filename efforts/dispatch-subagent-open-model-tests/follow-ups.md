# Dispatch test work queue

## Queued: findings quality and continuation coverage

Agreed follow-up after the local native-history fix (`ccc4c22c`); not implemented yet.

- [ ] Opt-in live dependent-read test: generated file A names file B; only B contains the answer. Assert successive tool calls and the exact answer.
- [ ] Opt-in live citation test: generated fixture with a unique fact on a known line; assert filename, line number, and quoted content against the fixture.
- [ ] Opt-in live repeated-dispatch isolation test: distinct generated tokens for successive dispatches; assert each response contains its own answer, not prior findings.
- [ ] Deterministic fallback content-fidelity test: multiple tool results retain exact content, identifiers, and order across provider switch; completed tools are not replayed.

## Queued: opt-in live malformed-call recovery (Bash argv contract)

Agreed follow-up after the Bash argv contract/diagnostic fix (argv semantics documented in `run.go` Description and `cmd` schema description; executable-not-found errors now append an actionable argv/explicit-shell hint while non-zero exits and permission failures stay unlabeled). Not implemented — do not build a new live harness.

- [ ] Opt-in live malformed-call recovery test: dispatch a sub-agent with a task that tempts a whole-command `cmd` value (e.g. a directory listing); after the hinted failure, assert the next Bash call arrives as proper argv (`["ls", "/path"]` or an explicit `["bash", "-lc", ...]`) and the iteration ultimately succeeds. Use the existing opt-in live harness; deterministic coverage of the hint itself lives in `run_test.go` (whole-command string, list-of-commands, missing path, explicit shell, spaces-in-path, non-mislabeled failures).

Use the existing opt-in live harness and deterministic test suites. No new framework or nested-agent coverage is required. Live smoke findings were substantively correct but their line references were inaccurate.

## Investigated: Lunie dispatch Bash argument failure

Dispatch `c090721b6cc1ea8754993cde`, parent conversation `fb8813af1e0f9a38` (LUNIE - BLURRY TEXTURE FIXES). Failure recorded at 2026-09-19 03:40:53 UTC.

- Route: cloud OpenAI-compatible provider, `zai-org/GLM-5.3`; native history (`flatten_tool_results=false`). Not the earlier local flattened-history failure.
- Iteration 1: two Bash calls, each passing an entire command as the sole `cmd` element (`find /... -name clipmap_ring.rs` and `ls /...`). Both fail before a process starts.
- Iteration 2: another one-element `ls /...` command, again failing executable lookup. Persisted reasoning incorrectly interprets the error as a missing working directory.
- Iteration 3: `cmd` is an array of separate shell command strings beginning with `pwd && ls ..`; executable lookup fails again.
- Bash executes `exec.CommandContext(runCtx, a.Cmd[0], a.Cmd[1:]...)`, not an implicit shell. The array is one executable plus arguments, not a list of commands.
- Direct read-only probe reproduced the exact error with the original malformed `ls` call. Splitting it into `["ls", "/..."]` succeeded in the same working directory.
- Four tool errors across three consecutive all-error iterations trigger the existing abort guard. This worker never reached tests, build, edits, or checkpoint.

Contributing interface ambiguity: the tool is named Bash and described as running a shell command, but its schema provides no `cmd` property description or concrete examples explaining direct argv execution. The raw executable-not-found error does not explain how to correct this misuse.

Follow-up (implemented): clarify argv versus explicit shell invocation in the tool contract, add an actionable diagnostic for this failure without automatically interpreting shell syntax, and cover malformed-call recovery in deterministic tests; the opt-in live recovery test is queued above. Support for legitimate executable paths containing spaces is preserved and covered by a deterministic test. Dispatch history, fallback, and abort-guard behavior were not changed.

Evidence: read-only queries of persisted conversation turns, server log and failures.jsonl; source inspection of `internal/capabilities/builtins/run.go` and `internal/agent/toolloop.go`.

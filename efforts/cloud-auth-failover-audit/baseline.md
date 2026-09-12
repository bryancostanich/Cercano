# Execution baseline

## Setup

The implementation worktree is `/Users/bryancostanich/git_repos/bryan_costanich/Cercano-cloud-auth-recovery`, branch `fix/cloud-auth-recovery`. The approved specification and plan were copied into this worktree. Existing main-checkout changes were not stashed, reset, or modified by setup.

## Directly verified tests

From `source/server`:

```text
go test ./internal/anthropicauth ./internal/chatgptauth ./internal/llm/anthropic ./internal/llm/responses -count=1
ok cercano/source/server/internal/anthropicauth 0.390s
ok cercano/source/server/internal/chatgptauth 0.456s
ok cercano/source/server/internal/llm/anthropic 3.072s
ok cercano/source/server/internal/llm/responses 0.719s
```

These are the existing package tests, not the new recovery regression matrix. Passing them does not verify the proposed behavior. No actual provider credentials were used.

## Execution tooling issue

Bounded delegated tasks at both standard and deep tiers returned only tool-use summaries rather than results. The classification task did not produce its requested evidence file or implementation changes; the follow-up baseline task did not produce its requested report. The parent ran the baseline test command directly and recorded its actual output above. Delegated output must not be counted as completed implementation or verification.

No implementation files have been changed by this work. Phase 1 regression development and all repair phases remain incomplete. Do not treat this baseline as completion of Phase 1.

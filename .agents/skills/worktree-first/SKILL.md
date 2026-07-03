---
name: worktree-first
description: Isolate every feature branch in its own worktree; never share the root workspace.
---

# Worktree-First Protocol

## When This Applies

Any time you're about to start a new feature or fix that needs its own
branch. If your next step is `git checkout -b <newbranch>` in a shared
workspace, this protocol is mandatory.

## The Rule

**Create a worktree; never share the root workspace.** The root worktree
(the top-level repository directory) is a shared physical checkout —
multiple concurrent sessions read from it and write to it. If you check
out a feature branch in the root, every commit any other session makes
in the root goes onto **your** branch, not the trunk.

## Steps

1. **Use the `git_worktree` tool.** Not raw `git checkout -b`, not
   `git worktree add` from the shell — the tool wraps both and adds the
   safety checks (clean baseline, target dir git-ignored, trunk
   resolution). Pass:
   - `path`: `../<repo-name>-<feature-slug>` — a **sibling**
     directory to the repo root, not a subdirectory of it. Sibling paths
     avoid gitlink-submodule confusion (git treats a linked-worktree
     directory *inside* the tracked tree as a submodule pointer, which
     creates persistent `M` entries in `git status` on
     the root as the worktree's HEAD advances). Example: for a repo at
     `/git_repos/foo/Cercano`, a good worktree path is
     `/git_repos/foo/Cercano-runtime-dashboard`.
   - `branch`: `feat/<feature-slug>` or `fix/<feature-slug>`.
   - `trunk`: the target trunk (usually `main`).

2. **Do all work inside the worktree.** Every `cd`, test run, edit, and
   commit lives in the isolated directory. The root stays on trunk,
   untouched.

3. **Do not `git checkout <branch>` in the root worktree** while your
   feature is active. If you need to look at trunk state, do it in the
   root (which is already on trunk). If you need to look at your branch,
   do it in the worktree.

## Why This Exists

The root worktree is the default directory other sessions operate in
when they touch the repo. If your feature branch is checked out there:

- Other agents committing "quick fixes" for unrelated tracks land on
  your branch instead of trunk.
- Any nested worktree left inside the tracked tree gets recorded as a
  submodule pointer whose HEAD keeps drifting — showing up as a
  persistent conflict every time you rebase. The sibling convention
  above sidesteps this entirely.
- Rebasing back onto trunk multiplies conflicts by every stowaway
  commit — each one carries its own worktree-pointer noise that has to
  be reconciled per commit.
- Rolling back is dangerous — other sessions may still be reading the
  checked-out working tree.

A worktree is one tool call to create with `git_worktree`. It isolates
all of this at zero ongoing cost. Skipping the worktree to save one step
now trades minutes for hours of rebase conflict resolution later.

## What This Prevents

- Concurrent-modification hazards between agent sessions sharing the root
- Submodule-pointer conflicts on nested-worktree paths you never touched
  (avoided entirely by the sibling-directory convention above)
- Unrelated commits from other sessions accumulating on your feature
  branch
- "How did I end up with 14 commits I don't recognize?" investigations
  mid-rebase

## Fast Path for Trivial Changes

For genuinely trivial changes — a typo, a label rename, a comment fix,
a one-line copy tweak — the worktree ceremony is disproportionate. The
fast path is:

- Applies to changes ≤ 5 lines across ≤ 2 files with no logic changes.
- **Create a feature branch in the root** with `git checkout -b fix/<slug>`.
  The worktree-first watchdog check is expected to fire here; overriding
  it is the fast-path escape valve, and the user is asked to authorize.
- **Stage only your intended files** with an explicit path list.
  Root may have other sessions' in-progress edits — never `git add -A`.
- **Commit via the checkpoint tool.**
- **Land via `git_land`.** The test gate and safety refs still run —
  the fast path saves worktree ceremony, not the safety guarantees.
- **Delete the branch** after landing.

The fast path is deliberately narrow. Anything with a logic change, a
new file, or touching more than a couple of files goes through the
full worktree flow. When in doubt: use the worktree.


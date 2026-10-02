---
name: delegate
description: Delegate exactly one bounded, well-specified coding run to a configured execution adapter in a clean linked Git worktree, then have Codex review the resulting local commit. Use for a single one-off implementation with no review loop or task store. For multi-role delivery (develop, review, fix, polish, recheck) of prepared tasks use the workflow skill instead. Not for pushing, deploying, multi-task orchestration, or exploratory work.
---

# Meerkat delegation

Codex owns setup and review; one execution run owns implementation, local checks, and one scoped local commit.

**Execution adapter.** The run is performed by the execution adapter configured in the profile. Only the Pi adapter is currently implemented: `scripts/run.mjs` spawns the `pi` CLI (profile `piCommand`, default `["pi"]`) and reads its JSON events. No other executor exists yet; do not promise one.

## When

- The task is concrete enough for a single run with an observable result.
- An execution profile exists (e.g. `projects/<project>.json`) and the environment variable named by its `authEnv` is set.

## How

1. Inspect attached worktrees and reuse a suitable free one; otherwise create one on a task branch from the intended base (never `main`, never the primary checkout) with the managed `create_worktree` tool when available. Use shell `git worktree add -b codex/<purpose> <path> <base>` only if that tool is unavailable.
2. Write the task to a file outside the worktree: goal, scope, non-scope, acceptance checks.
3. Dry-run (no key, no API): `node <plugin>/scripts/run.mjs --config <plugin>/projects/<project>.json --worktree <path> --task <file> --dry-run`
4. Run the same command without `--dry-run`. The script starts one Pi run; it does not retry, push, or deploy.
5. Read the printed summary (also in `<worktree>/.pi-developer/runs/`; the directory name is a compatibility name only). Exit 0 = the run settled, exited 0, committed, clean tree; 1 = failed/stopped (changes preserved); 2 = preflight/usage error.
6. Review `git diff <baselineSha>..<resultSha>` and rerun checks yourself. Fix, delegate again with a new task, or reject. Push/merge/deploy need separate user authorization.

Never print, log, or pass the API key on the command line.

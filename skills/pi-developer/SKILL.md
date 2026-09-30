---
name: pi-developer
description: Delegate one bounded, well-specified coding task to the Pi CLI running in a clean linked Git worktree, then review its local commit. Use when the user asks to have Pi (or a cheaper model) implement a task while Codex reviews. Not for pushing, deploying, multi-task orchestration, or exploratory work.
---

# Pi developer

Codex owns setup and review; Pi owns implementation, local checks, and one scoped local commit.

## When

- The task is concrete enough for a single run with an observable result.
- A project config exists (e.g. `projects/example-project.json`) and its `authEnv` key is set in the environment.

## How

1. Inspect attached worktrees and reuse a suitable free one; otherwise create one on a task branch from the intended base (never `main`, never the primary checkout) with the managed `create_worktree` tool when available. Use shell `git worktree add -b codex/<purpose> <path> <base>` only if that tool is unavailable.
2. Write the task to a file outside the worktree: goal, scope, non-scope, acceptance checks.
3. Dry-run (no key, no API): `node <plugin>/scripts/run.mjs --config <plugin>/projects/<project>.json --worktree <path> --task <file> --dry-run`
4. Run the same command without `--dry-run`. The script calls Pi once; it does not retry, commit, push, or deploy.
5. Read the printed summary (also in `<worktree>/.pi-developer/runs/`). Exit 0 = Pi settled, exited 0, committed, clean tree; 1 = failed/stopped (changes preserved); 2 = preflight/usage error.
6. Review `git diff <baselineSha>..<resultSha>` and rerun checks yourself. Fix, rerun Pi with a new task, or reject. Push/merge/deploy need separate user authorization.

Never print, log, or pass the API key on the command line.

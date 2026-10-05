# Checkpoints and explicit continuation

A confirmed budget stop or graceful pause can preserve incomplete developer or polisher work as a
private checkpoint. The task becomes `paused`; the Run remains `stopped`. No
candidate or passed review is created from incomplete work.

## Continue a saved task

```sh
node scripts/launch.mjs execute --task TASK_ID --resume
```

For a task created by developer-only `run`, continue with
`node scripts/launch.mjs run --task TASK_ID --resume` instead. It keeps the same
Task, Session, checkpoint and charged usage and returns an unreviewed local
candidate. `execute` and `dispatch` remain the full workflow entry points.

The asynchronous equivalent is `dispatch --task TASK_ID --request-id NEW_UUID
--resume`, or MCP `dispatch_tasks` with `resume: true`. Reuse the request UUID
only for the same dispatch receipt, not a later continuation.

Go checks the same frozen contract, Profile, role, persistent session, HEAD,
branch, linked worktree, staged changes and every changed/untracked file's
contents and mode. Pi verifies the worktree fingerprint again before a prompt.
The checkpoint is consumed atomically with the new Run's session claim, and the
original task's token/time use remains charged. Resume does not add allowance.
An exhausted allowance requires a separate [authorized decision](budget-decisions.md).

Changed, missing or extra files, a changed index, unverified commit, modified
session/history or exhausted allowance require the coordinator. The existing
worktree and checkpoint archive are preserved. Meerkat never automatically
resets Git or overwrites files to make a checkpoint match.

## When saving is allowed

- The owned executor confirms idle shutdown and its process group is gone.
- Tool starts/ends match and no failed or unresolved tool operation was observed.
- Request usage is known; an unknown request result does not become a checkpoint.
- The branch remains unchanged. HEAD is the role baseline or exactly one
  provisional commit descended from it, with all committed paths in scope.
  Only in-scope regular files are captured; merge conflicts, symlinks and
  oversized captures are refused.
- There was a token/time stop, an explicit graceful pause or budget wrap-up
  followed by incomplete local work. Immediate cancellation and controller
  crashes do not certify progress.

The archive records exact working bytes, file deletions/modes and the staged
binary patch. Private SQLite records bind it to the task, Profile, Session, Run,
usage and event position. Full execution history stays in the private Session.

From beta.15, checkpoints retain the original role baseline separately from the
saved execution HEAD. Continuation starts at that exact HEAD. Final scope and
the one-commit rule still cover the complete role from its original baseline.
A clean provisional commit can be reported without an extra commit; incomplete
changes amend that unreviewed commit. Two commits, unrelated ancestry or an
out-of-scope commit are refused. A changed polisher result requires a fresh review.
Earlier runtimes cannot read these distinct-SHA checkpoints; use beta.15 or later
to read or restore their backups. Existing equal-SHA checkpoints remain readable.
Unreported completed/remaining steps or checks stay unknown; the saved files
alone do not establish that a requirement has been met. Successful local tools
do not certify remote or background effects.

Limits: 512 changed files, 8 MiB per file, 32 MiB of working bytes and a 48 MiB
archive. Ignored untracked files are excluded. This implements verification and
continuation in the original worktree, not automatic reconstruction in a new one.

## History, backup and display

Checkpoints were introduced in SQLite V6; V7 adds independent budget decisions
and V8 adds [verified completed-step recovery](session-recovery.md), preserving
existing history. Saved and consumed checkpoint archives
are bundled into consistent backups and materialized only into a new private data
directory on restore. A missing/corrupt archive rejects backup or restore. A
resumed Run that is interrupted after its one-time claim remains unknown after a
restart; the consumed checkpoint is never replayed automatically.

The Tasks detail shows the checkpoint ID, role, HEAD, file count, saved/consumed
state and resumed Run. No file contents, private paths, provider session IDs or
authority digests are included. Codex settings remain read-only; bounded human
Run controls use the standard MCP host tools.

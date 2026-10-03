# Checkpoints and explicit continuation

A confirmed budget stop can preserve incomplete developer or polisher work as a
private checkpoint. The task becomes `paused`; the Run remains `stopped`. No
candidate or passed review is created from incomplete work.

## Continue a saved task

```sh
node scripts/launch.mjs execute --task TASK_ID --resume
```

The asynchronous equivalent is `dispatch --task TASK_ID --request-id NEW_UUID
--resume`, or MCP `dispatch_tasks` with `resume: true`. Reuse the request UUID
only for the same dispatch receipt, not a later continuation.

Go checks the same frozen contract, Profile, role, persistent session, HEAD,
branch, linked worktree, staged changes and every changed/untracked file's
contents and mode. Pi verifies the worktree fingerprint again before a prompt.
The checkpoint is consumed atomically with the new Run's session claim, and the
original task's token/time use remains charged. Resume does not add allowance.

Changed, missing or extra files, a changed index, unverified commit, modified
session/history or exhausted allowance require the coordinator. The existing
worktree and checkpoint archive are preserved. Meerkat never automatically
resets Git or overwrites files to make a checkpoint match.

## When saving is allowed

- The owned executor confirms idle shutdown and its process group is gone.
- Tool starts/ends match and no failed or unresolved tool operation was observed.
- Request usage is known; an unknown request result does not become a checkpoint.
- The role's starting HEAD and branch remain unchanged. Only in-scope regular
  files are included. Merge conflicts, symlinks and oversized captures are refused.
- There was a token/time stop or an explicit budget wrap-up followed by incomplete
  local work. User cancellation and controller crashes do not certify progress.

The archive records exact working bytes, file deletions/modes and the staged
binary patch. Private SQLite records bind it to the task, Profile, Session, Run,
usage and event position. Full execution history stays in the private Session.
Unreported completed/remaining steps or checks stay unknown; the saved files
alone do not establish that a requirement has been met. Successful local tools
do not certify remote or background effects.

Limits: 512 changed files, 8 MiB per file, 32 MiB of working bytes and a 48 MiB
archive. Ignored untracked files are excluded. This implements verification and
continuation in the original worktree, not automatic reconstruction in a new one.

## History, backup and display

SQLite V6 preserves existing V1–V5 records. Saved and consumed checkpoint archives
are bundled into consistent backups and materialized only into a new private data
directory on restore. A missing/corrupt archive rejects backup or restore. A
resumed Run that is interrupted after its one-time claim remains unknown after a
restart; the consumed checkpoint is never replayed automatically.

The Tasks detail shows the checkpoint ID, role, HEAD, file count, saved/consumed
state and resumed Run. No file contents, private paths, provider session IDs or
authority digests are included. The Codex monitor stays read-only.

# Verified worktree checkpoints

Date: 2026-10-03 (Asia/Shanghai). Baseline: `ae751c6`.

## Frozen implementation contract

Goal: preserve incomplete local coding work after a confirmed budget stop and
explicitly continue the same task and execution history without resetting Git.
Scope: checkpoint capture/verification, SQLite V6 records and backup/restore,
core resume validation, Pi's verified dirty-start binding, public checkpoint
summaries, monitor labels, tests, workflow instructions and delivery documents.
Implementation is local; no paid provider requests, coding subagents or remote
Issue updates. GitHub push remains authorized by the user.

Capture is allowed only for developer/polisher work when the owned executor has
confirmed idle shutdown, no unresolved tool execution, known request settlement,
an unchanged role-start SHA/branch and only in-scope regular-file changes. A
checkpoint stores exact staged changes, file contents/modes/deletions, linked
worktree identity, frozen task/profile/session digests, run/usage references and
the last event sequence. It does not imply a candidate commit or passed checks.
Unknown completed/remaining work stays unknown when no valid report exists.

Private files are written and synced before an atomic database reference and
paused task state. A crash before the database commit leaves an unreferenced
private file; a crash after resume claims a checkpoint never replays the run.
Existing uncertain runs remain unknown and are not promoted to checkpoints.

Resume requires an explicit existing `--resume` request, positive remaining
original allowance and the same role contract, context, profile, session bytes,
HEAD, branch, index, changed/untracked files and worktree identity. Capture and
comparison run outside SQL; ownership and one-time checkpoint consumption are
atomic with the new run. Pi independently verifies the dirty-start fingerprint
before submitting any prompt. Other tasks cannot adopt the saved worktree.

This step does not add budget, restore files over a modified worktree, certify
external side effects, salvage an unverified commit or resolve unknown history.
Such cases preserve evidence and require the coordinator. Model/provider changes
still require a new frozen contract. Reviewer resumes retain independent review.

Acceptance: unchanged dirty resume through final reviewed delivery; missing,
extra or changed files/index/HEAD/branch/context/profile/session rejected; token
history retained; one-time ownership; crash uncertainty; backup/restore of saved
and consumed checkpoints; corrupt file/bundle rejection; private projections;
actual Pi local-protocol checks without a paid model; monitor and skill wording
matches implemented behavior. Native changed monitor surfaces need host evidence.

## Delivery evidence

Implementation version: `0.4.0-beta.3`. SQLite schema: V6. The original task
allowance remains authoritative; adding budget is a separate planned step.

- All Go packages passed `go test -race ./... -count=1`, including the installed
  Pi test using an isolated loopback model endpoint. After final checkpoint and
  embedded UI changes, checkpoint, MCP, web and server race checks were rerun.
- 19 frontend tests, TypeScript checking, all three Vite output builds and 64
  installation/distribution tests passed. `go vet ./...`, `git diff --check`
  and the workflow skill validator passed.
- Tests cover exact staged/unstaged/untracked continuation, original cumulative
  allowance, worktree ownership, changed contract/history rejection, saved and
  consumed backup/restore, corrupt archive refusal, a crash after one-time claim,
  exhausted allowance and mandatory review after resumed polish.
- Installed Pi 0.99.1 wrote local changes, settled after the local request-budget
  denial, resumed its same persistent history and made one clean local commit.
  This verifies the real CLI/protocol/tool chain; its model replies and usage are
  synthetic. Paid provider execution remains paused at the user's request.

Existing native Codex monitor acceptance is recorded in
`docs/codex-ui-acceptance.md`. The added checkpoint detail still needs a refreshed
native host view; frontend/resource checks do not replace that evidence.

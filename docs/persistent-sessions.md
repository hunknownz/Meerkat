# Persistent execution sessions

Implemented in the Go service. Install the matching runtime before using these capabilities.

The Go scheduler automatically binds each role Run to a private Session when its executor advertises persistent sessions. Pi is the implemented adapter. There is no additional CLI flag and no perpetual idle Pi process: each Run opens a verified history through RPC, finishes or stops, then shuts down and reaps its child process.

## History and review

Development and fixes for the same task reuse one verified history. Polish uses its own history. Every review starts a fresh session, including reviews after a fix or a changed polish candidate.

Reuse requires the same frozen task, Context, Profile, executor, worktree and history digest. The SHA must match the session's last verified version or advance through a continuous chain of this task's successful, verified runs. An unrelated commit, a changed model or modified session file is rejected.

The adapter checks Pi's selected provider/model, session identity, file and idle state before submitting one prompt. Prompts travel through private stdin rather than process arguments. A prompt receipt is acceptance; `agent_settled`, idle state, process exit, report and Git checks determine the respective execution and delivery outcomes.

## Stop and uncertainty

The adapter clears queued messages, aborts, checks for idle, closes stdin and waits for exit. Its fallback signals only the child process group owned by that Run. A missing prompt receipt is never retried blindly.

Unconfirmed session outcomes and session claims left running after a service restart become `unknown`. They cannot be reused merely by adding `--acknowledge-interruption`. The history and ownership evidence remain available for investigation. No automatic reconciliation or replay of unknown sessions is implemented yet.

This implementation still requires a clean worktree to start. It preserves execution history; it does not yet authorize resuming uncommitted changes. Token/time limits can stop a Run. [Request budget reservations and settlement](request-budgets.md) are implemented for the supported Pi HTTP bridge. Automatic wrap-up and checkpoint-based recovery remain subsequent work in the [complete design](../design/session-budget-coordination-20261003.md).

## Storage and usage

SQLite V4 records Session identity, frozen contract, file reference/digest, verified SHA, state and Run bindings. History files live under the private data directory at `sessions/<id>/history.jsonl`, with directories `0700` and files `0600`. The Pi adapter accepts the verified v3 session format; unknown formats are rejected.

Public snapshots, MCP results and default metric exports exclude raw history and private session paths. Ledger-backed Run usage comes from raw request settlement; other executor runs retain current event accounting. Pi's historical session total is not added again. Missing token fields and prices remain unknown. Pi-reported cost is an estimate, not a confirmed bill.

Backups include idle and unknown session files. Idle files must match their confirmed digest. Unknown files preserve current bytes without declaring their session recoverable. Backups containing a running session are refused and partial output is removed. Wait until active sessions settle before creating a backup. Restore writes histories into a new private data directory and retains unknown states. Supported V1–V4 stores and backups migrate to V5, retaining session and request-budget history.

## Verification scope

Real local child-process fixtures exercise RPC, Git delivery, reuse, current usage, stop ordering, caller cancellation, token-limit stopping, missing receipts and private data filtering. Scheduler tests exercise independent review, fixes after polish and restart uncertainty. Storage tests exercise ownership, migrations, private file backup/restore and corruption rejection.

The opt-in installed Pi 0.99.1 probe reopens a seeded history twice in offline mode without submitting a model prompt. It verifies protocol and file compatibility. Real model coding, budget pause/resume and native Codex display still require their separate integration acceptance.

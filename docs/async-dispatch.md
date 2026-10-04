# Asynchronous dispatch

Implemented in the Go service. Install the matching runtime before using these commands.

Prepare a frozen task with its goal, paths, acceptance, Context, profiles, baseline, dependencies and budget. The coordinator prepares the linked worktree. Then submit:

```sh
bin/meerkat dispatch --task <task-id> --request-id <uuid>
bin/meerkat operation --operation <operation-id> --wait-ms 1000
```

`dispatch` returns after SQLite commits an operation and its task claims. Exit 0 means accepted. Inspect each member's `taskState`, checks and candidate SHA for actual delivery; operation `completed` alone is not code acceptance.

All requests share one Go dispatcher and its concurrency limit. Distinct worktrees may run in parallel. Tasks on one worktree are serialized. Task order is part of the request: reversing it with the same request ID is an input conflict. A downstream task requires a delivered dependency SHA that is actually available in its worktree. Meerkat does not merge dependencies.

## Receipts and waiting

Keep the request UUID with the task record. Identical requests return the original operation; different input under that UUID is rejected. After a lost reply, query without starting more work:

```sh
bin/meerkat operation --request-id <original-request-uuid>
```

Closing the CLI or MCP connection, canceling a wait, and reaching a wait deadline do not stop a task. CLI waits are limited to 30 seconds and return the current state on timeout. Stop a known run explicitly:

```sh
bin/meerkat stop --run <run-id> --request-id <stop-request-uuid>
```

Stop acceptance and confirmed stopping remain separate. There is no operation-cancellation command for never-started tasks in this implementation.

## Recovery

The service records member start before invoking the executor. On restart, a dispatch that started but lacks a settled result becomes `unknown`; it is never automatically replayed. Original run identity and dirty files remain available for investigation. A queued dispatch that never started may continue after frozen-contract checks. A changed queued contract prevents recovery.

For paused tasks, explicitly `dispatch --resume` with a new request UUID; a verified [checkpoint](checkpoints.md) may authorize the exact saved dirty worktree. Failed/stopped tasks without a checkpoint still require a clean worktree at the recorded SHA. An unknown persistent session requires [verified completion recovery](session-recovery.md) when eligible; `--acknowledge-interruption` does not repair or certify its history. Recovery restores one completed step and never resumes the original operation automatically. A separate new dispatch continues the task; the original unknown operation remains historical evidence.

Future profile defaults do not replace prepared task profiles. The fix-round setting is captured at submission; changing it later does not expand the operation. Selected hard task token caps and time limits still apply. Dynamic concurrency changes affect scheduling and do not terminate already running work.

## MCP and storage

The model control tools are `dispatch_tasks`, `get_operation` and `wait_operation`. `get_operation` accepts one operation ID or one request ID. MCP waits are at most one second, keeping the protocol loop available for subsequent tools. The MCP App supports bounded human instructions and stopping; settings remain read-only. The adapter forwards commands through the private socket and never owns a second scheduler or writes SQLite directly.

Operations and task memberships introduced in SQLite V3 retain a unique claim for each queued/running task. V4 adds [private execution sessions](persistent-sessions.md), and V5 adds [request budget authorization](request-budgets.md). Older stores and supported backups migrate without rewriting historical records. Backups preserve request matching, memberships and nullable usage; inconsistent payload/index records are rejected before restore.

This is the dispatch part of the [complete session and budget design](../design/session-budget-coordination-20261003.md). Persistent sessions, request reservations, early wrap-up, verified checkpoints, [explicit budget decisions](budget-decisions.md), verified completion recovery and the standard Codex monitor are implemented separately. Human instruction receipts are implemented; investigation of interruptions without verified completion evidence still requires the coordinator.

## Project and Provider task caps

`settings` accepts `projectConcurrency` and `providerConcurrency` maps of registered
IDs to limits 1–4. The global `maxConcurrency` is still the overall ceiling.
Omitting a map preserves it; supplying `{}` clears it; a supplied map replaces that
map. For example:

```json
{"maxConcurrency":3,"projectConcurrency":{"example":1},"providerConcurrency":{"gateway":2}}
```

Save through `meerkat settings --input /private/settings.json` or the loopback
browser settings dialog. Codex MCP settings are read-only in the panel; the
coordinator can use the authorized `update_settings` tool. Invalid, duplicate or
unknown IDs are rejected before writing. Lowering caps never cancels active work.

These are concurrent **task** limits, not requests-per-second or account quotas.
A task reserves all Providers in its frozen role profiles until it settles, so
later roles cannot overtake a Provider cap. Other eligible projects or Providers
may bypass a saturated queue member. Magpie still owns routing among API keys.

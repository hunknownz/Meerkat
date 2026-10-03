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

For failed or stopped tasks, inspect the cause, files and recorded SHA before an explicit `dispatch --resume`. Unknown runs additionally require `--acknowledge-interruption` after verifying the recorded process is gone. Dirty worktree recovery through checkpoints is pending; it is not enabled by this queue.

Future profile defaults do not replace prepared task profiles. The fix-round setting is captured at submission; changing it later does not expand the operation. The task's token and time limits still apply. Dynamic concurrency changes affect scheduling and do not terminate already running work.

## MCP and storage

The model control tools are `dispatch_tasks`, `get_operation` and `wait_operation`. `get_operation` accepts one operation ID or one request ID. MCP waits are at most one second, keeping the protocol loop available for subsequent tools. The MCP App monitor remains read-only. The adapter forwards commands through the private socket and never owns a second scheduler or writes SQLite directly.

Operations and task memberships introduced in SQLite V3 retain a unique claim for each queued/running task. V4 adds [private execution sessions](persistent-sessions.md), and V5 adds [request budget authorization](request-budgets.md). Older stores and supported backups migrate without rewriting historical records. Backups preserve request matching, memberships and nullable usage; inconsistent payload/index records are rejected before restore.

This is the dispatch part of the [complete session and budget design](../design/session-budget-coordination-20261003.md). Persistent Pi sessions and request budget reservations are implemented separately. Automatic wrap-up, checkpoints, message controls, interface extensions and native-host acceptance remain unfinished work packages.

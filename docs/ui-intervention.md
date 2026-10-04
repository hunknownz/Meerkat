# Human intervention

In **Agents**, expand a running Agent. The inline **干预当前 Agent** section lets
you send a direction or stop the Run. No terminal is needed.

## Sending a direction

The current adapter supports instructions during development and polish when
the controller owns the exact Run and verified Session. You can correct a
direction, clarify an acceptance condition or request a smaller next step.
Instructions do not edit the frozen task, scope, profiles or allowance. A change
outside that contract needs a separate task prepared by the coordinator.

The service saves the bounded text (up to 4000 codepoints / 16000 UTF-8 bytes)
in private SQLite storage before sending it to the same execution session.
Pi queues it after the current tool calls, before the next model request.
Subsequent calls retain the same task usage and original limits.

## Reading the result

| UI state | Meaning |
|---|---|
| 已保存，等待发送 | The service durably recorded the instruction. |
| 正在发送 | Sending began; no protocol result is recorded yet. |
| 已排队，等待 Agent 处理 | Pi acknowledged queueing. |
| 执行器已接收 | Pi acknowledged handling. |
| 发送结果未知 | The reply could not be confirmed. Query the original UUID. |
| 指令已拒绝 | The service or executor refused it. |

Queueing and handling are protocol receipts. Neither proves that a requested
code change was completed. Review the resulting candidate and checks.

The UI creates a UUID once for each click and stores only that ID in session
storage for recovery. It does not automatically retry a timed-out send. Closing
or reloading the panel does not stop the Run. **查询回执** reads without sending
again. Unknown receipts stay unknown after service interruption; no automatic
replay occurs. The Tasks detail view also retains public control receipts.

**停止运行** records a separate stop request, clears pending Pi work and aborts
through the existing process lifecycle. Accepted means recorded; inspect the
actual Run outcome before treating the process as stopped.

## Codex and browser transports

The MCP Apps panel uses app-only tools `send_run_instruction`,
`get_intervention_receipt` and `stop_run_from_ui` through the host bridge and
the private Unix socket. No browser write token is included in MCP UI data.
Instruction text is not echoed in model-visible tool results or public snapshots.
Settings remain read-only in the Codex panel. The legacy CDP connector remains
read-only. App-only visibility is host routing metadata, not authentication.

The loopback browser uses same-origin HTTP writes with the existing private
session token. Stale snapshots, unknown session identity and ended Runs disable
new instructions. The backend checks ownership, exact Session and frozen
contracts again before sending.

## Upgrade and verification

Beta.9 migrates SQLite schema 9 to 10, preserving historical wrap-up payloads
and digests. Stop the old owner first and retain a consistent backup. An old
runtime cannot open the upgraded database. If investigating rollback, restore
the old backup to a separate private directory; preserve new records.

Automated checks cover queue advancement, UUID conflicts, private text,
restart uncertainty, Unicode bounds, owner checks, Pi RPC forwarding and
automatic wrap-up, UI connection changes and lost-reply reads. The beta.8
Codex display screenshot verifies the older display only. Beta.9 interactive
host acceptance must be checked with a fresh plugin resource and active Run.

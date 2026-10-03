# Stage budget and wrap-up implementation

Baseline: `1e9fd98454218b108acc0df092276c864288138e`.
Change: `meerkat-stage-budget-20261003`.

Goal: reserve the declared budgets for remaining review, fix and polish stages;
request bounded wrap-up before the current role exhausts its allowance. The Go
service owns these decisions. Missing usage and uncertain requests remain unknown.

Scope: model/prepare contracts, core role allocation, private request authority,
Pi RPC control, public snapshot/types and corresponding tests/documentation.
Use the existing clean linked worktree. No paid provider requests are needed.

Frozen policy: optional stage reserves in the Task budget. Existing tasks without
reserves keep their previous allocation. The declared reserves are conservative
configuration, not a measured prediction. Current-role wrap-up stays within its
existing cap and never increases the Task authorization. Review and polish may
return valid no-change results; incomplete code is never called a delivery.

Acceptance:

- Future-stage allocation is retained across review rejection and bounded fixes.
- Reservations and in-flight requests use the existing shared SQLite ledger.
- The authority issues wrap-up once, after a reservation or settlement reduces
  headroom; the RPC adapter sends one steer message and distinguishes its receipt
  from a completed role. Time-based wrap-up is bounded by the original deadline.
- Unknown acknowledgements are not retried; hard stop and process confirmation
  retain the existing ownership checks.
- Unknown usage, fees and remaining allowance stay nullable in the UI.
- Meaningful offline tests cover stage allocation, the request threshold, one
  wrap-up notification, cancellation and graceful/hard stopping.

Checkpoint recovery is a separate implementation contract. A retained dirty
worktree or idle history alone does not authorize automatic continuation.

## Delivery scope

Version candidate: `0.4.0-beta.2`. The same commit updates generated snapshot
types, React views, embedded assets, setup compatibility and installation docs.
The standard MCP Apps host path has real Codex evidence in
[host acceptance](../docs/codex-ui-acceptance.md). Paid model tasks remain deferred.

Checks:

- Stage allocation, frozen input validation, request reservation thresholds,
  unknown settlement and confirmed overrun tests pass.
- Pi RPC child fixtures verify one wrap-up command, acceptance versus completion,
  original deadline preservation and lost-receipt handling.
- Executor race checks pass, including the installed Pi 0.99.1 probe against an
  isolated loopback fake model. No paid provider request is sent.
- Frontend type checking/build and 18 tests pass; shared React and self-contained
  MCP Apps assets are rebuilt.
- 64 Node installation, packaging and connector tests pass. Configuration refuses
  an unsupported custom Anthropic Messages API before writing a profile.
- `go vet ./...` and `git diff --check` pass.

The first race run exposed concurrent event callbacks after adding steering;
the adapter now serializes stream events and control receipts. An isolated
polish-review test also hit a local ThreadSanitizer startup failure; three isolated
repeats passed. The full core race rerun passed (89.376 seconds). All other Go
packages passed their full race run; the changed executor was rerun after fixing
event serialization and passed again.

The complete redesign is not declared finished: checkpoint pause/resume,
additional-budget decisions, durable control messages and paid end-to-end
continuation remain open in the complete plan.

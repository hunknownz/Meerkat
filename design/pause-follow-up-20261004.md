# Pause and follow-up implementation contract

Baseline: `026fca6b79129acf5e5b0f9f9e158fbd3843dde8`.
Worktree: `codex/controls-core-20261004`.

Goal: independent graceful pause with verified checkpoint continuation, and
durable same-session follow-up input. Scope: model/store/core/executor controls,
their focused tests, CLI/server/MCP transport and control documentation. React
and distribution are a subsequent integration. No dependencies or remote actions.

Acceptance: exact owned Run/Session, UUID deduplication, private bounded text;
pause prevents new directions, preserves dirty work and original allowance;
known queued follow-ups finish before shutdown; late valid delivery remains a
delivery; unknown tool/request/control outcomes block resume. SQLite V11 preserves
V10 payloads and order. Snapshot receipts separate acceptance from completion.
Focused migration, recovery, RPC and transport checks must pass before commit.

Pi's earlier broad task stopped after 892,320 confirmed tokens with no changes.
Its history remains intact. This coordinator implementation is a separate task;
Pi develops the report-contract repair in a separate clean worktree.

## UI integration contract

Scope extends to frontend Intervention, both transports, generated contract and
focused tests/assets. Keep Agents/Tasks/Usage and the accepted quiet layout. Add
one timing selector and graceful pause beside existing controls; preserve UUID
receipt recovery, unknown-state disabling and app-only settings boundaries.

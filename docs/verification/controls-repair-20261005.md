# Receipt feedback, committed checkpoints and execution choices

Date: 2026-10-05. Candidate: `0.4.0-beta.15`. Baseline: `886ee09`.

The user authorized Codex to complete two repairs after the native input exercise
and paid repair attempts. Codex transferred the reviewed receipt patch into a
separate linked worktree, finished its tests and implemented the checkpoint fix.
The original Pi Tasks, checkpoint files, frozen allowances and unknown requests
remain unchanged. No new paid model request was made for these repairs. Codex
usage and cost are unavailable; they must not be reported as zero.

## Behavior

- A receipt lookup shows pending, acquired, unchanged, updated or failed feedback.
  Invalid request/Run/session bindings keep the original receipt. A query never
  sends another instruction, pause or stop. Queued receipts remain queued; read
  feedback does not prove consumption or delivery.
- A confirmed pause or budget stop can save one in-scope provisional commit,
  with or without retained dirty changes. SQLite keeps the original role baseline
  and exact saved HEAD separately. No candidate is promoted from that pause.
- Explicit continuation starts at the verified HEAD and reuses the same Session.
  Final checks cover the whole role from its original baseline. Reporting an
  unchanged provisional HEAD needs no extra commit; retained edits amend it.
  Extra commits, unrelated ancestry, out-of-scope changes and false no-change
  polish reports are rejected. Changed polish requires a fresh exact-SHA review.
- Core passes the neutral role-baseline binding to the Executor. The Pi adapter
  independently checks the checkpoint and committed scope, measures delivery
  against the original role baseline and recognizes graceful pause after a commit.
- [Execution choices](../execution.md) define Direct, Delegate and Workflow as
  coordinator choices. Delegation is optional; role Profiles choose executor and
  model separately before Task preparation. Only Pi is currently implemented.
  Direct Codex work is not represented as a synthetic Meerkat Run. A takeover or
  changed Profile uses a new bounded contract and reviewed transfer.

There is no database schema bump: baseline and HEAD were already separate
fields. Distinct-SHA checkpoint validation requires beta.15 or later, including
backup restoration. Earlier equal-SHA checkpoints remain valid. Unknown request
settlement and unverified process identity still block automatic continuation.

## Local checks

| Surface | Observed check |
|---|---|
| Core checkpoint, Session/recovery/frozen contracts and delegate resume | Focused Go race checks passed (110.188 s). |
| Committed continuation with final neutral binding | Go race checks passed (30.456 s); includes backup/restore, restart, repeated pause, original allowance, amended/unchanged delivery and invalid scope/commit rejection. |
| Storage and checkpoint archives | Separate full Go race package checks passed (34.089 s / 2.922 s). |
| Executor | Full Go race suite passed (57.520 s), opting into installed Pi 0.99.1 against a loopback fake model. Real Pi tools exercised dirty, clean committed and committed-dirty continuation in one Session. Local protocol checks include graceful pause after a commit and rejected baseline bindings. No provider endpoint or real credential was used. |
| Frontend | All 38 React/transport tests passed; locked dependencies installed with Node 22; TypeScript and browser/mount/MCP App builds passed before Go packaging. |
| Host/service contracts | Server and MCP Go race checks passed (5.040 s / 2.903 s). |
| Distribution | All 66 Node checks passed with Node 22 inherited by child fixtures. |
| Static checks | Core/store/checkpoint and executor/core vet passed; diff whitespace check and both skill validators passed. |

An initial frontend build lacked the locked MCP Apps dependency in reused
node_modules; a clean `npm ci` resolved it. An initial distribution run gave its
children Node 25; rerunning with Node 22 in PATH passed. These environment failures
were not hidden as successful checks.

## Installation and remaining acceptance

Installation/cutover results are recorded separately after the built artifact and
loaded plugin are verified. The existing beta.14 native pause and stop evidence
remains historical. Fresh beta.15 receipt feedback needs a reopened Codex panel;
browser and loopback evidence cannot substitute for that host check.

Real paid Pi/model native follow-up delivery remains incomplete after the
failures in [the native record](native-controls-20261005.md). This repair does not
certify the complete plan or the cause of a reported Codex sandbox crash.

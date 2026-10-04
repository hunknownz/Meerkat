# Plan status and verification — 2026-10-04

Candidate: `0.4.0-beta.10`. Original design remains in
[session, budget and coordination](../../design/session-budget-coordination-20261003.md).
The full plan is **not complete**. This candidate finishes project/Provider task
caps, optional hard task token caps and independent progress projections.

## Current capabilities

| Package | Implementation and evidence | Remaining |
|---|---|---|
| 01 | Frozen contracts and separate execution/phase/delivery projections; model tests invalidate reviews after SHA changes. | Fresh host display of projections. |
| 02–04 | Executor capabilities, private Pi RPC, persistent developer/fix sessions and independent reviews. | Unsupported executor adapters remain unavailable. |
| 05–06 | Request reservations, one-time permits, raw usage, optional task token enforcement, per-Run/time/request bounds and explicit additions. | Reliable monetary ceilings; missing prices/fees remain unknown. |
| 07–09 | Early wrap-up, verified checkpoints, owned process lease, restart reconciliation and completed-step recovery. | Unknown processes/requests without evidence require investigation; no replay. |
| 10 | Durable bounded human directions, wrap-up and stop receipts; same-session Pi/browser interaction already verified in beta.9. | Independent pause and queued follow-up input; fresh Codex button clicks. |
| 11 | Dependencies, Worktree exclusion, global/project/Provider task caps; fairness and dynamic-cap tests. | HTTP rate-limit waiting/retry scheduling; current uncertain requests never retry. |
| 12 | Development, exact-SHA review, bounded fixes, polish and re-review. | No claim of independent QA or customer acceptance. |
| 13–14 | SQLite V10, generated TS/runtime validation, snapshots and SSE recovery. | Snapshot fields added here require a matching plugin/runtime and reopened panel. |
| 15 | Agents/Tasks/Usage, current-session directions, stop receipts, budget warnings and separate progress meanings. | Fresh Codex interaction evidence. |
| 16 | Issue source, drafts, delivery summaries and receipt deduplication. | External sends require the user's corresponding authorization. |
| 17 | Updated workflow/delegate skills and task input; delegate continuation already supported. | No automatic authorization or unknown-state override. |
| 18 | Historical import, consistent backup/restore, controls, checkpoints and budget history. | Live uncertain records retained as evidence. |
| 19 | Read-only doctor, pinned Pi protocol/version probes. | Static checks do not certify balance or provider availability. |
| 20 | Repository marketplace, versioned binary builder/installer, CLI/HTTP/MCP and standard panel. | New host controls; public tag/release and directory submission are separate actions. |

## Changed behavior

- New tasks without explicit `maxTokens` monitor cumulative tokens. The default
  500,000-token threshold warns; it does not stop the task. Explicit legacy
  `maxTokens` or `mode: "enforce"` still chooses a hard cap.
- Original prepared tasks retain their hard caps, digests, usage and decisions.
  Monitor mode preserves unknown usage and still enforces frozen Profile Run
  caps, time, request count, output bounds and fix rounds.
- Project/Provider maps accept registered IDs with limits 1–4. Missing maps
  preserve; `{}` clears; supplied maps replace. Active work is not canceled by
  lowering a cap. Eligible tasks can bypass a saturated member.
- Provider limits count whole tasks, reserving all frozen role providers until
  settlement. They do not replace gateway key routing or request-rate quotas.
- Progress is projected from SQLite task/Run/exact-SHA review evidence. It does
  not introduce another writable state machine or imply deployment.

## Checks

Frontend build and 29 React/transport tests passed. Model/core/store/MCP focused
race checks passed, including fairness, dynamic settings, exact clears, malformed
input, a complete workflow beyond its monitor threshold, retained Profile Run
caps, frozen-mode rejection and consistent backup/restore.

The full Go race run passed core (190.959 s), store (30.573 s), MCP and other
packages, except the existing isolated executor version fixture timed out at
three seconds under parallel load. The executor package was rerun separately;
it passed (35.634 s). `go vet ./...` passed. The pinned installed Pi 0.99.1 diagnostic, request-gate
and checkpoint tests also passed against an isolated fake model (7.160 s);
no paid provider request was sent in those checks.

The first Node distribution check found an unbumped MCP App version. The metadata
was corrected and assets rebuilt before rechecking. All 65 Node distribution checks then passed. Both skill entrypoints passed
the Skill Creator validator. Installation/cutover evidence follows below when verified.

## Real Pi and host evidence

The concurrency helper used **140,658 confirmed tokens** over 18 requests and
134.998 wall seconds. It stopped at its bounded allowance without code changes.
The earlier concurrency runs used 820,455 confirmed tokens; together they used
961,113 of the authorized 1,000,000. Missing token breakdowns and fees stay null.
Codex completed the reviewed integration in a separate linked worktree; no claim
is made that Pi delivered these full changes. Codex token usage is unavailable.

A native-host probe received HTTP 502 through the model gateway and lacked a
reliable settlement. Its one request retains a 17,994-token reservation as unknown;
it was not retried or counted as zero.

The new Codex MCP App was readable through the host surface, but its screenshot
and button click failed because the visible region had zero size. The computer-use
tool explicitly prohibits operating `com.openai.codex`. The user is away and has
been asked to reopen and visibly expand the panel after returning. No shell/CDP
workaround was used. Loopback screenshots and protocol probes do not count as
native control acceptance. The beta.8 host screenshot remains historical evidence;
beta.9 real Pi/browser instruction evidence remains valid for that tested path.

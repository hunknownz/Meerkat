# Plan status and verification — updated 2026-10-05

Candidate: `0.4.0-beta.16`. Installation and native Windows results are in
[the current installation audit](installation-20261005.md). Historical real-run evidence below was captured with
beta.13 and beta.14. Beta.15 repairs receipt feedback and continuation after a
scoped provisional commit; fresh native query feedback remains pending. Original design remains in
[session, budget and coordination](../../design/session-budget-coordination-20261003.md).
The full plan is **not complete**. Real Pi/model pause, checkpoint continuation,
queued follow-up, structured reporting and multi-role delivery passed. Fresh
native Codex pause, checkpoint continuation and stop passed on beta.14; native
instruction and follow-up delivery remain pending. Reliable monetary ceilings
and unmeasured timing/cost breakdowns remain unavailable; they are not zero.
See [the current real-run evidence](real-controls-20261005.md) and
[fresh native controls](native-controls-20261005.md).

## Current capabilities

| Package | Implementation and evidence | Remaining |
|---|---|---|
| 01 | Frozen contracts and separate execution/phase/delivery projections; model tests invalidate reviews after SHA changes. | Fresh host display of projections. |
| 02–04 | Executor capabilities, private Pi RPC, persistent developer/fix sessions and independent reviews. | Unsupported executor adapters remain unavailable. |
| 05–06 | Request reservations, one-time permits, raw usage, optional task token enforcement, per-Run/time/request bounds and explicit additions. | Reliable monetary ceilings; missing prices/fees remain unknown. |
| 07–09 | Early wrap-up, verified dirty checkpoints, owned process lease, restart reconciliation and completed-step recovery. | Beta.15 supports one scoped provisional commit with exact saved HEAD, original role baseline and explicit same-session continuation; local checks are recorded in the repair evidence. Unknown processes/requests without evidence require investigation; no replay. |
| 10 | Durable directions, wrap-up, independent pause, queued follow-up, stop and UUID receipts. Go seals new directions before final reporting; Pi drains pending input. Real pause/resume and follow-up delivery passed. | Fresh real Pi/model Codex button scenario. |
| 11 | Dependencies, Worktree exclusion, global/project/Provider task caps; fairness and dynamic-cap tests. Bounded retry requires an attested pre-generation 429 rejection and a new permit. | The configured gateway has not been verified to supply that proof. Ordinary 429, 5xx, disconnects and unknown settlements do not retry. |
| 12 | Development, exact-SHA review, bounded fixes, polish and re-review. | No claim of independent QA or customer acceptance. |
| 13–14 | SQLite V12, generated TS/runtime validation, snapshots and SSE recovery. V10→V12 migration and V12 backup/restore parity passed. | Matching plugin/runtime and a reopened panel are required after a host surface change. |
| 15 | Simplified Agents/Tasks/Usage, directions, follow-up, pause, stop receipts, budget warnings and separate progress meanings. | Beta.15 adds visible pending, unchanged, updated and failed query feedback; fresh native query feedback and paid follow-up delivery remain pending. |
| 16 | Issue source, drafts, delivery summaries and receipt deduplication. | External sends require the user's corresponding authorization. |
| 17 | Optional direct/delegate/workflow selection, updated skills and task input; delegate continuation already supported. | No automatic authorization or unknown-state override. |
| 18 | Historical import, consistent backup/restore, controls, checkpoints, sessions and budget history. All 28 authority tables reconciled; relocated Issue bodies matched original bytes. | Live uncertain records remain unknown and are not replayed. |
| 19 | Read-only doctor, pinned Pi protocol/version probes. | Static checks do not certify balance or provider availability. |
| 20 | Repository marketplace, verified binary builder/installer, CLI/HTTP/MCP and standard panel. Installed beta.16 plugin and runtime retain history; 27 non-lease tables matched across the idle cutover (40 Tasks / 64 Runs). Native Windows and macOS installation checks passed. | Fresh beta.16 native panel/control scenario; public tag/release and directory submission are separate actions. |

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

## Latest verification

Beta.15 repair checks and execution choices are recorded in
[the Codex repair evidence](controls-repair-20261005.md). No additional paid model
request was made for these repairs. The native beta.14 failures and unknown
settlements below remain part of the record.


- Installed Pi 0.99.1 exercised the structured report tool for all three roles
  against a local fake model, including exact SHA/Context bindings and settled
  requests. The executor suite passed separately (47.128 s); budget and RPC
  packages also passed.
- Focused Go race checks passed for report writing, control draining and the
  private bridge. All 15 bridge Node tests passed. CLI race checks passed
  (7.603 s), including bounded plaintext file/stdin follow-ups. Executor vet
  passed. These local checks made no paid requests.
- Frontend build and 33 React/transport tests, 66 Node distribution checks and
  both skill validators passed for beta.13 before the reporting/CLI fixes. Those
  fixes did not change the frontend contract or embedded assets.
- Beta.14 assigns the reviewed fixes a distinct install version. Manifest,
  server, MCP App, frontend lockfile and installation commands match. Assets were
  rebuilt before Go packaging; all 33 frontend tests and 66 Node distribution
  checks passed again for this candidate. Server and MCP race checks passed.
- Three real Pi tasks completed development, independent review, polish and
  re-review. One resumed a verified checkpoint in the original Session; one
  consumed a queued follow-up before reporting. The useful documentation
  candidate `8200679ed4f9fd8ff335bde055da08f2b1611754` was reviewed and integrated
  into the development branch. Exercise-only candidates stayed in their
  isolated worktree.
- Receipt reads kept the same control UUID and did not change private Session
  history. Two historical unknown Runs remain untouched. There were zero
  running or queued Runs at the post-verification snapshot.

Beta.14 cross-builds passed for macOS/Linux on arm64/amd64; only macOS arm64 was
installed and executed here. The verified installer checked the binary version,
checksum and source SHA. An idle-owner cutover retained a consistent backup and
all 27 non-lease authority tables unchanged (34 Tasks, 56 Runs). The controller
lease changed to the new owner as expected. Service health, SQLite schema 12,
integrity and foreign-key checks passed. Doctor still blocks the two historical
unknown Runs/requests; this is retained uncertainty, not an installation pass
for those tasks. No affected task was replayed.

Offline JSON and CSV exports reconciled the useful documentation Task's four
Runs and 306,781 confirmed tokens. Agent/Session/request linkage, one first-review
result and the pause exercise's checkpoint linkage were present. Fees remained
null in JSON and empty in CSV. The [metrics meanings](../metrics.md) distinguish
wall time from summed Agent time and leave unmeasured breakdowns unknown.

On 2026-10-05 the user expanded the native panel and operated real Pi pause and
stop controls. The pause saved a verified checkpoint; explicit continuation
reused the original Session. The separate stop had a processed receipt and
verified process exit. Continuation then hit upstream HTTP 502/EOF and unknown
request settlement; native direction consumption was not verified. A subsequent
manual input window ended without controls. These attempts and measurements are
in [the fresh native record](native-controls-20261005.md). No Codex main-window
automation or CDP workaround was used.

A second native attempt consumed the ordinary instruction and made a scoped
provisional commit. Its queued follow-up reached the original Session history,
but the next model request could not fit the frozen allowance. Beta.14 did not
capture a checkpoint after that commit. Separate Pi repair attempts produced
an unverified receipt-feedback patch and no checkpoint implementation; one
repair Run has an unknown upstream settlement. Across the eight new Runs,
752,961 tokens are confirmed and two requests remain unknown. These are
additional failures and incomplete work, not passing native delivery evidence.

## Historical beta.10 checks

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

Before the beta.11 startup repair and host reload, the Codex MCP App was readable
through the host surface, but its screenshot and button click failed because the
visible region had zero size. The computer-use tool explicitly prohibits
operating `com.openai.codex`. At that time the user was away and had been asked
to reopen and visibly expand the panel after returning. No shell/CDP
workaround was used. Loopback screenshots and protocol probes do not count as
native control acceptance. The beta.8 host screenshot remains historical evidence;
beta.9 real Pi/browser instruction evidence remains valid for that tested path.

After the beta.11 repair, the user restarted Codex and expanded Meerkat. Native
Agents / Tasks / Usage, live heartbeat, delivered Task details, usage and
read-only settings passed DOM and screenshot checks. See
[the fresh host evidence](../codex-ui-acceptance.md#beta11-native-panel-verification-2026-10-04).
Native human instruction and stop delivery still need an active Run; the existing
unknown Run was not replayed. Automation timed out while inspecting its controls,
so that inspection has no passing evidence. Fresh global sidebar entry visibility
also remains unverified. These remaining checks prevent a claim of complete
native control acceptance.

The subsequent user screenshot accepted global Meerkat entry visibility and
full-page rendering. Native host instruction, receipt lookup, same-session
fixture delivery and stop controls then passed with deterministic local protocol
children; unknown Run controls were disabled. See
[the follow-up record](native-controls-20261004.md). No provider request was made.
This closes the host transport checks, while the combined real Pi/model native
control scenario remains pending. The original unknown paid Run stays untouched.

## Acceptance follow-up on 2026-10-05

See [current acceptance](acceptance-20261005.md). The full race suite passed on
Go 1.26.8; prior Darwin fork failures were diagnosed without relaxing gates. A
new user screenshot verifies global entry/display. Real native controls, current
resource reload, Windows host and public distribution remain separate gates.

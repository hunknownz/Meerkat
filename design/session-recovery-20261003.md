# Verified interrupted-step recovery

Date: 2026-10-03. Baseline: `35ef120`.

## Frozen contract

Goal: inspect an interrupted task and explicitly recover a completed local role
step when Go already verified and durably recorded its result before interruption.
Restore that result once, then wait for a separate resume. No role is replayed.

Scope: SQLite V8 completion evidence and recovery decisions; record clean,
successful, confirmed-idle role completion before final settlement; inspect frozen
contract, worktree/SHA, session bytes, process absence and settled request ledger;
explicit owner-authorized CLI/MCP recovery with proposal and stable request UUID;
read-only monitor summaries, migration/backup, tests, skills, local install and
authorized GitHub push. No paid providers, coding subagents or Issue writes.

Completion evidence is private and immutable. Go records the before/after
Run/Task/Session and new delivery/review records under its current lease. Normal
settlement consumes it atomically. After restart a pending record is evidence,
not automatic recovery. Recovery rechecks physical facts and transactionally
compares current DB evidence. It restores the verified role result and session,
leaves the task stopped awaiting explicit continuation and appends a receipt.
Previously consumed usage and unknown fee/timing fields are preserved.

Eligibility requires a clean verified candidate/report, confirmed idle exit with
no uncertain tools or surviving owned process group, and settled request evidence.
Active work, changed contract/profile/history/Git, observed overruns or unknown
requests are refused. PID existence checks use signal 0 only; no recovery action
signals a process, trusts a reused PID, repairs JSONL, replaces files or clears
unknown accounting. Missing completion evidence remains blocked even if a PID
is absent. Existing dirty checkpoints keep their separate verified resume path;
this step does not promote arbitrary dirty interrupted work to a checkpoint.

Inspection is read-only. Apply requires the exact current proposal, `apply:true`,
request UUID and an authorization reference after actual user authorization.
References record permission; free text cannot authenticate human intention.
Identical requests recover the receipt; changed/stale/concurrent inputs refuse.
Lost replies are queried before any further write, with no automatic retry.

Acceptance: completed developer/reviewer/polisher settlement window interruption;
no automatic execution or duplicate candidate/review; explicit subsequent resume
through final delivery; normal settlement; missing/changed/unknown/live-process
refusal; original nullable usage; stale/concurrent decisions and lost receipts;
pending/settled/recovered backup migration and corruption refusal; private fields
absent from monitor; CLI/MCP and read-only UI. Installed Pi is tested against a
loopback model only. Native changed detail and paid model execution stay separate.

## Implementation and checks

Version: `0.4.0-beta.5`. Go/SQLite own private completion evidence, recovery
inspection, proposal matching and durable decisions. CLI and three model-only MCP
tools call that authority; the React Tasks detail displays summaries read-only.
Normal clean completed steps record and consume evidence; interrupted settlement
remains unknown until explicitly recovered. Delegate results remain initial
candidates. Original asynchronous operation records are not rewritten or replayed.

Verification on 2026-10-03:

- Race checks passed across all Go packages, including installed Pi 0.99.1 with
  dummy credentials and an isolated loopback model. The storage future-version
  assertion was updated to use `schemaVersion + 1`, then the complete store package
  passed again. The changed CLI and recovery core were rechecked with the race
  detector after review changes.
- Fault injection between durable completion and final settlement exercises
  development, passing/rejected review, unchanged/changed polish, explicit later
  continuation, async redispatch, no duplicate Run/candidate/review and nullable
  fee preservation. Completed work is not executed again.
- Missing completion evidence, changed files/history/Profile/state, a replacement
  clone at the same path/SHA, unknown request accounting and a live process group
  block recovery. Inspection sends no stopping signal.
- Pending and recovered backup/restore preserve proposals, receipts, history and
  result records. Normal settlements are recorded; corrupted completion digests
  reject backup. A restored lease pointing to a live controller is refused.
- CLI checks private proposal permissions and explicit application. MCP checks
  authorization validation, public receipts, model-only discovery and no automatic
  retry after a lost reply. Monitor snapshots omit private authority/history.
- 21 frontend checks, 64 Node distribution/adapter checks, all three frontend
  builds, `go vet`, workflow skill validation and `git diff --check` passed.

These checks do not establish paid-provider delivery or native rendering of the
new recovery detail. Installation and live-history reconciliation follow below.

# Explicit additional budget decisions

Date: 2026-10-03. Baseline: `8fd6cd6`.

## Frozen implementation contract

Goal: let a coordinator explicitly add token/time allowance to a verified paused
or budget-stopped task and continue its existing work. Original Task, Context,
Profiles, stage reserves and fix-round limits remain frozen. Existing usage and
request settlements remain charged.

Scope: append-only SQLite V7 budget decisions, read-only proposals tied to current
evidence and revision, explicit application with a request UUID and authorization
reference, Go scheduler/request gate integration, CLI/MCP operations, read-only
monitor summaries, migration/backup verification, tests and usage instructions.
No paid provider calls, coding subagents or remote Issue writes. GitHub push and
local plugin upgrades remain authorized.

Proposals do not grant allowance. Application requires `--apply` (or `apply:true`)
and the caller's explicit authorization reference. This local owner-controlled
API records authorization evidence; it cannot authenticate a human's intention
from free text. Coordinators must obtain the user's actual authorization before
applying a real increase. No real task receives new allowance during development.

Only settled, inactive paused or budget-stopped tasks are eligible. The original
contract, selected profile files, linked worktree, session and saved checkpoint
must verify. Unknown process identity, usage or requests, observed overruns and
modified evidence are refused. SQLite rechecks evidence and revision in the same
short transaction as the append. A conflicting second decision cannot silently
reuse a stale proposal. Duplicate identical request UUIDs recover the original
receipt; changed input under the same UUID is rejected. Application never resumes
a task or changes its state; a separate explicit resume still checks all evidence.

Token/time totals remain bounded by existing task limits (10,000,000 tokens and
86,400 execution seconds). Per-Run Profile limits and frozen stage reserves still
apply. No strict fee budget, reset, uncertain-request forgiveness, contract/model
change, active-run budget increase or arbitrary stage redistribution is added.

Acceptance: exhausted checkpoint resumes after a decision with original usage;
same-UUID deduplication, stale/concurrent proposal refusal, changed/unknown evidence
rejection, no implicit execution, request gate honors revision-specific totals,
original contract unchanged, old/new backup reconciliation, private authorization
absent from monitor/export, CLI/MCP validation and readonly UI. Paid full-model
delivery and changed native detail display remain separately verified.

## Implementation evidence

Version: `0.4.0-beta.4`. SQLite schema: V7. Go owns proposals, decisions and
effective allowance. The original frozen budget is unchanged; old Run request
policies keep their recorded revision. New Runs deduct prior settled use from
the effective allowance. CLI and model-only MCP operations use the same service.
The read-only React detail displays original allowance, cumulative additions and
decision reasons without exposing private authorization references or digests.

- An exhausted checkpoint received a fixture-authorized token/time addition,
  resumed the same history, completed review/polish/review and delivered locally.
  Original usage remained charged. Proposal and application made no executor call.
- Same UUID/input recovered the original receipt after delivery; changed input,
  two competing decisions, stale evidence, changed files/context/profile, unknown
  use/request state and excessive totals were rejected.
- Request-gate tests retained the historical policy revision and confirmed use;
  a subsequent Run used the increased total. Corrupt persisted revisions,
  timestamps and contract bindings refused effective allowance or backup.
- Backup/restore retained the decision chain and original contract. CLI tested
  private proposal files and explicit application. MCP checked authorization
  flags, lost-reply recovery without retry, receipt correlation and private fields.
- All Go packages passed `go test -race -p 1 ./... -count=1`, including installed
  Pi 0.99.1 against an isolated loopback model endpoint. A prior parallel-package
  attempt encountered a macOS ThreadSanitizer runtime assertion; the affected
  continuation test passed alone, then the serial package run passed. MCP was
  rerun after final receipt validation changes.
- 20 frontend tests, TypeScript checking, three Vite outputs, 64 Node
  installation/distribution tests, Go vet, diff checks and the workflow skill
  validator passed.

These are isolated/local implementation checks. No existing live task received
additional allowance and no paid provider request was sent. Native Codex's base
monitor has acceptance; the new budget/checkpoint detail still needs a refreshed
native view. The complete 20-work-package plan remains partially implemented.

## Installation and cutover

Implementation commit: `583a109cde4d24e09a60f0241bb1535ebb8b50be`, integrated into
`main` and pushed to GitHub. All four supported binaries were built from that
clean tree. The local marketplace is pinned to that commit; plugin, runtime and
running service report `0.4.0-beta.4`.

The idle beta.3 service was stopped before a consistent schema V6 backup. Beta.4
restored it into a fresh isolated schema V7 directory, then upgraded the existing
live store. Both passed integrity and foreign-key checks. Backup, restored and
live core records matched exactly: 14 tasks, 21 runs, 11 deliveries, 6 reviews,
profiles, contexts, settings and cumulative usage. The live decision table has
zero entries: no existing task was granted additional allowance.

Installed stdio initialization, discovery of all 13 tools, model-only visibility
of the three budget tools, exact MCP App resource and read-only `open_monitor`
passed. The binary SHA-256 is
`a15588ee0776a9f870e2bc3215ca08d420dc61429fd039db13343441561fb02f`;
the embedded UI SHA-256 is
`caf3b389d6534656575042408ce12e68bf833ab9aa360e59a3640d2d145fc332`.
Private authorization/credential fields were absent. Backups and raw private
reconciliation evidence remain outside Git. New native detail acceptance and
paid provider execution remain outstanding; installed transport checks do not
claim those results.

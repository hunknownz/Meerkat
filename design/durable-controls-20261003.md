# Durable bounded Run controls

Date: 2026-10-03. Baseline: `7800766dcad624876a4950e23bcafe8f58eb62d1`.

Goal: persist one owner-authorized bounded wrap-up instruction for an exact active
Run/Session and query its receipt after disconnect/restart. Add a read-only query
for existing stop receipts without resubmitting a stop. Keep acceptance, protocol
acknowledgement and actual Run outcome distinct.

Scope: Go/SQLite V9 control records; executor-neutral control channel/authority;
Pi fixed wrap-up steering; CLI/MCP controls and read-only Task summaries; backup,
restart, tests, workflow skill, local install and authorized GitHub push. Work in
the clean linked task worktree. No subagents, paid model calls or remote Issue
writes. Existing automatic budget wrap-up remains available; Pi coalesces it with
the explicit instruction so an already requested wrap-up is not resent.

Manual input freezes Run, Session, request UUID, approval reference and apply:true.
Go records authority before acknowledgement. Before the protocol write, the
executor calls Go to persist `sending`; after a known queued/handled receipt it
records `acknowledged`. A definite refusal records `rejected`; a lost/uncertain
reply records `unknown`. No uncertain command is automatically retried, including
on restart. Accepted/sending records from interrupted Runs remain unknown.

Go owns lanes and leases; no control locates or signals a process by an old PID.
One explicit wrap-up per Run, with identical requests returning the original
record and changed/reused IDs refused. Control and stop IDs share a namespace.
Pending controls at verified Run exit are closed as unsent/rejected or unknown
before final settlement. Unknown control outcomes block completed-step recovery.
Stop keeps its existing ledger and processed outcome; query does not enqueue it.

The command uses the existing fixed wrap-up message, preserves the original scope
and allowance, and does not create a new task or resume an old one. Queued/handled
is protocol acknowledgement, not proof the Agent followed the instruction or
stopped. Each receipt also reports current Run state. Arbitrary session messages,
follow-ups and standalone pause/resume control remain outside this bounded item.

Public summaries exclude approval references, private history, frozen authority
digests and raw protocol/errors. CLI/MCP queries are read-only; control writes are
model-only and require actual user authorization. Missing replies must be queried
using the same UUID before another write. Backups preserve immutable identity,
state and receipts; corrupt/conflicting records refuse migration/restore.

Acceptance: one protocol effect across duplicate/concurrent requests; wrong or
ended Run/Session, unsupported executor and changed contract refuse; queued and
handled acknowledgements, rejection and missing replies; disconnect/restart with
no replay; explicit wrap-up/budget coalescing; stop query without write; known Run
result and nullable usage preserved; unknown control blocks recovery; backup and
restoration, strict CLI/MCP, private field filtering and read-only UI. Installed
Pi checks use a loopback fake model; paid execution and changed native detail
acceptance remain separate.

## Verification before installation

Version: `0.4.0-beta.6`. The Go service persists explicit wrap-up controls in
SQLite V9 before protocol effects. The Pi adapter reports queued/handled,
definite rejection and unknown replies separately. CLI/MCP expose bounded
requests and read-only receipt lookup; Task details show public control summaries.

Passed on 2026-10-03 in the linked task worktree:

- Full `go test -race -p 1 ./... -count=1`, `GOMAXPROCS=4`, including installed
  Pi 0.99.1 checks against an isolated loopback fake model. No paid calls.
- `go vet ./...`.
- React type check, browser/mount/MCP builds and 22 frontend tests.
- 64 Node installation, distribution, launcher and connector tests.
- Workflow skill validation and `git diff --check`.

New checks cover concurrent duplicate authority, stop/wrap-up ID conflicts,
ended/wrong Run/Session refusal, changed private profile before sending, protocol
send order, queued/handled receipts, rejection, reply/persistence failure,
budget coalescing, accepted/sending restart without replay, unknown recovery
blocking, unsent exit, stop query without submission, backup/restore and corrupt
records. Public receipt rendering and private-field rejection were checked.

The changed control details still require refreshed native Codex acceptance.
Automated rendering, installed stdio and resource checks cannot substitute for
that evidence. Arbitrary messages/follow-ups and paid full delivery remain pending.

## Installation and history reconciliation

Implementation commit: `5b0cc0afa1d46d8546cf09cb535407d3cbc6fe62`.
All four release artifacts were built from its clean Git archive. macOS arm64
binary SHA-256: `04a670ebeda417e20c963689ac3e5b109907f13bd2b205686dca85aa24f4c769`.
The GitHub marketplace is pinned to that commit; the installed plugin and live
service report `0.4.0-beta.6`.

The idle beta.5 service was identified by PID, start time and exact command
before termination. A stopped schema V8 backup was restored into a fresh
isolated V9 directory before switching the live service. Backup, restored and
live databases passed integrity and foreign-key checks. Every original live
row except the renewed controller lease matched the backup; restored Issue body
paths were rebased as intended, with original IDs, fields and bytes preserved.
Public task/run/delivery/review history and usage also matched: 14 tasks, 21 runs,
11 deliveries and 6 reviews. No synthetic control or paid run was added.

Installed stdio checks passed initialization, all 18 tools, model-only control
visibility, read-only receipt lookup, resource loading, `open_monitor`, missing
historical receipt handling and private-authority filtering. Installed MCP HTML
matches the committed asset, SHA-256:
`3c83c83e180e2c9380390b1b272952b1671a6ae7f52781b8cb3a95aabda06585`.

Private backup and reconciliation receipts are retained outside this repository.
Native acceptance of the new details remains pending: reopen the monitor in a
new Codex chat to load the updated plugin/resource. These installation checks do
not replace a refreshed host screenshot or DOM result. No tag, GitHub binary
release or public-directory submission was made.

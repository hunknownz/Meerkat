# Installation and remaining plan checks

Candidate: `0.4.0-beta.16`. Baseline: `136179d`. This audit updates status in the
[earlier plan checklist](plan-completion-20261004.md). Historical evidence remains.

## Implemented

Go scheduler/CLI, private SQLite, frozen Context/Profile/SHA/scope, dependencies,
concurrency, development/review/fix/polish/final review; Pi RPC sessions, durable
controls/receipts, budgets, verified checkpoints and explicit recovery; React
snapshots/SSE, Agents/Tasks/Usage, nullable metrics; Issue drafts/deduplicated
authorized sending; standard stdio MCP Apps and optional CDP.

Beta.15 fixed query feedback and committed checkpoint continuation. Direct,
delegate and workflow are choices; Pi is not mandatory for every code change.
Beta.16 adds Windows ACLs, named pipes and owned Job Objects, host-only source
installation, private dependencies and a portable MCP host package.

## Evidence still required

| Item | State before beta.16 checks | Evidence needed |
| --- | --- | --- |
| Windows runtime | Cross-compiled | Actual Runner ACL/pipe/process/SQLite and clean install |
| macOS clean installation | New installer pending | Disposable install, configuration, service and MCP resource |
| Current Codex package | Fresh host check pending | Current version, visible receipt-query feedback |
| Real model + native controls + final reviewed delivery | Incomplete | Fresh bounded task, original Session directions, exact SHA review |
| Windows Codex native display | No evidence | Real Windows host; CI is not GUI acceptance |
| Public binary release | Unpublished | Authorized tag/assets with checksums/source metadata |

The live database contains unresolved historical runs/requests. Preserve states
and budget reservations; do not reuse them for exercises or automatically replay.
No paid model call is part of installation verification.

## Product limits

- Only Pi is implemented. Direct coordinator work is not a synthetic Run.
- Reliable monetary ceilings require reliable price/fee data. Unknown tokens,
  costs and timings remain null.
- Gateway retries require proof of rejection before generation; ordinary
  429/5xx/disconnect responses do not prove it.
- Installation needs local command/network access. Native UI depends on MCP Apps
  host support. Public directory review is separate.
- Windows cannot infer descendant absence after losing its owned Job handle.
  Persisted PID observations alone do not authorize recovery/termination. File
  contents are flushed; Windows directory persistence is not Unix directory fsync.

## Results

Actual test, commit and CI results are recorded after verification. CLI/resource
success is not native display or real task acceptance.

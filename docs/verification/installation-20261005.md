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

| Item | Current state | Evidence needed |
| --- | --- | --- |
| Windows runtime | Native amd64 checks passed | arm64 remains cross-compile only |
| macOS clean installation | Passed locally and on macos-14 | Current Codex host reload is separate |
| Current Codex package | Fresh host check pending | Current version, visible receipt-query feedback |
| Real model + native controls + final reviewed delivery | Incomplete | Fresh bounded task, original Session directions, exact SHA review |
| Windows Codex native display | No evidence | Real Windows host; CI is not GUI acceptance |
| Public binary release | Unpublished | Authorized tag/assets with checksums/source metadata |
| Full core race regression | Not green | Intermittent checkpoint fixture failures need diagnosis; focused retries passed |

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

- [Native installation CI at `19fb2ae`](https://github.com/hunknownz/Meerkat/actions/runs/37269202798)
  passed on Windows and macos-14. Windows exercised private ACL/reparse checks,
  secured pipe ownership/half-close, Job Object descendant termination, SQLite
  diagnostics/backup/restore, PowerShell installation and private download of
  Node/Go with those toolchains absent. The missing-toolchain step took about
  four minutes on that Runner; it is a manual opt-in on main to limit routine CI.
- Clean installation exercised runtime, configuration, controller reuse, CLI
  snapshot, stdio MCP tools/resource and Pi 0.99.1 on both OSs. Actual Pi made four
  requests to a deterministic loopback fixture, wrote one scoped Git commit and
  submitted a report. MCP snapshot counts included that delivery. This is adapter
  evidence, not a remote model, reviewed workflow or native GUI acceptance.
- Windows fixture candidate: `3eaa8d44db662361087b08603f04a06681bf8a92`;
  macOS Runner candidate: `09ac77410a526b4bb62d93f9bf01a56e36addbf4`.
  Disposable repositories, databases and processes were removed. Fixture usage
  is synthetic; no paid provider call or user credential was used.
- Local Node checks passed (69 before the linked-path regression was added);
  frontend check/build and 38 tests passed. Store, server, executor and CLI race
  suites passed; core without race passed (134.849 s); vet passed. Full core race
  runs failed intermittently, first with a ThreadSanitizer child-runtime check,
  later with checkpoint fixtures returning stopped instead of paused. The
  affected focused cases passed, including four repetitions (75.176 s). The
  cause is unresolved; this does not constitute a fully green race regression.
- Elevated Windows defaults were confirmed to give SQLite WAL/SHM files an
  Administrators owner. Go now selects the current user as its process default
  object owner before SQLite opens; existing file ownership/ACL checks remain
  strict. PowerShell normalizes only newly created objects from the current
  token's default owner. Cross-version inherited module paths are isolated.
- CLI/resource success is not native display or real task acceptance. The live
  beta.15 database had 40 Tasks, 64 Runs and four unknown Run/request outcomes
  before this installation work; their state must survive the update unchanged.

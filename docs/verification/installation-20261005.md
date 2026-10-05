# Installation and remaining plan checks

Candidate: `0.4.0-beta.17` (current audit); beta.16 installation evidence is retained. Baseline: `136179d`. This audit updates status in the
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
| Current Codex package | beta.17 installed; service/resource checks passed | Fresh host reload and visible receipt-query feedback |
| Real model + native controls + final reviewed delivery | Incomplete | Fresh bounded task, original Session directions, exact SHA review |
| Windows Codex native display | User-assisted test deferred | Real Windows host; CI is not GUI acceptance |
| Public binary release | Unpublished | Authorized tag/assets with checksums/source metadata |
| Full core race regression | Passed with Go 1.26.8 | Earlier failures diagnosed as the Go Darwin race fork regression; see [current acceptance](acceptance-20261005.md) |

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

- [Native installation CI at `ba6febc`](https://github.com/hunknownz/Meerkat/actions/runs/37270159027)
  passed on Windows and macos-14. Windows exercised private ACL/reparse checks,
  secured pipe ownership/half-close, Job Object descendant termination, SQLite
  diagnostics/backup/restore, PowerShell installation and private download of
  Node/Go with those toolchains absent. The missing-toolchain step took about
  seven minutes on this Runner; it is a manual opt-in on main to limit routine CI.
- Clean installation exercised runtime, configuration, controller reuse, CLI
  snapshot, installer-owned service startup, stdio MCP tools/resource and Pi
  0.99.1 on both OSs. Actual Pi made four
  requests to a deterministic loopback fixture, wrote one scoped Git commit and
  submitted a report. MCP snapshot counts included that delivery. This is adapter
  evidence, not a remote model, reviewed workflow or native GUI acceptance.
- Windows fixture candidate: `51ce59e822b55b92688eb3023e8c03c3ac51f6a7`;
  macOS Runner candidate: `161d3d8931836b77de4373daad17ae4e076b61a3`.
  Disposable repositories, databases and processes were removed. Fixture usage
  is synthetic; no paid provider call or user credential was used.
- Local Node checks passed (70, including the linked-path regression);
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

## Local update and main integration

- Native installation checks passed again on main at `e110d82`:
  [macOS and Windows CI](https://github.com/hunknownz/Meerkat/actions/runs/37271699711).
  The costly missing-toolchain download was skipped here; the preceding branch
  run covers it. The stdin-import regression passed in the installer suite
  (23 checks), and private read-only Unix profiles passed their focused Go check.
- beta.16 is installed and enabled in Codex through the generated local
  marketplace, with absolute Node/runtime paths. Its Go service responds at the
  previous loopback address. This is registration/resource evidence; the user
  was asked to reload Codex and verify the current panel.
- Only a currently identified idle daemon was stopped. The old runtime made a
  consistent backup before switching. All 27 non-lease authority tables matched
  the backup exactly after the update: 40 Tasks, 64 Runs, four unknown Runs,
  four unknown requests and four unresolved budget reservations remain. The
  private backup and cutover report are retained outside this repository.
- Existing user-owned Pi installation directories had mode 0755; this local
  update explicitly set those three private directories to 0700. The bootstrap
  continues to reject unsafe existing paths instead of silently changing them.
- No paid model call, Issue write, tag or release was performed in this update.

## Current acceptance follow-up

The user supplied a new genuine Codex global-entry/panel screenshot. Full race
checks passed after updating the Go patch. Native Windows and macOS installation
checks passed for beta.17 at `c562482`, including Windows missing-toolchain
bootstrap. A clean macOS arm64 binary installation also passed with Pi and a
loopback model fixture. Main at `07b60e5` passed Windows and macOS CI. beta.17 is
installed locally; all 27 authority tables match the pre-update backup, and a
fresh private restoration recovered the actual sessions and checkpoints without
changing current records. [The current audit](acceptance-20261005.md) records the
diagnosis and remaining native/device/distribution gates. Earlier beta.16
installation checks do not certify the new package.

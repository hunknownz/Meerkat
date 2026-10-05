# Acceptance on 2026-10-05

Candidate: `0.4.0-beta.17`; source baseline: `0fa48a1`.

## Completed checks

- The user supplied a genuine Codex screenshot of the global Meerkat entry and
  Agents / Tasks / Usage. It showed a connected panel, zero active/queued Runs
  and four historical unknown Runs. This verifies display; the screenshot does
  not identify the loaded resource version or prove button delivery.
- Full `go test -race ./... -count=1` passed with Go 1.26.8, including core
  (236.922 seconds), checkpoint, executor, store, MCP and server suites. The
  focused checkpoint rejection/redaction check passed (4.665 seconds).
- `go vet ./...` and diff whitespace checks passed.
- Node tests passed: 71. Frontend type checking, all three builds and 38 tests
  passed. No model request was made by these checks.
- [Native installation CI at `c562482`](https://github.com/hunknownz/Meerkat/actions/runs/37277130607)
  passed on Windows amd64 and macos-14 with Go 1.26.8. Windows also passed the
  opt-in download/bootstrap with Node and Go absent. This verifies native
  processes, storage and installation, not the Windows Codex interface.

## Diagnosed race-test failure

Two fresh full core race runs under Go 1.26.0 failed (223.227 and 222.383
seconds). One emitted the ThreadSanitizer trace-part assertion; another refused
an otherwise valid checkpoint HEAD. Local macOS crash reports identified
`core.test` as both child and parent, SIGSEGV at address `0x8`, with the annotation
`crashed on child side of fork pre-exec`.

These observations match [Go issue 79804](https://github.com/golang/go/issues/79804),
whose fix was backported to Go 1.26.5. The repository, bootstraps and native CI
now use the current Go 1.26.8 patch. No extra retry, weakened checkpoint binding
or longer timeout was added. Earlier failure logs remain in private evidence.

Checkpoint capture rejection now records a static phase code. Rejected file
names, file bodies, Git stderr and storage paths are not exposed by that event.
The existing unsafe/scope fixture verifies rejection and public redaction.

## Remaining acceptance

| Item | State | Required evidence |
| --- | --- | --- |
| Current package and receipt-query feedback | Awaiting native panel access | Reloaded package/resource and visible updated/unchanged query result |
| Real Pi/model native controls and final reviewed delivery | Not started in this audit | Same-session directions/follow-up, pause/checkpoint/resume, stop, exact SHA review |
| Windows Codex panel | User has a Windows computer; device test explicitly deferred | Real Windows Codex screenshot and live controls; native CI is separate |
| Six-target distribution | Built and checksummed from `c562482`; macOS arm64 binary installation passed | Public download requires release authorization; other native targets are covered separately |
| Public prerelease | Unpublished | Explicit maintainer authorization after assets are ready |

The four historical unknown Runs, requests and budget reservations remain
untouched. Their missing evidence cannot be repaired by a successful new task.
Unknown status is not a claim that a process is still running.

## Distribution and historical records

Six binaries were built from the clean committed tree, with source metadata and
SHA-256 verification for every artifact. A fresh macOS arm64 installation from
those binary assets passed private IPC, service startup/reuse, snapshot, doctor,
provider configuration, Pi version probing and the stdio MCP UI resource. Real
Pi made four requests to a deterministic local model and committed candidate
`04cdc5eebfacba27559a0c623e1af65f2539b346`. This is adapter/installation evidence;
it is not a paid model, native button or reviewed-workflow result. Disposable
state and its owned processes were cleaned up. The user's controller was not
replaced by this exercise.

All four historical unknown Tasks were inspected through the read-only recovery
interface. Each remained blocked with `completion_evidence_missing` and
`absence_is_not_completion_proof`. No recovery application, replay, old-PID signal
or budget release was performed. The audit used no paid provider request.

## Deferred Windows host check

On the user's Windows computer, tell the local Agent:
**“安装 https://github.com/hunknownz/Meerkat；使用原生 Windows，先不要调用付费模型。”**
The Agent follows [INSTALL.md](../../INSTALL.md), records the source SHA and
installation result, and opens `open_monitor` after Codex reloads. Capture the
Meerkat entry and connected Agents / Tasks / Usage panel, with the installed
version separately confirmed by `doctor`. An empty history is valid on a fresh
computer. Only then prepare a separately authorized bounded task for native
direction, pause and stop checks; installation alone does not require model
credentials or make a model request.

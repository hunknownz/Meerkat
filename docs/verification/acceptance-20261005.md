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
- Node tests passed: 71. Frontend type checking, all three builds and 38 tests
  passed. No model request was made by these checks.

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
| Windows Codex panel | Device evidence unavailable | Real Windows Codex screenshot and live controls; native CI is separate |
| Six-target distribution | To build from reviewed commit | Binaries, checksums, exact source SHA, clean install |
| Public prerelease | Unpublished | Explicit maintainer authorization after assets are ready |

The four historical unknown Runs, requests and budget reservations remain
untouched. Their missing evidence cannot be repaired by a successful new task.
Unknown status is not a claim that a process is still running.

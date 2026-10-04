# UI intervention verification — 2026-10-04

Candidate version: `0.4.0-beta.9`.

## Implementation

Pi implemented the bounded model input and receipt contract in candidate
`51c58f4820b05d7dcbdc4a31ba8d31d92fdc83a6`; the coordinator reviewed that diff,
ran model checks, then integrated the store, scheduler, executor, HTTP/MCP and
React surfaces. This is not a claim that Pi implemented the whole feature.

The panel adds inline human instructions and stopping to the existing Agent
details. No terminal, new top-level view or task-start control was added.
Instructions bind the existing owned Run/Session and frozen contract. Durable
UUID receipts distinguish persistence, protocol acknowledgement and actual
Run results. Settings remain read-only in MCP; the legacy connector is unchanged.

## Automated checks

- `npm run build` (type generation, TypeScript, browser/mount/MCP assets): passed.
- `npm test`: 27 passed.
- `node --test tests/*.test.mjs`: 65 passed.
- `go test -race ./...`: all packages except executor passed; the isolated
  version-probe fixture timed out while packages ran concurrently.
- `go test -race ./internal/executor -count=1`: the entire executor package
  passed on rerun (36.573 seconds). No production timeout was changed.
- Store migration was rerun after making historical copy order explicit: passed.
- `git diff --check`: passed.

New coverage includes multiple controls in one Run, deduplication and conflicts,
exact Session ownership, legacy payload/digest preservation, private-text
projection, interruption without replay, full Unicode message bounds, owner
checks, actual Pi RPC child forwarding, retained automatic wrap-up, snapshot
refresh without losing typed input, and a timed-out host write followed by a
read using the original UUID.

## Pi development metrics

| Run | Confirmed total tokens | Wall seconds | Result |
|---|---:|---:|---|
| Initial broad UI attempt | 1,709,766 | 93.131 | Paused at the request gate; no code changes. |
| Bounded model contract | 294,187 | 111.243 | Scoped local candidate, reviewed by the coordinator. |
| First display probe | 99,342 | 72.819 | Finished before a human instruction was sent; does not verify intervention. |
| Live UI instruction | 25,954 | 66.564 | Same-session instruction applied in scoped candidate `28529f5`. |

The bounded run reported 29,063 input, 13,476 output and 251,648 cache-read
tokens. Cache-write usage and monetary cost are unknown. The broad attempt's
input/cache breakdown is also unknown. Total tokens include cache usage and
are not a monetary bill. Both runs and the unused paused checkpoint remain
in private state; the history was not reset.

The live instruction run reported 5,301 input, 2,221 output and 18,432 cache-read
tokens. Cache-write usage and monetary cost remain unknown. Its wall time
includes a deliberate 45-second wait for UI interaction; it is not a development
speed benchmark.

## Installation and service cutover

- Feature source: `1728db9e06033b533fbbfa03833a139e12ae9f91`.
- Clean-source release build for `darwin/arm64`: passed.
- Installed runtime and plugin cache both report `0.4.0-beta.9`.
- Runtime SHA-256: `fa655eb2fe35c9d111e18a7dee5c336853de5c2516197f044cf39e6db628ca56`.
- Local marketplace tracks the repository's `main`; the previous source was
  pinned to an older commit, so an upgrade alone did not fetch beta.9.
- The idle old service was stopped after a consistent database backup. The
  new owner migrated schema 9 to 10, preserving all 19 tasks and 28 runs present
  at cutover. Integrity and foreign-key checks passed.
- Installed stdio MCP initialization, tool listing and UI resource reading
  passed. The three app-only intervention tools and the new inline UI are
  present. This protocol check does not prove a host click works.

## Real executor interaction

In the loopback panel, the coordinator expanded a running developer and sent
an instruction to replace the last line of one scratch document with
`Human direction verified.` The panel showed a durable queued receipt. Its
Run and Session IDs stayed the same through the instruction and final delivery.
Reading the original receipt UUID returned the settled Run outcome without
resending the instruction.

Pi delivered candidate `28529f5357249dbb3a8d82dc4c0a56cdeea23ae5`. The coordinator
reviewed the actual diff: one line replaced in the declared document, with the
heading and session explanation preserved. The linked worktree was clean.
The scratch candidate was retained for evidence and was not integrated into
the product repository. No network publishing occurred in that run.

## Acceptance boundary

The real beta.8 Codex MCP panel was inspected again during development and
showed live task history. That does not verify beta.9 human controls. Fresh
beta.9 installation, real executor interaction and native host interaction
are separate acceptance evidence. Installation and real executor interaction
passed as recorded above. Native beta.9 intervention remains pending: the
expanded Codex tab still contains the beta.8 resource. The user has been asked
to close and reopen it, or restart Codex if the old resource persists. Browser
interaction and stdio protocol checks do not replace that host acceptance.
The original project-concurrency task and optional-budget policy remain
separate unfinished work.

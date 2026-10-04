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

The bounded run reported 29,063 input, 13,476 output and 251,648 cache-read
tokens. Cache-write usage and monetary cost are unknown. The broad attempt's
input/cache breakdown is also unknown. Total tokens include cache usage and
are not a monetary bill. Both runs and the unused paused checkpoint remain
in private state; the history was not reset.

## Acceptance boundary

The real beta.8 Codex MCP panel was inspected again during development and
showed live task history. That does not verify beta.9 human controls. Fresh
beta.9 installation, real executor interaction and native host interaction
are separate acceptance evidence; update this record after those checks.
The original project-concurrency task and optional-budget policy remain
separate unfinished work.

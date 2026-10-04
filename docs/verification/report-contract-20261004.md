# Role report contract verification — 2026-10-04

The role report contract binds each run to its frozen Context and candidate.
`candidateSha` must equal the final worktree HEAD and `contextDigest` must equal
the frozen Context digest byte-for-byte (or JSON `null` when there is no frozen
context). Strict validation in `internal/executor/report.go` rejects any report
that fails either binding as `report_stale`, unchanged by this work.

## Observed failure

Two real Pi reviewers normalized `contextDigest` by dropping the `sha256:`
prefix before writing their role report. The reports were otherwise well formed,
but the strict byte-for-byte comparison in the executor rejected them as
`report_stale`. Root cause: `buildPrompt` in `internal/executor/pi.go` showed a
pseudo-JSON shape (`{"candidateSha","contextDigest",...}`) that was not valid
JSON, so models had to reconstruct the report from prose and guessed at both
the shape and the digest form.

## Fix

`buildPrompt` now appends a role-specific, deterministic, valid JSON example
built with `json.Marshal` (`SetEscapeHTML(false)`) via the new `reportExample`
helper, plus an explicit instruction to copy the Context digest byte-for-byte
— never normalize, truncate, or re-derive it — and to write the JSON `null`
value when the digest is absent. The reviewer example pins `candidateSha` to
the exact `ExpectedSHA`; developer and polisher examples carry the placeholder
`<final HEAD from git rev-parse HEAD>` with surrounding text instructing the
substitution of the final HEAD. Absent Context renders `"contextDigest":null`.

The examples below use the frozen Context digest
`sha256:7357b2dd4b2ba0f835799a19c222a78259b54bcd1bda781ddaf590a7a1b22c36`
and parse as JSON for each supported role.

### Developer and polisher (decision `changed` | `no_change`)

```json
{"candidateSha":"<final HEAD from git rev-parse HEAD>","contextDigest":"sha256:7357b2dd4b2ba0f835799a19c222a78259b54bcd1bda781ddaf590a7a1b22c36","summary":"Implemented the task; all local checks pass.","checks":[{"command":"go test ./internal/executor -count=1","result":"pass"}],"knownGaps":[],"decision":"changed"}
```

### Reviewer (verdict `pass` | `changes_requested`)

```json
{"candidateSha":"0c9a1f2b3d4e5f60718293a4b5c6d7e8f9a0b1c2d3e4f5061728394a5b6c7d8e9","contextDigest":"sha256:7357b2dd4b2ba0f835799a19c222a78259b54bcd1bda781ddaf590a7a1b22c36","verdict":"pass","summary":"Reviewed the candidate against the task; all local checks pass.","findings":[],"checks":[{"command":"go test ./internal/executor -count=1","result":"pass"}],"knownGaps":[]}
```

The reviewer example carries the exact reviewed SHA; in a real run that value is
the run's `ExpectedSHA`. With no frozen context, `contextDigest` becomes
`"contextDigest":null` in every role and `buildPrompt` prints a null digest
line instead of a digest string.

## Checks

| Command | Result |
| --- | --- |
| `go test ./internal/executor -run "Prompt|Report" -count=1` | ok |
| `go test ./internal/executor -count=1` | ok |
| `go vet ./internal/executor/` | clean |

## Scope

Only `internal/executor/pi.go`, `internal/executor/prompt_test.go` and this
record changed. The new prompt tests cover: each role example parsing as JSON
and preserving the prefixed digest, absent-context rendering as `null`, the
reviewer example passing strict report validation with the exact
`ExpectedSHA`, and prompt robustness when the task brief and context contain
quotes, backslashes and Unicode. Stale SHA/context validation and all report
schema checks remain unchanged.

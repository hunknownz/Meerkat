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
`report_stale`. A contributing prompt problem: `buildPrompt` in `internal/executor/pi.go` showed a
pseudo-JSON shape (`{"candidateSha","contextDigest",...}`) that was not valid
JSON, so models reconstructed the report from prose and guessed at both
the shape and the digest form.

## Earlier prompt fix

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
{"candidateSha":"<final HEAD from git rev-parse HEAD>","contextDigest":"sha256:7357b2dd4b2ba0f835799a19c222a78259b54bcd1bda781ddaf590a7a1b22c36","summary":"<summarize the actual work>","checks":[],"knownGaps":[],"decision":"changed"}
```

### Reviewer (verdict `pass` | `changes_requested`)

```json
{"candidateSha":"0c9a1f2b3d4e5f60718293a4b5c6d7e8f9a0b1c2d3e4f5061728394a5b6c7d8e9","contextDigest":"sha256:7357b2dd4b2ba0f835799a19c222a78259b54bcd1bda781ddaf590a7a1b22c36","verdict":"pass","summary":"<summarize the actual review>","findings":[],"checks":[],"knownGaps":[]}
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

Coordinator review removed sample pass evidence. Examples now leave checks empty; the prompt requires actual command outcomes. The Pi developer candidate used 649,025 confirmed tokens over 13 requests and 294.116 wall seconds. Fees and missing breakdown fields remain unknown. This was one developer run, reviewed by the coordinator; it was not the complete automated workflow.


## Structured submission

A later paid continuation still produced a 39-character SHA and used
`observed` instead of `result` in a check. It remains a failed run, with its
commit and usage preserved. Prompt examples alone did not remove transcription
errors.

In budget-gated Pi RPC sessions, `meerkat_report` now accepts only role
conclusions and observed checks. Go supplies the exact current HEAD and frozen
Context, validates the complete report and writes a new private file. Reviewers
must still be on the expected SHA. The final executor checks remain unchanged;
a saved report is not a delivery. Non-RPC compatibility uses the earlier
manual report contract.

The tool is explicitly included in Pi's restricted tool list. All three role
schemas were exercised by installed Pi 0.99.1 against an isolated loopback
model: real tool execution, exact bindings, private report output and settled
requests passed. No paid request was made in these checks.

Focused Go race checks passed. The bridge's 14 Node tests passed, including
report registration before session start and no silent retry of failed report
submission. Invalid drafts do not turn model settlement into unknown usage.
Changed duplicate reports and symlinks are rejected; identical reports return
the same receipt without a model call. Model-provided SHA/Context overrides,
wrong role fields and unknown nested check fields are rejected.

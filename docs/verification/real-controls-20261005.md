# Real Pi controls and delivery — 2026-10-05

Candidate: `0.4.0-beta.13`; installed Pi: `0.99.1`; model:
`zenmux/deepseek/deepseek-v4-flash`, routed through the configured local gateway.
Runs occurred on 2026-10-04 UTC and spanned 2026-10-04–05 Asia/Shanghai.
Private task contracts, profiles, receipts, histories and backup reports are
retained outside the repository. This record contains no credentials or raw
Session transcript.

## Report and control boundary

The model submits role conclusions to `meerkat_report`. Go supplies the current
Git HEAD and frozen Context, validates the full report and writes it privately.
The executor still checks exact SHA, Context, scope and role before accepting
an outcome. A saved report alone is not a delivery.

Go seals new direction admission only after accepted and sending controls
drain. Pi checks pending messages before and after that handshake. A queued
follow-up defers reporting until consumed, preventing an early report from
binding a later commit. Stop remains available independently. Identical report
submissions return the same receipt; changed duplicates are refused.

The CLI also accepts bounded plaintext follow-up files/stdin. JSON task and
settings inputs keep their JSON validation. Text stays private; receipt queries
do not send it again.

## Real pause and resume

Task `f3219436-86d6-41b4-acf4-845abb4d1cfa` paused developer Run
`28b13d9a-ac4f-4ad3-832c-a996c20232c3` through the CLI. A protocol acknowledgement
was followed by an actual saved checkpoint. Explicit continuation created Run
`def7ce43-8ead-4e25-ba3d-f1d81f80f4a5` in the same Session,
`198ce182-2a2d-47a9-ad7f-0923620c6c78`. The checkpoint was consumed once and
the frozen contract and allowance remained unchanged.

Development, independent review, polish with a changed SHA and independent
re-review completed. Final local candidate:
`1b7480f764bd63aa3ac7cbe950829b457e26b4f5`. A subsequent separate task corrected
the exercise note's checkpoint/commit wording and delivered
`696681910d183c98e94b7398bde012c68d4a29d8`. Both exercise candidates remain in
the isolated worktree; neither is a production or QA result.

## Real follow-up and useful delivery

Task `1a006134-58fa-434d-9389-4ed438dcdc5e` changed only
`docs/run-controls.md`. Developer Run `bbb38a45-cb9a-484d-a742-f59b80a217b6`
received follow-up UUID `d7145ece-5e1d-4144-892b-e079fbeb5cfa` in Session
`467ed897-9052-45fe-8901-292b28f4d504`. The receipt recorded acceptance and Pi
queue acknowledgement; private history contained the direction once.

The first report submission deferred while input was pending. Pi consumed the
follow-up, committed the requested sentence and submitted the final report.
Independent review passed; polish changed the candidate; independent re-review
passed for `8200679ed4f9fd8ff335bde055da08f2b1611754`. The coordinator reviewed
the actual diff and integrated those original Pi commits into the development
branch. Repeated receipt reads left one control row and unchanged Session
history bytes. This verifies real CLI/backend delivery, not native UI clicks.

## Failures retained

- Task `d73f6ec3-7326-4734-82ef-306f5c771d0b` paused and resumed, but its manual
  report supplied a 39-character SHA and a wrong check field. Validation rejected
  it. The commit and actual usage were retained; its report was not patched into
  a successful outcome. This prompted deterministic report binding.
- Task `fd42a341-e302-46f1-95c8-219a0a9feb1e` investigated control internals after
  an instruction to wait for the coordinator. It produced no candidate and
  stopped when another request could not fit its frozen Run allowance. No
  follow-up was sent. The next task used an ordinary scoped documentation goal.
- A planned follow-up to the correction exercise was rejected by the CLI's
  former JSON-only input reader before reaching the daemon. That task's delivery
  does not count as follow-up evidence. The plaintext fix preceded the successful
  documentation task above.

## Measured usage

Numbers below are confirmed executor totals for these five named cases, including
failed attempts. They are not total project or coordinator usage.

| Task prefix | Outcome | Confirmed tokens | Summed Agent seconds |
| --- | --- | ---: | ---: |
| `d73f6ec3` | Failed manual report | 302,292 | 142.752 |
| `f3219436` | Delivered after checkpoint resume | 248,424 | 173.910 |
| `fd42a341` | Stopped, no candidate | 288,046 | 97.240 |
| `4e424946` | Delivered corrected exercise | 198,395 | 181.737 |
| `1a006134` | Delivered documentation with follow-up | 306,781 | 207.138 |
| Total | Five cases | 1,343,938 | 802.777 |

The useful documentation operation took 208.431 wall seconds. Agent seconds
sum execution segments, including tools and controlled waits; they are not pure
model time. Missing cache breakdowns and fees remain null. Codex token usage is
unavailable. The stopped case's configured request ceiling is not actual usage.

## Local checks and restoration

- Installed-Pi executor checks passed (47.128 s), including all role report
  schemas and an actual queued follow-up against a local fake model.
- Focused Go race checks passed for reports, control drain and the private
  budget bridge. CLI race checks passed (7.603 s); executor vet passed.
- All 15 Node bridge tests passed. The installed-Pi follow-up fixture settled
  six local requests and required no paid provider request.
- V10→V12 migration and a separate V12 restore rehearsal reconciled all 28
  authority tables. Issue receipt payloads matched after expected body-path
  relocation, and the body bytes matched. Backup container tables were consumed
  as restoration bundles rather than omitted authority records.
- Idle service cutovers retained consistent snapshots, exact source SHA/binary
  checksums and historical outcomes. Two old unknown Runs remain unknown and
  are not signaled or replayed by an old PID.

## Remaining native gate

The standard Codex panel and earlier native protocol-fixture controls already
have [host evidence](native-controls-20261004.md). The fresh beta.13 combined
real Pi/model native direction, follow-up, pause and stop scenario remains
pending. On 2026-10-05 the expanded MCP App inventory was empty after opening
the monitor inline. The user has been asked to expand it; automation cannot
operate Codex's main window. No browser screenshot or CLI result substitutes
for this gate. The [complete plan](plan-completion-20261004.md) stays open.

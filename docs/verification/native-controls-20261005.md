# Real Pi and native Codex controls — 2026-10-05

Installed plugin, MCP App and Go service: `0.4.0-beta.14`; source baseline
`d7dd9f4b41a636787b17a8494deeaa2fec0ef3ca`. The user operated the native Codex
panel. The coordinator read durable receipts, process identity, request
settlements and scoped files separately. No browser or CLI write substituted
for a native button click.

## Pause and checkpoint continuation

The user clicked **暂停并保留进度** in the real Pi developer Run. Its native
screenshot shows the pause request and acknowledgement. Go then saved checkpoint
`374649bc-b9ea-4509-aac3-f3aafeab7260`, retained the incomplete scoped draft and
settled the Task as paused. Run `8fc7019b-6aba-46ef-a275-b89ebf34fbe6` stopped;
its owned process was absent. There was no candidate commit.

The coordinator explicitly resumed the verified checkpoint. Run
`07e3648a-e05e-4148-b51f-ba0b1a79d68e` reused Session
`a4dbbc9c-8cb7-4f90-91a9-e679203611c7`. This establishes checkpoint continuation,
not completed delivery.

| Evidence | ID |
| --- | --- |
| Task | `50768ad6-4f16-4d7f-816c-19c3d02657f5` |
| Pause receipt | `0efa31a5-305e-4ec3-a20f-80437e66b403` |
| Explicit continuation operation | `aa94ceff-b998-46fb-8ce4-1a65ebfaa6e3` |

The pause screenshot is retained privately; SHA-256:
`87657f5a9e63fef08e90ebdf2790a124c7792d032970daf97e270429aadffa9d`.

## Interrupted direction attempt

The resumed Run received two native submissions, both recorded as `instruction`:
`ce96e2d4-e929-422b-96cf-c3d05bb06cab` and
`42b536bc-0a7b-4f27-863b-8181182ec6be`. Both received queued protocol receipts.
Neither requested sentence appeared in the draft; no delivery was produced.
Changing the selector after submission does not change the stored control kind.

The private Pi history reports HTTP 502 with an upstream ZenMux connection EOF.
Two requests settled; the third has no reliable usage or terminal response.
The Task remains unknown with reason `request_budget_unverifiable`; the process
exited. Its 61,325-token reservation remains held, not recorded as actual usage.
The uncertain request and Run were not replayed, recovered or counted as zero.
The gateway interruption is separate from the selected input timing.

The user's native screenshot shows the unknown state, disabled controls and
the last queued receipt; SHA-256:
`611ab16ae9ebdff98e65828982143951fca7699d94d3f733b08611cd0248fab6`.

## Stop

The user clicked **停止运行** once in a separate real Pi Task. Receipt
`60a6c939-8feb-4c54-b24e-935b615e0d36` became processed with outcome stopped.
Run `a6eb5cbe-6b03-4e35-9aa2-91b6efd32011` stopped; its owned process was absent.
Task `38d6bd83-ccec-4163-bcf4-a5f1c599400d` has reason `stop_requested`, retains
its incomplete draft and has no candidate. Two receipt queries returned the
same UUID and outcome. They sent no new stop request.

The user reported this button click in chat. A fresh stop screenshot was not
provided; process exit and the stored result were verified independently.

## Direction window without input

A separate Task `bc165ef8-d516-4a49-9e01-80e365635099` opened a 180-second
manual input window. No controls were stored. Pi ended without committing or
reporting, preserving its draft; Run `089d1a2d-2825-4943-81da-3eb3d6ae34d4`
failed with `no_commit` and its process exited. This did not verify native
direction delivery. The coordinator's timing left too little time for the
human to find and operate the Task; the next attempt uses a longer window.

## Measurements

| Run | Settled requests | Confirmed tokens | Unknown requests | Execution wall seconds |
| --- | ---: | ---: | ---: | ---: |
| Pause | 6 | 48,858 | 0 | 110.214248 |
| Interrupted continuation | 2 | 23,458 | 1 | 127.775701 |
| Stop | 3 | 10,909 | 0 | 72.176734 |
| Direction window without input | 5 | 26,566 | 0 | 203.428704 |
| Native direction, second attempt | 17 | 154,642 | 0 | 441.240813 |
| Committed-checkpoint repair attempt | 15 | 296,618 | 1 | 235.445220 |
| Receipt-query repair, first Run | 8 | 87,654 | 0 | 72.803467 |
| Receipt-query repair, continuation | 4 | 104,256 | 0 | 90.911285 |
| Total confirmed subset | 60 | 752,961 | 2 | 1,353.996172 |

## Native instruction and follow-up, second attempt

Task `9871aae7-813b-4903-ac0d-1c9e18df447e` used the unchanged original
200,000-token/450-second Run Profile. The user sent an instruction and a
follow-up from the native panel. Both received queued receipts in developer
Run `5e009bf3-e03b-4db4-a3bc-1a01de9bee2c`, Session
`64fdba2c-00a3-47f4-bb72-edbc83d641c8`:

| Kind | Receipt |
| --- | --- |
| Instruction | `18b57396-c762-4792-ac74-6004d6ac9296` |
| Follow-up | `86e66dfb-f3d6-4d59-b7cb-f00af36b8498` |

The instruction was consumed and appears in scoped provisional commit
`109c062d39ee8692a360ec0774411bbe99424af8`. The final report tool correctly
deferred while the follow-up was pending. Pi then placed the follow-up in the
same Session's user history, but its next model request could not fit the frozen
Run allowance. All 17 sent requests settled, totaling 154,642 tokens; the denied
next request was not sent. The scoped document still lacks the second sentence.
This is not complete follow-up consumption or delivery.

The native screenshot displays the selected timing and matching follow-up
receipt; SHA-256:
`ef35fb1c52c263c9a0c7238caeb0527431bd41de14dbc1caf67bc4023202477d`.
The user saw no visible change when querying the still-queued receipt. This
revealed a receipt-query feedback gap, separate from control delivery.

The Task stopped with `token_limit`. Beta.14 only captures partial checkpoints
while HEAD equals the role baseline, so the new provisional commit prevented
checkpoint capture and continuation. The original limits, commit, history and
receipts are preserved. A separate frozen coding Task addresses future committed
checkpoint behavior; it does not reconstruct or replay this stopped Task.

## Repair attempts and remaining defects

The committed-checkpoint repair Task `63fdc2f2-815d-4929-9a3e-5ff08552bb12`
made no source changes. Its model output was truncated before an implementation;
a later request ended with an upstream EOF. Run
`42da2938-62ef-43f4-8d48-fae5634573f4` remains unknown. Its 103,382-token
reservation is held, not counted as actual usage. The process exited and the
uncertain request was not replayed.

The receipt-query feedback Task `a5458ed5-c490-45a9-ba5e-9a840f56adfd`
first ended with truncated output and no source changes. An explicit continuation
in the same Session produced a two-file UI patch, then stopped when the next
request could not fit the frozen allowance. Verified checkpoint
`3edfc597-2710-47b4-b336-4f23db9bbdea` retains that dirty progress.
The focused React check passed seven tests and failed two new assertions:
one fixture returned an instruction kind for a follow-up receipt; one expected
an exact text node that also contains the control label. The patch is not a
delivered commit and is not installed. Both repair Tasks retained their original
frozen limits and history.

An isolated, unpaid probe using the installed Pi 0.99.1 SDK confirmed that
`--thinking low` sends `reasoning_effort: low` for the configured model. This
does not prove that the gateway or upstream honors it. Meerkat's request bridge
currently caps output at 8,192 tokens; request admission also uses a conservative
input reservation. Those bounds explain why a Run can stop before its confirmed
usage reaches the nominal limit. The observed truncations and unknown requests
remain failures; they are not resolved by increasing old frozen limits. The
protocol comparison used the official [DeepSeek thinking-mode guide](https://api-docs.deepseek.com/guides/thinking_mode/)
and [ZenMux Pi guide](https://zenmux.ai/docs/best-practices/pi-coding-agent.html).

The table covers eight Runs across six Tasks, including failed repair attempts.
The two uncertain Runs have confirmed usage subsets; their complete Run usage
remains unknown. Their combined 164,707-token held reservation is not consumption.
All eight execution processes had exited at reconciliation; there were no
running or queued Runs. Fees and Codex usage are unknown. Deliberate human interaction
windows are included in wall time; these are not development speed measurements.
Raw histories, profiles, receipts and screenshots stay outside Git. Exercise
drafts and commits remain in their isolated worktrees.

## Remaining

Native queued follow-up consumption through real Pi/model delivery still needs
a passing result. Receipt-query feedback and checkpoints after a scoped
provisional commit also need implementation, checks and installation. The full
design is not closed by a queued receipt, checkpoint or stopped process.
Public release and directory submission remain separate actions.

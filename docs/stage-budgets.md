# Stage reserves and early wrap-up

Optional `budget.stageReserves` is frozen when preparing a task:

```json
{
  "maxTokens": 150000,
  "maxWallSeconds": 600,
  "maxFixRounds": 1,
  "stageReserves": {
    "reviewTokens": 12000,
    "fixTokens": 20000,
    "polishTokens": 12000,
    "wrapUpTokens": 6000,
    "wrapUpSeconds": 30
  }
}
```

Go retains an allowance for every remaining permitted fix and its review, the
current candidate's review and the review after polish. It rejects a configuration
that leaves no development allowance. Actual unused tokens remain in the shared
task ledger. Per-profile limits can reduce the current Run's allowance further.
Single-run delegation does not reserve later roles. Tasks without `stageReserves`
retain their previous allocation.

When ledger reservations or settlements reduce the current Run's headroom to
`wrapUpTokens`, Go requests wrap-up once. `wrapUpSeconds` requests wrap-up before
the original deadline. Zero disables that trigger. The Pi adapter sends one
steering message: finish safe current work, preserve progress and report gaps.
Accepting this message does not complete the role, produce a delivery, increase
the allowance or extend the deadline. Lost receipts are not automatically retried.

The original hard limits, report verification, exact SHA and scope checks remain
required. A confirmed overrun stops execution; missing settlement retains its
reservation and unknown state. Monetary costs remain unknown without billing
evidence. Token reservation is an estimate, not a guaranteed tokenizer ceiling.

The monitor labels active wrap-up and shows task authorization, conservative
available allowance, pending/unknown requests and safe session summaries. It
does not expose prompts, private history paths, credentials or write tokens.

Verified dirty-worktree continuation is described in [checkpoints](checkpoints.md).
This release does not implement additional
budget authorization. Preserved files and an idle session alone do not authorize
continuation. See the [complete plan](../design/session-budget-coordination-20261003.md).

# Additional allowance with explicit authorization

Meerkat preserves the frozen original Task budget and appends each authorized
increase to the SQLite V7 decision ledger. Previously consumed tokens and execution
time remain charged. A proposal does not grant allowance or start an Agent.

## CLI

For an existing verified paused or budget-stopped task:

```sh
node scripts/launch.mjs budget propose --task TASK_ID \
  --add-tokens 50000 --add-wall-seconds 300 \
  --reason 'Finish the original bounded scope' --output /private/proposal.json
```

Review the exact increase and obtain the user's authorization. Only then apply
the unchanged proposal using a fresh stable request UUID and a short reference
to that authorization (not a credential):

```sh
node scripts/launch.mjs budget apply --input /private/proposal.json \
  --request-id DECISION_UUID --authorization 'User approval reference' --apply
node scripts/launch.mjs execute --task TASK_ID --resume
```

The proposal file must be new; it is written with private permissions. Amounts are
non-negative integers, at least one positive. Reason and authorization reference
must be nonempty bounded text without credentials. Total task limits remain
10,000,000 tokens and 86,400 execution seconds. Profile limits, fix rounds, stage
reserves and request-count limits remain unchanged. Zero addition of one dimension
means that dimension is unchanged; it is not unknown measured usage.

Application only records allowance. Explicit resume independently checks files,
index, SHA, worktree, context, profiles, session and remaining effective allowance.
Review and delivery gates still apply.

## MCP

- `propose_budget`: read-only proposal with exact additions, revision and evidence.
- `apply_budget_decision`: exact proposal, `requestId`, `authorizationRef` and
  `apply: true`. The caller must already have actual user authorization.
- `get_budget_decision`: read the durable public receipt using that request ID.

This owner-controlled local API records an authorization reference; free text is
not proof that a human granted permission. A coordinator cannot infer permission
from the proposal or from the task needing more budget. The monitor remains
read-only, showing original allowance, cumulative additions, totals and reasons;
it exposes neither authorization references nor authority digests.

## Conflicts and recovery

Changed task/profile/session/checkpoint evidence or budget revision makes the
proposal stale. Only settled inactive tasks are eligible; unknown process identity,
unknown usage/requests or observed overruns are not repaired by adding allowance.
A partial token breakdown is eligible only when the same Run has a closed request
ledger, at least one settled request, no unresolved results or overruns, and raw
request totals reconcile exactly with its recorded total. Missing input/cache
fields and fees stay unknown; an authorized increase never rewrites prior usage.
A known total without that matching ledger remains blocked. Fully complete
historical usage keeps its existing eligibility checks.

Active runs, completed tasks and user-canceled work cannot receive a decision.

An identical request UUID/input returns the original receipt, even after later
execution. Reusing that UUID with another proposal or authorization is refused.
Concurrent applications of one proposal have a single winner. A lost reply is
unknown: query `budget receipt --request-id DECISION_UUID` (MCP
`get_budget_decision`) before any further write. Never automatically retry with
a new UUID. If no decision exists, independently recheck authority and current
state before explicitly recovering the same request.

Historical Runs retain their original budget revision and request policies. New
Runs use the effective total after the decision. Consistent backups include the
decision ledger; restore only into a new private directory. Do not replace the
live store with an older backup after recording additional decisions or Runs.

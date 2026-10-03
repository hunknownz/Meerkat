# Recover a verified completed step

`0.4.0-beta.5` can recover a completed role step if the Go service saved its
verified result before an interruption prevented final SQLite settlement. The
original Run, candidate, review and usage are restored once. No executor starts
during inspection or recovery. A workflow then waits for explicit continuation.

This requires completion evidence recorded by this runtime. An absent PID alone
does not establish completion. Earlier unknown runs without that evidence remain
blocked; recovery does not certify dirty work, repair history or settle unknown
requests. Confirmed incomplete work uses the separate [checkpoint](checkpoints.md)
path.

## Inspect, authorize, recover, continue

With the local service running, inspect the interrupted task:

```sh
node scripts/launch.mjs recovery inspect --task TASK_ID
```

The result is `verified` or `blocked`, with stable check reasons. Only a verified
inspection has a proposal. Save it to a new private file:

```sh
node scripts/launch.mjs recovery inspect --task TASK_ID \
  --output /private/recovery-proposal.json
```

Review the task, Run and check results. Obtain the user's actual authorization
before applying the exact proposal, using a stable request UUID and a short
reference to that approval:

```sh
node scripts/launch.mjs recovery apply --input /private/recovery-proposal.json \
  --request-id RECOVERY_UUID --authorization 'User approval reference' --apply
node scripts/launch.mjs recovery receipt --request-id RECOVERY_UUID
```

The reference records authorization; it cannot authenticate human intention and
must not contain credentials. An identical UUID/input returns the same receipt;
changed input is refused. Concurrent applications have one winner. If the write
reply is lost, query that UUID before another write; never retry automatically
with a new ID. If no receipt exists, recheck the current inspection and authority
before explicitly recovering the same request.

A recovered workflow task becomes `stopped`. To continue later stages:

```sh
node scripts/launch.mjs execute --task TASK_ID --resume
```

The asynchronous equivalent is `dispatch --task TASK_ID --request-id NEW_UUID
--resume`. A completed developer is not rerun. Recovered review findings still
decide whether a bounded fix is needed; a changed polish candidate receives a
review bound to its SHA. Delegate recovery restores only its initial candidate,
without turning a single-run task into a workflow. Recovery does not add budget.

## What is checked

- A pending, immutable Go completion record under the original frozen contract.
- No observable process or process group for the recorded local identity. Checks
  use signal 0 only. A live or reused PID, another host or uncheckable identity
  blocks recovery; the recovery API never sends a stopping signal.
- The frozen Context and Profiles, branch, clean linked worktree and exact SHA.
- The executor's provider/session identity and exact private history digest.
- Closed request policies, settled or canceled requests, no observed overrun,
  no other active or unknown Run/Session, and no conflicting worktree claim.
- The exact current SQLite evidence at application, within a lease-fenced short
  transaction. State changes make the proposal stale.

No transaction waits for Git, executor or network operations. Git/files and
SQLite are not one atomic resource: application checks physical evidence first,
then compares database evidence transactionally; explicit continuation checks
the physical evidence again before executing. Do not externally modify a task's
worktree or history while recovering it.

## MCP, display and backup

The model-only tools are `inspect_recovery`, `apply_recovery` and
`get_recovery_decision`. They call the same owner-controlled Go service over its
private socket. The MCP App remains read-only; Tasks shows pending/recovered
completion summaries and distinguishes missing evidence from a recovery receipt.
Raw history, process identity, authorization references and authority digests
are not in the monitor.

SQLite V8 preserves private completion records and durable recovery decisions.
Supported V1–V7 stores migrate without certifying old unknown runs. Consistent
backups validate pending, settled and recovered records and include session
files; conflicting or corrupt evidence is refused. Restore into a new private
directory. A copied lease still pointing to a live controller is refused rather
than cleared: stop the original controller before using that restored store.
Keep newer records when investigating an older backup; do not overwrite a live
store with it.

The installed-Pi checks use an isolated loopback model with dummy credentials.
Paid execution and native rendering of the new detail remain separate
acceptance items in [the full plan](../design/session-budget-coordination-20261003.md)
and [Codex host evidence](codex-ui-acceptance.md).

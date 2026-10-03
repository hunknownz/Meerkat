# Run controls and receipts

`0.4.0-beta.6` records one explicit bounded wrap-up instruction for an exact
owned Run and Session. It also reads retained stop receipts without sending
another stop. Go owns the control ledger; Pi is the implemented adapter.

## Request and query

Inspect the current Run and its active Session in the snapshot. After actual
user authorization, use a stable request UUID:

```sh
node scripts/launch.mjs control wrap-up --run <run-id> --session <session-id> \
  --request-id <uuid> --authorization <actual-authorization-reference> --apply
node scripts/launch.mjs control receipt --request-id <uuid>
```

MCP equivalents are `request_wrap_up` and `get_control_receipt`. They are
model-only tools; the Codex monitor stays read-only. The authorization reference
records evidence, not human authentication. It must not contain credentials.
No arbitrary message, new requirement, extra budget or resumed task is accepted.
The instruction uses the fixed wrap-up text and retains the original contract.

| Receipt | Meaning |
| --- | --- |
| `accepted` | Go saved the instruction; no send is confirmed yet. |
| `sending` | Go saved the send intention before the protocol write. |
| `acknowledged` | Pi replied `queued` or `handled`. |
| `rejected` | Not sent, already requested, contract changed, or definitely refused. The reason specifies which. |
| `unknown` | Service or protocol interruption prevents a confirmed result. No automatic resend. |
| `processed` | Existing stop ledger processed the request; inspect its outcome. |

`runState` is the current observed Run state. `outcome` stays null until there
is a terminal or unknown Run result. A queued/handled acknowledgement does not
prove the Agent followed the instruction, finished the task or exited. A stop
whose outcome is unknown does not prove process exit. Inspect the candidate SHA,
checks, session and process evidence separately.

## Disconnect, duplicates and restart

After a missing reply, query the same UUID before deciding another write. The
query neither sends nor resumes anything. Identical accepted inputs return the
original record even after the Run ended. Changed inputs, reused IDs and a
second explicit wrap-up UUID for the same Run are refused. Stop and wrap-up IDs
share a namespace. A missing receipt grants no permission to resend.

On restart, unfinished accepted/sending wrap-ups become unknown and are never
replayed. A verified Run exit closes an unsent instruction as rejected; an
unfinished send becomes unknown. Unknown controls block completed-step
recovery. Investigation or a new frozen task remains the coordinator's job.

Pi coalesces the explicit request with the existing automatic budget wrap-up:
after either has been requested successfully, it does not send another wrap-up
in that Run. Automatic budget notifications retain their existing Run events;
they are not explicit-authority records in this ledger. The hard deadline and
request gate still apply, including any model call triggered by steering.

## History and display

SQLite V9 adds immutable Run/Session/contract bindings and mutable protocol
receipts. V1–V8 stores and backups migrate without inventing old controls.
Backup validation rejects corrupt or conflicting records. Current Task details
show the latest 50 control/retained stop summaries. The stop ledger keeps its
existing limit of processed receipts (`MaxStopReceipts`); it is not an unlimited
audit archive. Explicit wrap-up records are retained in SQLite and backups.

Public summaries exclude authorization references, private histories, authority
digests and raw protocol errors. New control details have automated render and
installed-resource checks; their refreshed native Codex rendering still needs
host acceptance. The previously accepted base panel remains a separate result.

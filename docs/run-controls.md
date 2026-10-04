# Run controls and receipts

Controls bind to an exact owned Run and Session. Go owns durable receipts;
Pi is the implemented adapter. Receipt queries never send a control again.

## Pause and follow-up (beta.13)

Pause requests a graceful end at a verified idle boundary. It does not discard
working changes or start another Run. Continue only when the Task has a verified
checkpoint, using `execute --task <id> --resume` (workflow) or
`run --task <id> --resume` (developer-only). Original usage and frozen limits carry
forward. Unknown tools, requests, controls or process identity block continuation.
A candidate already completed and verified remains a candidate.

```sh
node scripts/launch.mjs control pause --run <run-id> --session <session-id> \
  --request-id <uuid> --authorization <actual-authorization-reference> --apply
node scripts/launch.mjs control follow-up --run <run-id> --session <session-id> \
  --request-id <uuid> --authorization <actual-authorization-reference> \
  --input /private/direction.txt --apply
node scripts/launch.mjs control receipt --request-id <uuid>
```

The follow-up file contains bounded plain text, not JSON. Pi handles it after
the current turn, in the same Session. It grants no new scope, budget or external
permission. Pause refuses later directions and rejects earlier directions that
were definitely not sent. In-flight or acknowledged directions retain their
actual receipts. Pending follow-ups must drain before a normal Run shutdown.
Hard stop remains separate and may abort immediately.

The Codex panel offers app-only `pause_run_from_ui` and
`queue_follow_up_from_ui`. They are not model-visible tools. Its timing selector
chooses steering after the current tools or a follow-up after the current turn.

## Request and query

Inspect the current Run and its active Session in the snapshot. After actual
user authorization, use a stable request UUID:

```sh
node scripts/launch.mjs control wrap-up --run <run-id> --session <session-id> \
  --request-id <uuid> --authorization <actual-authorization-reference> --apply
node scripts/launch.mjs control receipt --request-id <uuid>
```

MCP equivalents are `request_wrap_up` and `get_control_receipt`. They are
model-only tools. Beta.9 also supplies a separate [human intervention](ui-intervention.md) surface in the monitor. The authorization reference
records evidence, not human authentication. It must not contain credentials.
The wrap-up tool accepts no arbitrary message, new requirement, extra budget or resumed task.
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

SQLite V11 extends control kinds and preserves existing payloads and ordering.
V12 adds a ledger compatibility boundary for definite rate-limit rejections.
Older supported stores and backups migrate without inventing old controls.
Backup validation rejects corrupt or conflicting records. Current Task details
show the latest 50 control/retained stop summaries. The stop ledger keeps its
existing limit of processed receipts (`MaxStopReceipts`); it is not an unlimited
audit archive. Explicit wrap-up records are retained in SQLite and backups.

Public summaries exclude authorization references, private histories, authority
digests and raw protocol errors. New control details have automated render and
installed-resource checks; their refreshed native Codex rendering still needs
host acceptance. The previously accepted base panel remains a separate result.

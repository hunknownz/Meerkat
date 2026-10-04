# Diagnose local setup

Run this before a coding task or when a service/configuration problem is unclear:

```sh
node scripts/launch.mjs doctor
node scripts/launch.mjs doctor --profile /Users/me/.meerkat/profiles/example.json
node scripts/launch.mjs doctor --profile /Users/me/.meerkat/profiles/example.json --probe-executor
```

`--data-dir /absolute/private/path` selects another state directory. A missing
directory is reported without creating it. `--profile` inspects only the selected
private config using prepare's validation; it never creates a task.

Default checks read the private socket's service version and a read-only SQLite
snapshot: file privacy, schema, integrity, foreign keys, lease heartbeat and
Run/session/request/control state counts. Older supported databases are not
migrated; groups absent from their schema remain `null`. The database and
permissions are not repaired. SQLite may use reader locks/shared memory for a
live WAL, but doctor does not change rows or schema.

Pi diagnostics locate the executable and check an explicitly configured isolated
model's API and credential reference. Profiles using external Pi configuration
receive a warning because that selected provider/API was not inspected.
`--probe-executor` requires `--profile`. It runs the resolved executable with
`--version` only, in a temporary directory and without inherited credentials,
model/task flags or the configured agent directory. Interpreters and unsupported
command wrappers are refused. The probe is bounded to three seconds. It does not
send a model prompt or contact a provider to test credentials or balance.

## Read the result

The JSON envelope stays `{ "ok": ..., "data": ... }`. The report has
`schemaVersion: 1`, CLI/service versions, aggregate evidence and named checks.

| Check status | Meaning |
| --- | --- |
| `ok` | This check has matching evidence. |
| `warning` | Setup or unsettled state needs attention; read the next action. |
| `blocked` | Unsafe, unsupported, corrupt or uncertain evidence prevents this check passing. |
| `not_checked` | This invocation did not verify that property. |

Exit code `0` means there are no blocking findings; warnings may still be present.
Exit code `2` means blocked diagnostics or invalid flags. Each finding includes a
fixed message and, when needed, a next action. Raw provider/process errors, keys,
transcripts and controller tokens are excluded.

`executionReadiness` is always `not_verified`. Matching CLI/Pi versions and model
configuration do not prove that credentials work or a request-budget gate is live.
The executor verifies the gate handshake before each run's first prompt. Stored
counts do not validate a session history, frozen task contract or process identity;
execution and [explicit recovery](session-recovery.md) perform those checks.

Unknown outcomes remain unknown. Pending records without a confirmed service
owner are blocked. Doctor never replays, resumes, resets, removes a socket or
signals a recorded PID. A finding about one uncertain task does not authorize
changing its state or prohibit independently verified work in another worktree.

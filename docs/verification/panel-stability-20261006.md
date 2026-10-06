# Native panel stability — 2026-10-06

Candidate: `0.4.0-beta.19`. The refresh repair is implemented, installed and
locally verified. The reported native renderer crash is **not yet resolved by
evidence**. Native reload and longer observation remain open.

## Incident and limits

The user reported another gray native Meerkat panel after restarting Codex.
A local Crashpad sidecar records a renderer crash. It does not identify the
Meerkat frame or establish a cause. The daemon was idle and readable, SQLite
passed integrity and foreign-key checks, and its 44 Tasks / 75 Runs remained
intact. Four historical uncertain Runs remain unknown. They were not replayed.

After the user reopened the tool panel, native DOM access and a switch to Tasks
worked. Later native automation timed out again. Reopening is recovery evidence,
not proof of a permanent repair or a diagnosis of the later timeout.

An actual public snapshot contained about 618 KB of history. Beta.18 transmitted
the whole snapshot every four seconds, including when idle. That is a measured
resource burden; neither the crash sidecar nor the protocol measurements prove
it caused the renderer crash.

## Changes

- The app-only `get_monitor_snapshot` accepts an optional state revision. When
  state is unchanged, it returns that revision, observation time and controller
  heartbeat. It omits the repeated history.
- Observation time and heartbeat are excluded from the revision. Every other
  public field participates, including events, controls, SHAs, usage and
  controller state. A change or revision mismatch returns the full snapshot.
- The React transport keeps one validated history and reuses its arrays on an
  unchanged reply. Missing or invalid revisions, time or controller state reset
  the conditional read; the next request obtains full state.
- Active or uncertain state polls every four seconds. Confirmed idle state
  polls every fifteen seconds. Document visibility suspends hidden-panel polls;
  visibility return requests fresh state immediately. This changes panel
  refresh only, not Agent execution.
- Teardown removes the visibility listener, aborts bounded requests, clears
  timers and releases the cached snapshot. Legacy zero-argument readers still
  receive complete snapshots. Control tools and their UUID receipts are unchanged.

No history is truncated and no new writable state or model call is introduced.

## Local verification

- Frontend TypeScript check, all three builds and 46 tests passed. Tests cover
  conditional reads, malformed/mismatched recovery, unknown usage, known-idle
  timing, hidden-before-handshake behavior and repeated native mount/teardown.
- A four-hour **simulated-time** polling test retains the same history references
  and sends only snapshot reads. It is not a four-hour native host observation.
- Go MCP race tests, MCP CLI race tests and MCP vet passed.
- The 23 installation, release and setup tests passed.
- Read-only MCP stdio against the existing live daemon returned all 44 Tasks /
  75 Runs. First reply: 618,777 bytes. Twenty unchanged replies: 589–591 bytes;
  observation time advanced. A mismatched revision returned full history again
  (618,778 bytes). Credential and private-authority fields were absent.

Raw measurements and incident material remain in private maintainer evidence.
No paid provider request was made for this repair.

## Installed verification

Implementation commit: `07d85618b7c03de8abd3b1f7a56744829aaba4ea`.
Codex lists beta.19 as installed and enabled. The matching live runtime has
SHA-256 `974ce38d27ece6d974b47f196c1312beb919695a4b7d4a69669cde79460d1cd5`.
The actual cached stdio launcher discovered 23 tools, including the conditional
read schema, and loaded the exact committed MCP App HTML. Resource SHA-256:
`3677a9dc2140d86a3f67703fa9ac99308900564bec55b9e3604aace307728f51`.

Against the upgraded live daemon, the installed launcher returned all 44 Tasks /
75 Runs. Twenty unchanged replies measured 588–591 bytes, advanced freshness
and recovered full history on revision mismatch. Installed protocol checks do
not establish that an already open host panel loaded the new resource.

The confirmed idle beta.18 owner exited before backup and cutover. A consistent
backup and fresh beta.19 restore passed integrity/foreign-key checks. All 27
non-lease authority tables matched exactly before and after the live cutover,
including history, usage, controls, budgets and sessions. Credential references
were reused; historical unknown state remained unknown. Doctor confirms healthy
service/storage and still blocks uncertain historical execution. No override or
automatic replay was applied.

Disposable macOS installation, service reuse, private IPC, cached MCP resource,
Pi version/configuration probe and a local simulated delivery task passed. That
task exercised four local fixture requests; it did not call ZenMux or establish
paid-model/native UI acceptance. The original backup/runtime are retained.

## Remaining acceptance

- Reload the real Codex host and check the changed native transport, views,
  freshness and retained read-only/control boundaries.
- Observe the real native panel over time and investigate any recurring crash.
  Local tests and reduced traffic alone do not close this gate.
- The previously deferred Windows device check remains separate. Public release
  and directory submission remain separate authorized actions.

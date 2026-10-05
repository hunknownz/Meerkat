# Codex MCP Apps acceptance

Date: 2026-10-03, Asia/Shanghai.

## Verified host path

The installed `0.4.0-beta.1` plugin's `open_monitor` tool opened Meerkat in the
real Codex side panel. The user supplied a screenshot showing the Codex chat and
Meerkat tab together. The MCP Apps automation backend then found the expanded
Meerkat app and verified its DOM directly.

Observed checks:

- Agents / Tasks / Usage render and switch inside the host.
- The host obtains live snapshots through `get_monitor_snapshot`; the controller
  heartbeat advanced during verification and the connection remained connected.
- Task titles, candidate SHAs and delivery states load from the existing service.
- Usage preserves missing fees as unknown and separates wall time from Agent time.
- Settings show the read-only explanation; concurrency, fix rounds and save are disabled.
- No CDP port, application modification or in-app browser was used for this acceptance.

The service had 14 tasks, 21 historical runs and no active, queued or unknown runs.
Raw screenshots remain in private maintainer evidence storage, outside Git. The
user-supplied host screenshot SHA-256 is
`554e7c65e1d1de5b3c7541cc7388ceb53d28e1169cf70213a88282c0b772402b`.

## Boundaries

This verifies the standard MCP Apps panel on the observed Codex installation.
It does not establish a permanent custom sidebar item, the experimental CDP
connector, every host version or a paid coding task. The full session/budget
redesign remains tracked in [the complete plan](../design/session-budget-coordination-20261003.md).

`0.4.0-beta.2` adds session and request-budget summaries using the same React
monitor and MCP host transport. Its build and automated checks are recorded in
[the delivery record](../design/stage-budget-20261003.md). A newly installed plugin
may require a new chat or host restart to reload its cached stdio server and UI.

## Installed beta.2 verification

The GitHub marketplace was pinned to commit
`39177979a0b8023a40ec216b1cc4f3d3bba599d0` and installed as `0.4.0-beta.2`.
The installed plugin's stdio launcher completed `initialize`, `tools/list`,
`resources/read` and `tools/call open_monitor` against the upgraded service.
The resource MIME type is `text/html;profile=mcp-app`; its HTML matches the
committed asset byte for byte, with SHA-256
`a966ae8f2b5e4b11ffaaf8775389558ee02608313c0eb88d9e66b2044f58b288`.
Its CSP lists no external connection or resource domains. The monitor response
passed checks for absent credential and private session fields.

The installed runtime reports `0.4.0-beta.2`, and its binary SHA-256 is
`31e2deddd1334c58cb9b685511b86972a3c862452f984bdca86412be6e589bcf`.
Before service cutover, the old runtime exported a consistent backup; the new
runtime restored it into an isolated data directory. Both restored and live
databases passed integrity checks at schema V5. Live history before and after
cutover matched for 14 tasks, 21 runs and their usage, 11 deliveries and 6 reviews.
No paid model request was made during this verification.

These installed-transport checks supplement the host evidence above. They do not
claim that an already open Codex panel reloaded beta.2; that requires reopening
the panel in a fresh chat or restarting the host.

## Installed beta.3 verification

The GitHub marketplace was pinned to implementation commit
`ab2f9df2680870cbd9197a3a3eedcfb53ac825eb`, installed as `0.4.0-beta.3`.
The installed stdio server reports that version; initialization, tool discovery,
resource loading and `open_monitor` succeeded against the real local service.
Its MCP App HTML matches the committed asset, includes the checkpoint detail
code and has SHA-256
`38e829dbbab744f75dd144383ebe736c3123edca828c517f706b831e9eb7997d`.
Private credential and authority fields were absent from the monitor response.

The runtime's binary SHA-256 is
`e19da210348375297a508d062918956dfa22a3dcfb1fea26a3640a34ebb834c4`.
The stopped beta.2 runtime made a consistent schema V5 backup. The beta.3 runtime
restored it into a fresh isolated directory; restored and live schema V6 databases
passed integrity and foreign-key checks. The 14 tasks, 21 runs, 11 deliveries,
6 reviews, profiles, contexts, settings and cumulative usage matched exactly
before and after cutover. No paid provider request was sent.

The expanded native monitor was reachable and showed its three views and the
updated live heartbeat after cutover. Its cached UI version was not established;
the new checkpoint detail has no native display acceptance yet. Installed resource
checks and frontend tests do not replace that remaining check. Reopen the plugin
in a new chat or restart Codex to load the newly installed stdio server and UI.

## Installed beta.4 verification

The marketplace is now pinned to implementation commit
`583a109cde4d24e09a60f0241bb1535ebb8b50be`; the installed plugin, stdio server
and running service report `0.4.0-beta.4`. Installed initialization, all 13 tools,
resource loading and `open_monitor` passed. The new budget tools are model-only;
the monitor has no budget-application controls or private authorization fields.
The exact committed MCP App HTML has SHA-256
`caf3b389d6534656575042408ce12e68bf833ab9aa360e59a3640d2d145fc332`.
The runtime binary has SHA-256
`a15588ee0776a9f870e2bc3215ca08d420dc61429fd039db13343441561fb02f`.

The idle beta.3 service made a schema V6 backup before cutover. Isolated restore
and the live schema V7 store passed integrity/foreign-key checks; the original
14 tasks, 21 runs, 11 deliveries, 6 reviews, profiles, contexts, settings and
usage matched exactly. No live task received additional budget. No paid model
request was made. Details are in [the delivery record](../design/budget-decisions-20261003.md).

This verifies the installed transport and resource. It does not establish that
an already open Codex panel reloaded the new budget/checkpoint detail. Reopen in a
fresh chat to load the new stdio tools and UI; native detail acceptance remains
separate from the already accepted base monitor.

## Installed beta.5 verification

The marketplace is pinned to implementation commit
`d74cdcc34d175c4d2afcbb6a4b7de2bf8ac7a5c7`. The installed plugin, stdio server
and running service report `0.4.0-beta.5`. Initialization, discovery of all
16 tools, resource loading, `open_monitor` and read-only recovery inspection
passed. The three recovery tools are model-only; Tasks exposes summaries without
recovery controls or private authority. Installed HTML matches the committed
resource byte for byte, with SHA-256
`3d7b09db1dc0a9fd06d0569efbddf56ae625d671da34140b0927e7129f1e40fe`.
The installed runtime binary SHA-256 is
`6a6c5999336d141c92ebc80e5185acd1714bbe645e55c5f6dae3a993981a8f9f`.

A stopped beta.4 schema V7 backup, isolated beta.5 restore and live schema V8
store passed integrity/foreign-key checks. The original 14 tasks, 21 runs and
usage, 11 deliveries, 6 reviews, profiles, contexts and settings matched exactly.
Issue receipt body bytes were also preserved, with its restored private path
relocated. No live task was recovered or given additional budget. See
[the delivery record](../design/session-recovery-20261003.md).

These are installed-transport checks. Native rendering of the new recovery detail
has not been accepted; an already expanded panel may cache an earlier resource.
Open Meerkat in a fresh chat to load the new tools and UI. Paid model execution
remains paused as requested.

## Beta.6 bounded control details (2026-10-03)

Plugin and live service are now `0.4.0-beta.6`, installed from implementation
commit `5b0cc0afa1d46d8546cf09cb535407d3cbc6fe62`. All 18 MCP tools, model-only
control visibility, resource loading, live snapshots, read-only control receipt
lookup and authority filtering passed installed stdio checks. The UI adds public
control receipts while keeping the monitor read-only; 22 frontend tests passed.

A stopped V8 backup, isolated V9 restore and live migration preserved all history
and usage. Installation evidence and hashes are recorded in
[the bounded control record](../design/durable-controls-20261003.md).

The base native panel acceptance remains the earlier result. New control details
have not yet been accepted inside a freshly loaded real Codex panel. Existing
panels may still hold old resources: start a new chat and reopen `open_monitor`
for this version. No browser, stdio or generated-resource result is counted as
new native rendering evidence. Paid full delivery remains deferred.

## Beta.11 startup repair (2026-10-04)

Codex reported a failed MCP initialization while beta.10's direct stdio probe
worked. Its launch command resolved the script under an unrelated project rather
than the installed plugin. Beta.11 supplies a plugin-relative `cwd` and forwards
the host Node path; see the [launch repair record](verification/mcp-launch-root-20261004.md).
Earlier native screenshots remain historical evidence. The fresh panel check
after this repair is recorded below.

## Beta.11 native panel verification (2026-10-04)

After restarting Codex, the user expanded `open_monitor` into the side panel.
The MCP Apps automation backend found the expanded Meerkat app on the native
`codex-sandbox` surface. Its DOM and screenshots supplied the following evidence:

- Agents, Tasks and Usage render and switch in the real host. The connection
  stays connected and the coordinator heartbeat advances.
- A delivered Task opens with separate execution, phase and delivery states,
  its exact candidate SHA, frozen Context digest, checks and review results.
- Usage displays input, output and cache categories, preserves partial totals
  as lower bounds and missing fees as unknown, and separates wall span from
  summed Agent execution time.
- Settings display their read-only explanation and the save button is disabled.

The service had 23 Tasks, 32 Runs, no active or queued Runs and one unknown Run.
No paid request, instruction, stop, recovery or budget change was submitted.
No CDP port, application modification or in-app browser was used. Screenshots
remain in private maintainer evidence storage, outside Git:

| Native screenshot | SHA-256 |
| --- | --- |
| Agents | `740a109dcc348da4f3174ed383041b79c788d1ecb34f03510e409ff232f92e8e` |
| Task detail | `95a8d276b48e4395e662bf9c6987f9797021b2ec1e531cbb484b5342b7b03a8a` |
| Usage | `2993c5a6cb1d2460dc73e73b689fe92ea34da434f9e115b1be3836c9de5b822e` |
| Read-only settings | `5273af2cd865ac447a701571d194615d35dad67f88d16a8dc0fe067c7a7396b0` |

### Checks pending at the initial beta.11 inspection

Human instruction delivery and stopping an active Run still need native host
acceptance. There was no active Run during this check. An attempt to inspect the
unknown Run's controls timed out in the automation backend; subsequent DOM and
screenshot access also timed out. This supplies no acceptance evidence for those
controls and does not establish a product failure. The unknown Run remains
unknown and has not been replayed. The expanded panel was not closed.

Visibility of a fresh global sidebar entry also remains unverified. This check
accepts the expanded tool panel on the observed Codex installation, not every
host entrypoint or the entire delivery plan.

## Beta.11 global entry and native controls follow-up (2026-10-04)

The user's later screenshot shows the selected global Meerkat sidebar entry,
full-page Agents view and connected state. Global entry visibility and rendering
are now accepted on this Codex installation. Screenshot SHA-256:
`58677f79249a358cd9960ba84a9caf3924335b0ccfdca2ceeadee0f8a49f2b28`.

The expanded MCP App's DOM became reachable again. Its unknown Run's input,
send and stop controls were disabled. Two frozen local protocol fixtures then
passed native instruction submission, receipt lookup without resend, same-session
proof delivery, native stopping, retained Task receipts and confirmed child exit.
Three native screenshots and exact IDs are in
[the control verification record](verification/native-controls-20261004.md).

The fixture used no model or gateway request. It accepts the native control
transport; real Pi/model execution controlled through the native panel still
needs an end-to-end check. Prior real Pi/browser interaction is separate evidence.
The original unknown paid Run remains untouched and the panel remains open.

## Beta.17 real controls and beta.18 display repair (2026-10-05)

After the user's reload, the expanded MCP App was accessible directly in Codex.
Native instruction and follow-up buttons reached the same real Pi Session and
its developer candidate. Native pause preserved staged/unstaged files; explicit
checkpoint continuation reused the Session and finished reviewed delivery.
These are paid executor/native UI results, not local protocol fixtures.

That exercise exposed two display defects: an ended expanded Run disappeared
before its receipt lookup finished, and follow-up/pause Task receipts were
labeled as stop requests. Beta.18 fixes both and is installed; its Go/React
resource and private-state preservation passed checks. After the user's restart
on 2026-10-06, the native panel showed corrected receipt labels. A real Pi Run
was stopped from the native button, with its owned process and children exiting,
draft retained and no candidate. The ended expanded card retained its original
receipt; querying showed unchanged feedback without another write. New controls
were disabled and collapsing the ended card preserved truthful running counts.
Exact IDs, commits, screenshots, usage and Windows CI are in
[the current record](verification/native-controls-beta18-20261005.md).
The Windows device test is deferred; neither CI nor macOS display replaces it.

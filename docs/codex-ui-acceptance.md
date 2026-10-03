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

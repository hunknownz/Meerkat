# MCP launch directory repair (2026-10-04)

## Failure and cause

The installed beta.10 runtime passed a direct stdio probe, but Codex's MCP client
failed during initialization. Its stderr reported `Cannot find module` for
`scripts/launch.mjs` inside an unrelated project. The missing panel was therefore
a transport startup failure, not evidence that the MCP App resource was absent.

The old configuration expected plugin-root variables to survive into a shell and
fell back to `.` when they did not. That fallback used the conversation's working
directory. It also did not explicitly forward Codex's bundled Node path.

## Beta.11 repair

- Set MCP `cwd` to `.`; Codex resolves this against the installed plugin root.
- Launch `./scripts/launch.mjs` from that directory. Remove the shell's plugin-root
  fallback, which could silently select the wrong directory.
- Explicitly forward `CODEX_MCP_NODE_PATH` and the three optional Meerkat runtime,
  binary and private-data path overrides. No provider credentials are forwarded.
- Keep the existing global and thread entrypoint metadata and matching Node
  fallback for non-desktop hosts. Scheduling and storage are unchanged.

## Checks

The launcher regression runs from a plugin path containing spaces, without
`PLUGIN_ROOT` or `CLAUDE_PLUGIN_ROOT`, and with a PATH that cannot find `node`.
It verifies use of the forwarded host Node, the MCP command and an explicit
private-data override. The frontend type check and all three builds passed.
MCP and server tests passed with the Go race detector.
All 66 Node distribution/launcher tests passed with Node 22 on PATH. An initial
run that inherited a broken system Node was superseded by this passing run.

## Installed verification

All four release artifacts were built from committed implementation
`b6596cb47c5c32f14da5fa75a6db716b09825091`. The installed darwin/arm64 binary
reports beta.11 and has SHA-256
`7cf7f02f8dc07922a42d243e03504f46e3337160725e122dc70eb8f4a7b9458c`.
Codex reports the beta.11 plugin installed and enabled. Its resolved MCP
transport points `cwd` at that installed version's directory and explicitly
forwards the host Node path.

Using that resolved transport, the bundled desktop Node and no plugin-root
environment variables, initialization, discovery of 21 tools, resource loading
and `open_monitor` passed against the real service. The returned entrypoint
metadata contains both `global` and `thread`. The actual private browser control
token is absent from both the resource and the monitor result.

The service switched after the old owner exited and an offline backup was made.
All 23 Tasks, 32 Runs, deliveries, reviews, frozen contexts/profiles, settings and
usage matched before and after. The existing unknown Run stayed unknown; it was
not replayed. The first startup attempt met the still-held old lease and was
repeated only after that process exited. No lease was bypassed.

The HTTPS marketplace refresh failed twice on this machine. Installation then
succeeded through Codex's supported Git SSH source for the same GitHub repository.
Before the host reload, this chat's available tools still omitted Meerkat and
there was no expanded MCP App tab. The user was asked to reload Codex after the
installed configuration change.

Installed runtime/protocol probes are separate from native display acceptance.
The monitor already declares both global and thread entrypoints; those appear
only if the host discovers the tools and supports the relevant surface. A
successful stdio probe or browser view does not count as a fresh native panel
screenshot.

No paid model request, task recovery or budget increase is part of this repair.

## Fresh native acceptance after reload

The user restarted Codex and expanded Meerkat. The chat then discovered its
tools, and the MCP Apps backend found the real expanded `codex-sandbox` panel.
Agents / Tasks / Usage, live heartbeat, a delivered Task's exact candidate and
review details, execution/phase/delivery states, usage categories and unknown
fees, and disabled settings save passed native inspection. Four private native
screenshots and their hashes are recorded in
[host acceptance](../codex-ui-acceptance.md#beta11-native-panel-verification-2026-10-04).

There was no active Run. Native instruction and stop delivery remain pending,
as does fresh global sidebar entry visibility. Inspecting the unknown Run's
controls and later screenshot access timed out in the automation backend;
those operations have no passing evidence. The panel was not closed, no
control was submitted and no new model request was made.

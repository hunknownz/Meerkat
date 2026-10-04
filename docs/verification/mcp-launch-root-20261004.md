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

Installed runtime/protocol probes are separate from native display acceptance.
The monitor already declares both global and thread entrypoints; those appear
only if the host discovers the tools and supports the relevant surface. After
the update, reload the plugin in Codex and open Meerkat again. A successful
stdio probe or browser view does not count as a fresh native panel screenshot.

No paid model request, task recovery or budget increase is part of this repair.

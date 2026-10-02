---
name: get-started
description: First-use setup for Meerkat. Install the runtime, configure a private executor profile, start the local service and open the monitor. Use when the user asks to set up, configure or start Meerkat.
---

# Meerkat: get started

Act only after the user asks. Full guide: [install](../../docs/install.md).

## Locate

Find the plugin root relative to this skill (`../..`); it contains `scripts/launch.mjs`.
Requirements: macOS/Linux (arm64/amd64), Node 22+, Git, and Pi 0.99.1
(`npm install -g @earendil-works/pi-coding-agent@0.99.1`). Windows is unsupported.

## Steps

1. Runtime: `node scripts/setup.mjs` downloads and verifies the platform binary
   (`SHA256SUMS`, `release.json`) into `~/.meerkat/runtime/<version>/<os>-<arch>`.
   Use `--artifact-dir` for offline files or `--runtime-dir` for an isolated install.
2. Profile: reuse an existing private profile when suitable. Otherwise use supplied project ID,
   provider, model and credential environment-variable name; ask only for missing values. Run:
   `node scripts/configure.mjs --project-id ID --provider P --model M --auth-env VAR_NAME`
   (custom endpoints add `--base-url URL --api anthropic-messages|openai-completions`).
   It refuses to overwrite an existing profile.
3. Key: tell the user to `export VAR_NAME=...` in their own shell. Never ask for, accept,
   echo or store the key.
4. Service: reuse a running service for the chosen data directory. Otherwise start
   `node scripts/launch.mjs serve --port 47826` with the named key available and `pi` on PATH.
   If the key is unavailable to your environment, ask the user to start it in their shell.
5. Monitor: after install, in a new chat, call the `open_monitor` tool and report the counts
   it shows (agents, tasks, deliveries). If the host lacks MCP Apps, run
   `node scripts/launch.mjs snapshot` and say it is a CLI fallback, not the native panel.

## Next

Use the `delegate` skill for one reviewed-by-you run, or `workflow` for full delivery.

## Rules

- Store no credentials in files, arguments, chat or reports; profiles hold the variable name only.
- Do not push, publish or deploy. Native Codex panel acceptance is still pending; do not claim it works.

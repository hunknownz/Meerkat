# Install and first use

Meerkat `0.4.0-beta.9` is a release candidate by hunknownz: <https://github.com/hunknownz/Meerkat>.
The repository install below works from source. Tag-based binary downloads become
available only when the GitHub release is published.
It is not listed in the OpenAI plugin directory (see [publishing](publishing.md)).

## Requirements

- macOS or Linux on arm64 or amd64. Windows is unsupported (the service uses Unix sockets).
- Node 22, Git and Codex. Source installation also needs Go 1.26+; a published
  binary release removes that Go requirement. Frontend assets are already bundled.
- Pi is currently the only executor:
  `npm install -g @earendil-works/pi-coding-agent@0.99.1`.
  The provider API key stays in your shell environment.

## 1. Install the plugin

From the GitHub repository marketplace:

```sh
codex plugin marketplace add hunknownz/Meerkat --ref main
codex plugin add meerkat@meerkat
```

## 2. Install the runtime binary

Until binary assets are published, build the committed source and install locally:

```sh
git clone https://github.com/hunknownz/Meerkat.git
cd Meerkat
node scripts/build-release.mjs
node scripts/setup.mjs --artifact-dir .dist/releases/0.4.0-beta.9
```

The builder compiles four supported targets from clean committed HEAD. After a
release exists, pin both marketplace and clone to its tag (for example
`--ref v0.4.0-beta.9` / `--branch v0.4.0-beta.9`) and use `node scripts/setup.mjs`
to download binaries. `setup.mjs` validates the selected binary against the release's
`SHA256SUMS` and `release.json`. Defaults:

| Item | Default |
| --- | --- |
| Runtime | `~/.meerkat/runtime/<version>/<os>-<arch>` |
| Private state (SQLite, profiles, runs) | `~/.meerkat` |

Options: `--artifact-dir <dir>` installs offline from already downloaded release files;
`--runtime-dir <dir>` installs into an isolated directory. Absolute overrides:
`MEERKAT_RUNTIME_DIR`, `MEERKAT_DATA_DIR`, and optionally `MEERKAT_BIN` (absolute path to a binary).

Every Go command runs through the launcher, which never builds or downloads:

```sh
node scripts/launch.mjs <command> [--data-dir /private/path]
```

## 3. Configure an executor profile

```sh
node scripts/configure.mjs --project-id example --provider PROVIDER --model MODEL \
  --auth-env MY_PROVIDER_KEY
```

- Choose the provider and model yourself. Without `--base-url`, Pi's existing provider configuration is used.
- Optional flags: `--base-url URL` together with `--api openai-completions`,
  `--pi-command /abs/path/to/pi`, and `--data-dir /abs/private/dir`.
  Endpoints use HTTPS; local gateways may use HTTP on `127.0.0.1` or `::1`.
  HTTP hostnames and LAN addresses are refused.
- With `--base-url`/`--api`, a custom provider is written to an isolated `pi/example/models.json`
  that references the environment variable name only, never the key.
- The profile is written to `~/.meerkat/profiles/example.json`; existing profiles are not overwritten.
- Set the key only in your shell (`export MY_PROVIDER_KEY=...`). Never paste it into a chat.
- Current request-budget support is Pi 0.99.1 text HTTP SSE using `openai-completions`.
  An existing provider must use this API too. Anthropic Messages, WebSocket and
  media inputs are not supported by this release's budget bridge.

For keys managed by a local Magpie gateway, see [Magpie routing](magpie.md).

## 4. Start the service

In a shell where the key is exported and `pi` is on `PATH`:

```sh
node scripts/launch.mjs serve --port 47826
```

It runs in the foreground until terminated.

Check setup before the first task:

```sh
node scripts/launch.mjs doctor --profile /Users/me/.meerkat/profiles/example.json --probe-executor
```

This checks local state/configuration and the isolated executable version without
calling a model. Read warnings and unverified checks; a passing report is not an
execution-readiness guarantee. See [diagnostics](doctor.md).

## 5. Open the monitor

Start a new Codex chat (or restart Codex) after installing, then ask: `打开 Meerkat 面板`.
This calls the MCP Apps tool `open_monitor` (global or per-thread entrypoint); the local stdio monitor
supports bounded human instructions and stopping through app-only host tools; settings stay read-only.
The beta.8 panel and its live snapshots were verified on 2026-10-03; beta.9 human controls need fresh host acceptance. See [human intervention](ui-intervention.md) and [previous host acceptance](codex-ui-acceptance.md).
Hosts without MCP Apps support should use `node scripts/launch.mjs snapshot`; a browser page is not the
native panel. The legacy CDP adapter is optional, see [desktop adapter](../desktop/README.md).

## 6. Run a task

Codex prepares a clean linked worktree and a frozen task JSON outside it:

```json
{
  "project": { "id": "example", "name": "Example" },
  "repository": "/abs/path/to/repo",
  "worktree": "/abs/path/to/clean-linked-worktree",
  "title": "Update README",
  "goal": "...",
  "scope": ["README.md"],
  "acceptance": ["..."],
  "context": { "version": 1, "text": "..." },
  "profiles": {
    "developer": "/Users/me/.meerkat/profiles/example.json",
    "reviewer": "/Users/me/.meerkat/profiles/example.json",
    "polisher": "/Users/me/.meerkat/profiles/example.json"
  },
  "budget": { "maxTokens": 150000, "maxWallSeconds": 600, "maxFixRounds": 1 },
  "changeId": "unique-change-id"
}
```

Full schema: [task input](../skills/workflow/references/task-input.md).

Single run (first local candidate only, unreviewed):

```sh
node scripts/launch.mjs run --input TASK.json --dry-run
node scripts/launch.mjs run --input TASK.json
```

Complete workflow (develop, review, limited fix, polish, recheck, local delivery):

```sh
node scripts/launch.mjs prepare --input TASK.json   # prints the task ID
node scripts/launch.mjs execute --task <task-id>
node scripts/launch.mjs snapshot
```

Remote Issue updates need explicit `--apply` and your authorization; nothing is published automatically.

## Diagnosis

| Symptom | Check |
| --- | --- |
| Binary missing | Rerun `node scripts/setup.mjs`; check `MEERKAT_RUNTIME_DIR`/`MEERKAT_BIN` are absolute. |
| Daemon not reachable | Is `serve` still running? Do service and CLI use the same `--data-dir`/`MEERKAT_DATA_DIR`? |
| Missing environment variable | Export the `--auth-env` name in the shell that started `serve`, then restart it. |
| Pi not found | `pi --version` should report 0.99.1; otherwise reinstall or pass `--pi-command`. |
| No panel in Codex | Start a new chat after install; if the host lacks MCP Apps, use `snapshot`. |

## Update and uninstall

Runtimes are versioned: installing a new version adds a new
`~/.meerkat/runtime/<version>/` directory. When changing the marketplace's pinned
ref, remove its old registration first; Codex otherwise reports that the same
marketplace is already registered from a different source:

```sh
codex plugin marketplace remove meerkat
codex plugin marketplace add hunknownz/Meerkat --ref main
codex plugin add meerkat@meerkat
```

Use a published tag or exact commit instead of `main` when pinning a version.
Install its matching runtime with `setup.mjs`, and open a new chat or restart Codex
to reload the MCP server and UI. Before switching the local service, finish or
stop active work, terminate the old service, and make a consistent backup with
the old runtime's `backup --output /abs/path/to/backup.db` command. Start the new
service with the same private data directory; keep the backup until history and
usage are reconciled.

```sh
codex plugin remove meerkat@meerkat
```

Removing the plugin keeps `~/.meerkat` (SQLite history, profiles); delete it manually if desired.

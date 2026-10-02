# Install and first use

Meerkat `0.4.0-beta.1` is a personal GitHub prerelease by hunknownz: <https://github.com/hunknownz/Meerkat>.
It is not listed in the OpenAI plugin directory (see [publishing](publishing.md)).

## Requirements

- macOS or Linux on arm64 or amd64. Windows is unsupported (the service uses Unix sockets).
- Node 22+, Git and Codex. End users do not need Go or an npm frontend build.
- Pi is currently the only executor:
  `npm install -g @earendil-works/pi-coding-agent@0.99.1`.
  The provider API key stays in your shell environment.

## 1. Install the plugin

From the GitHub repository marketplace, pinned to the release tag:

```sh
codex plugin marketplace add hunknownz/Meerkat --ref v0.4.0-beta.1
codex plugin add meerkat@meerkat
```

## 2. Install the runtime binary

Either use the installed plugin's cached copy of `scripts/setup.mjs`, or a pinned clone:

```sh
git clone --branch v0.4.0-beta.1 https://github.com/hunknownz/Meerkat.git
cd Meerkat
node scripts/setup.mjs
```

`setup.mjs` downloads the binary for your OS/architecture and validates it against the release's
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
  --auth-env MY_PROVIDER_KEY \
  [--base-url https://... --api anthropic-messages|openai-completions] \
  [--pi-command /abs/path/to/pi] [--data-dir /abs/private/dir]
```

- Choose the provider and model yourself. Without `--base-url`, Pi's existing provider configuration is used.
- With `--base-url`/`--api`, a custom provider is written to an isolated `pi/example/models.json`
  that references the environment variable name only, never the key.
- The profile is written to `~/.meerkat/profiles/example.json`; existing profiles are not overwritten.
- Set the key only in your shell (`export MY_PROVIDER_KEY=...`). Never paste it into a chat.

## 4. Start the service

In a shell where the key is exported and `pi` is on `PATH`:

```sh
node scripts/launch.mjs serve --port 47826
```

It runs in the foreground until terminated.

## 5. Open the monitor

Start a new Codex chat (or restart Codex) after installing, then ask: `打开 Meerkat 面板`.
This calls the MCP Apps tool `open_monitor` (global or per-thread entrypoint); the local stdio monitor
is read-only. Acceptance in the native Codex app is **pending**: no screenshot of it has been verified.
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

Runtimes are versioned: installing a new tag adds a new `~/.meerkat/runtime/<version>/` directory.
Update by re-adding the marketplace at the new `--ref` and rerunning `setup.mjs`.

```sh
codex plugin remove meerkat@meerkat
```

Removing the plugin keeps `~/.meerkat` (SQLite history, profiles); delete it manually if desired.

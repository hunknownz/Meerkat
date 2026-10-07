# Install and first use

Meerkat `0.4.0-beta.19` is maintained by [hunknownz](https://github.com/hunknownz/Meerkat).
GitHub installation does not require OpenAI directory review. Public binary assets
are a separate release; source installation works without those assets.

## AI installation

Say **“安装 https://github.com/hunknownz/Meerkat”** to a local-capable AI Agent.
It should read [INSTALL.md](../INSTALL.md), clone a clean checkout and run:

```sh
sh scripts/install.sh
```

Native Windows, from PowerShell:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/install.ps1
```

Both bootstraps find Node 22.19+/Go and privately install missing toolchains from
official HTTPS downloads with published SHA-256 checksums. Git and Codex must be
available. They do not introduce WSL, global npm packages or an administrator
service. Frontend assets are committed; end users do not run Vite.

The installer verifies a published runtime or builds only the current host from
clean committed HEAD when the release is absent. Network failures and invalid
checksums do not silently fall back. It installs Pi 0.99.1 privately, starts/reuses
a matching Go service, and generates a local marketplace with absolute Node,
runtime and data paths. Missing host registration is reported with its command.
A different marketplace or daemon version is preserved for explicit update.

Installer options: `--source`, `--artifact-dir DIR`, `--runtime-dir DIR`,
`--data-dir DIR`, `--codex-command ABS`, `--no-host`, `--no-executor`, `--no-start`.

| Item | Default |
| --- | --- |
| Runtime | `~/.meerkat/runtime/<version>/<os>-<arch>/meerkat` (`meerkat.exe` on Windows) |
| SQLite, profiles and sessions | `~/.meerkat` |
| Private Pi | `~/.meerkat/executors/pi/0.99.1` |
| Generated marketplace | `~/.meerkat/marketplaces/<version>-<sourceSha>` |

On Windows, `~` means the current user's home. Private files use protected
current-user/SYSTEM DACLs; commands use a secured named pipe; executor descendants
use an owned Job Object. Unix keeps its private modes and socket/process groups.
See [platform evidence](verification/installation-20261005.md).

## First configuration

Installation makes no model call and does not choose a provider. Only Pi is
implemented; direct coordinator work is also an [execution choice](execution.md).

```sh
node scripts/configure.mjs --profile-id shared-pi --provider PROVIDER --model MODEL --auth-env MY_PROVIDER_KEY --base-url https://provider.example/v1 --api openai-completions
```

The profile/model files hold the credential variable name, never the value.
Installed Pi uses a Node/CLI array on both OSs; Go sets its isolated directory
without a system `env` executable. Existing profiles are not overwritten.
This creates `~/.meerkat/profiles/shared-pi.json` without a project binding.
Discover safe names with `launch.mjs profile list`. Choose `shared-pi` (or its
absolute path) for each desired role in any task's `profiles`; the
task supplies its own project, repository, worktree and Context. The private Pi
directory holds provider settings, while Go allocates separate task sessions.
`--project-id example` remains an optional binding; using it alone keeps the
legacy `profiles/example.json` behavior. See [migration](execution.md#reusable-execution-profiles)
before replacing any config used by frozen tasks. Setup does not write Meerkat
requirements, skills or credentials into customer repositories.
Overrides: `--data-dir`, `--pi-command`. Pi's native provider configuration can be
used without `--base-url`/`--api`; the budget bridge still requires Pi 0.99.1 text
HTTP SSE with `openai-completions`.

Set the key in your own service environment; never paste it into chat. A service
started without that key needs a deliberate idle restart from that environment.
Foreground startup and version-only diagnostics:

```sh
node scripts/launch.mjs serve --port 47826
node scripts/launch.mjs profile list
node scripts/launch.mjs doctor --profile /absolute/private/profile.json --probe-executor
```

The launcher never builds/downloads. Absolute overrides: `MEERKAT_BIN`,
`MEERKAT_RUNTIME_DIR`, `MEERKAT_DATA_DIR`. Diagnosis/version checks do not prove
credentials, balance or a real task. [Magpie](magpie.md) can manage upstream keys.

## Open and run

Reload Codex or open a new chat and ask **“打开 Meerkat 面板”**. The standard
`open_monitor` MCP App shows Agents / Tasks / Usage and bounded instructions,
pause, follow-up, receipt queries and stop. Settings stay read-only. CDP is optional.

Hosts without MCP Apps can use `snapshot` or the loopback browser page; those are
not native panel evidence. Current [host evidence](codex-ui-acceptance.md) and
[remaining checks](verification/installation-20261005.md).

Codex prepares a clean linked worktree and frozen [task input](../skills/workflow/references/task-input.md):

```sh
node scripts/launch.mjs run --input /private/task.json --dry-run
node scripts/launch.mjs run --input /private/task.json
```

This returns an unreviewed candidate. For automated multi-role local delivery:

```sh
node scripts/launch.mjs prepare --input /private/task.json
node scripts/launch.mjs execute --task TASK_ID
node scripts/launch.mjs snapshot
```

Human acceptance, QA, push, merge and deployment are separate. Issue sending needs
explicit authorization and `--apply`.

## Update

1. Finish/explicitly stop owned work and confirm no active operations. Retain
   unknown histories and allowances unresolved.
2. Shut down the confirmed old service through its terminal or verified current
   identity. Never signal a PID merely because it is in a file. Use the old
   runtime for a consistent `backup --output /absolute/private/backup.db`.
3. Install the new runtime with `--no-host --no-start` if doing steps separately.
4. Remove the old registration only, then rerun the installer:

   ```sh
   codex plugin remove meerkat@meerkat
   codex plugin marketplace remove meerkat
   node scripts/install.mjs
   ```

5. Reload Codex, verify the matching service/native panel and reconcile history.
   Retain the old binary and backup until verified.

Direct repository installation remains available with Node on the host PATH:
`codex plugin marketplace add hunknownz/Meerkat --ref main`, then
`codex plugin add meerkat@meerkat` and runtime setup. Prefer an exact SHA/tag for
repeatability. Plugin install itself does not execute setup hooks.

## Uninstall

`codex plugin remove meerkat@meerkat` removes the plugin. Stop a confirmed service
before removing its runtime. SQLite, profiles and history remain; delete them
only if you intend to discard them.

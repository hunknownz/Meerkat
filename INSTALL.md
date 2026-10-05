# Instructions for an AI installing this repository

When a user says **“安装 https://github.com/hunknownz/Meerkat”**, complete the
steps below in their environment. Installing Meerkat is an authorized local
setup task; it does not authorize a paid model call or an Issue update.

1. Detect the OS and architecture, find Git, Node and the user's Codex CLI. Use
   native Windows; do not introduce WSL. The supported runtime targets are
   macOS, Linux and Windows on amd64/arm64. Verify the Node version is at least
   22.19. The source fallback needs Go 1.26+ and the OS's `tar`.
2. Clone `https://github.com/hunknownz/Meerkat.git` into a user-owned directory.
   Read its root `AGENTS.md` and this file. Record `git rev-parse HEAD`. Reuse a
   checkout only when its remote matches and it is clean. Never reset user work.
3. On Windows run, from the checkout:

   ```powershell
   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/install.ps1
   ```

   This installs missing Node 22 and Go 1.26.0 privately using official HTTPS
   downloads and published SHA-256 checksums. It does not require WSL or an
   administrator install. Git and Codex must already be available; find the
   Codex CLI bundled with the desktop app if `codex` is not on PATH.

   On macOS/Linux:

   ```sh
   sh scripts/install.sh
   ```

   If a prerequisite is missing, use an existing supported runtime first. When
   necessary install an official toolchain in `~/.meerkat/tools/`, verify its
   published checksum, and add its `bin` to this invocation's PATH. Do not change
   the user's global default Node or install global npm dependencies.
4. Read the installer's JSON result. It downloads verified release binaries or
   builds **only the current host target from committed HEAD** when the release
   does not exist. An invalid checksum is fatal; it is not a reason to rebuild.
   Pi 0.99.1 is installed privately; no credentials or model calls are needed.
5. Complete host registration when `host.state` is `needs_registration`. Run the
   returned `codex plugin marketplace add <path>` command using the actual Codex
   executable, then `codex plugin add meerkat@meerkat`. Never silently replace a
   different existing marketplace. Follow [the explicit update procedure](docs/install.md#update).
   The generated local package pins Node and the Go runtime by absolute path,
   so a GUI-launched Codex does not need a shell startup file or `/bin/sh`.
6. Verify `node scripts/launch.mjs doctor` and `snapshot` with the installer's
   data directory/runtime overrides. Start a new Codex chat and invoke
   `open_monitor`. Ask for host display assistance only if the current tools
   cannot access the native panel. A browser screenshot is not host evidence.
7. Report separately: runtime installed; service responding; plugin registered;
   provider configured; first real task verified; native panel verified. Install
   completion must not be described as verified task delivery.

## First provider configuration

Reuse a suitable private profile. Otherwise obtain only the project ID,
provider, model, endpoint and credential **environment-variable name**:

```sh
node scripts/configure.mjs --project-id example --provider PROVIDER --model MODEL --auth-env MY_PROVIDER_KEY --base-url https://provider.example/v1 --api openai-completions
```

The installed Pi uses a Node/CLI command array on both operating systems. The
isolated agent directory is set by Go rather than a system `env` executable.
The key stays in the service environment; do not ask users to paste it into
chat. An installation service started before configuring credentials needs a
deliberate idle service restart from the credential-bearing environment before
an authorized real task. See [install](docs/install.md) and [execution choices](docs/execution.md).

Only the Pi adapter is implemented. Users can choose direct coordinator work,
one delegated run or a full workflow. Other AI hosts can use the CLI and their
own stdio MCP registration; native panel rendering depends on MCP Apps support.

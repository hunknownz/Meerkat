---
name: get-started
description: Install, configure or start Meerkat on macOS, Linux or native Windows. Verify local setup and open the monitor without making a paid model call during installation.
---

# Meerkat setup

Act after the user asks for installation/setup. Find the root relative to this
skill (`../..`) and read [INSTALL.md](../../INSTALL.md), then [install](../../docs/install.md).

1. Detect the OS and prerequisites. Use native Windows:
   `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/install.ps1`.
   macOS/Linux: `sh scripts/install.sh`. A non-Git installed package needs a
   clean repository clone for the source fallback.
2. Read the installation result. Reuse the matching controller; preserve another
   marketplace or service version for explicit idle backup/update. Complete
   reported registration with the actual Codex executable.
3. Reuse a private Profile, or obtain project ID, provider, model, endpoint and
   credential environment-variable **name**. Run `configure.mjs`. Only Pi is
   implemented; the bridge needs Pi 0.99.1 text HTTP SSE/openai-completions.
4. Let the user set the key in the service environment. Never request, print or
   store it. An earlier keyless service needs a deliberate idle restart.
5. Run `node scripts/launch.mjs doctor --profile ABS --probe-executor` with
   matching runtime/data overrides. The version probe calls no model. Investigate
   blocked results; do not resolve/replay old unknown runs as part of setup.
6. Reload/open a new Codex chat and call `open_monitor`. Verify actual native
   display when accessible; otherwise request user assistance for this evidence.
   `snapshot` is a CLI fallback, not native display proof.

Report runtime, service, registration, configuration, real task and native
display separately. Installation does not authorize a paid task. Select
[direct work, delegation or workflow](../../docs/execution.md) after setup.
Remote actions require the user's authorization.

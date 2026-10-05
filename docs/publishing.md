# Publishing

Meerkat has two independent distribution paths. Only the first is used for `0.4.0-beta.18`.

## GitHub repository (current)

- The repository <https://github.com/hunknownz/Meerkat> is its own Codex marketplace
  (`.agents/plugins/marketplace.json`). The recommended AI setup entry is [INSTALL.md](../INSTALL.md); it produces a private local marketplace package with absolute host/runtime paths. Users with Node already on the host PATH can also install with
  `codex plugin marketplace add hunknownz/Meerkat --ref main` and
  `codex plugin add meerkat@meerkat`. No OpenAI registration is needed for this.
- Release assets are raw binaries plus `SHA256SUMS` and `release.json`, built by
  `node scripts/build-release.mjs` from a clean committed HEAD. The builder exports
  `git archive <HEAD>` into a fresh temporary directory, so untracked or ignored files cannot affect
  the binaries, and records that SHA as `sourceSha`.
- Tag `v0.4.0-beta.18` and publish a GitHub prerelease by hand, with explicit maintainer authorization.
  Until then, use the documented source build in [install](install.md); no
  nonexistent release download is required for the repository installation.

## OpenAI plugin directory (not pursued yet)

Per the official docs ([build plugins](https://developers.openai.com/plugins/build/plugins),
[extensions](https://developers.openai.com/plugins/build/extensions),
[submission](https://developers.openai.com/plugins/deploy/submission)):

- An individual identity may submit; organizations need a verified owner or the Apps Management Write permission.
- Directory MCP servers are normally reachable at a public HTTPS URL. Meerkat's MCP server is a local
  stdio process, which requires contacting OpenAI first.

We skip the directory beta for now. Directory review is a separate decision and does not affect Git installs.

## Status

The standard MCP Apps panel and its live snapshots were verified inside Codex on
2026-10-03. On 2026-10-04, the user supplied global-entry evidence and the native
panel's instruction, receipt and stop controls passed local protocol fixtures.
Those fixtures made no model request; combined native-panel and paid-executor
acceptance remains separate. Beta.12 simplifies the shared monitor layout.
See [host acceptance and limits](codex-ui-acceptance.md) and
[UI simplification](../design/ui-simplification-20261004.md) for the current checks.

Beta.13 adds graceful pause and same-session follow-up controls, retained retry
decisions for gateways proving rejection before generation, and richer nullable
metrics export. Real Pi/model pause, continuation and follow-up passed through
the CLI; see [the current evidence](verification/real-controls-20261005.md).
The combined real-executor native button scenario still needs its own evidence;
earlier panel acceptance is not substituted.

# Publishing

Meerkat has two independent distribution paths. Only the first is used for `0.4.0-beta.1`.

## GitHub public prerelease (current)

- The repository <https://github.com/hunknownz/Meerkat> is its own Codex marketplace
  (`.agents/plugins/marketplace.json`). Users install with
  `codex plugin marketplace add hunknownz/Meerkat --ref v0.4.0-beta.1` and
  `codex plugin add meerkat@meerkat`. No OpenAI registration is needed for this.
- Release assets are raw binaries plus `SHA256SUMS` and `release.json`, built by
  `node scripts/build-release.mjs` from a clean committed HEAD. The builder exports
  `git archive <HEAD>` into a fresh temporary directory, so untracked or ignored files cannot affect
  the binaries, and records that SHA as `sourceSha`.
- Tag `v0.4.0-beta.1` and publish a GitHub prerelease by hand, with explicit maintainer authorization.

## OpenAI plugin directory (not pursued yet)

Per the official docs ([build plugins](https://developers.openai.com/plugins/build/plugins),
[extensions](https://developers.openai.com/plugins/build/extensions),
[submission](https://developers.openai.com/plugins/deploy/submission)):

- An individual identity may submit; organizations need a verified owner or the Apps Management Write permission.
- Directory MCP servers are normally reachable at a public HTTPS URL. Meerkat's MCP server is a local
  stdio process, which requires contacting OpenAI first.

We skip the directory beta for now. Directory review is a separate decision and does not affect Git installs.

## Status

The MCP Apps `open_monitor` tool is implemented, but acceptance in the native Codex app is pending:
no screenshot from the real app has been verified, so no sidebar success is claimed.

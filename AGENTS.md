# Meerkat maintenance

Meerkat is a general local agent tool. Keep client requirements, credentials, deployment topology and project profiles outside this repository. Public examples use neutral paths and names.

- Go owns scheduling, state, process lifecycle and CLI. SQLite is the authority for state and history. React consumes the versioned snapshot contract. Node is limited to frontend tooling, executor CLIs and the thin desktop connector.
- Keep scheduling independent of executor protocols. Pi is currently supported; do not advertise unimplemented adapters.
- Freeze each coding task's goal, scope, acceptance, Context and Profiles. Use a clean linked Git worktree for execution. Review the actual diff and meaningful checks before a scoped local commit.
- Preserve unknown usage, fees and process identity. Never replace unknown with zero or replay an uncertain run automatically.
- Local delivery, QA, human check, repository integration and deployment are separate states. Remote actions follow the user's actual authorization.
- Use codex/ for task branches. Build frontend assets before Go builds. Run meaningful checks for the changed surface.
- Codex display has two paths: the standard MCP host adapter (MCP Apps tool `open_monitor`, local stdio, bounded human instructions and stop controls; settings read-only) is the default; the legacy CDP desktop connector is optional, non-default and experimental. The standard panel was verified in Codex on 2026-10-03 (docs/codex-ui-acceptance.md). Recheck changed host surfaces; browser screenshots do not prove the Codex panel works.
- Install docs live in docs/install.md, distribution in docs/publishing.md. Keep commands matching scripts/setup.mjs, configure.mjs and launch.mjs.

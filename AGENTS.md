# Meerkat maintenance

Meerkat is a general local agent tool. Keep client requirements, credentials, deployment topology and project profiles outside this repository. Public examples use neutral paths and names.

- Go owns scheduling, state, process lifecycle and CLI. SQLite is the authority for state and history. React consumes the versioned snapshot contract. Node is limited to frontend tooling, executor CLIs and the thin desktop connector.
- Keep scheduling independent of executor protocols. Pi is currently supported; do not advertise unimplemented adapters.
- Freeze each coding task's goal, scope, acceptance, Context and Profiles. Use a clean linked Git worktree for execution. Review the actual diff and meaningful checks before a scoped local commit.
- Preserve unknown usage, fees and process identity. Never replace unknown with zero or replay an uncertain run automatically.
- Local delivery, QA, human check, repository integration and deployment are separate states. Remote actions follow the user's actual authorization.
- Use codex/ for task branches. Build frontend assets before Go builds. Run meaningful checks for the changed surface.
- The desktop connector is experimental. Browser screenshots do not prove the Codex sidebar works.

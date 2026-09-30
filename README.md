# Meerkat

![Meerkat icon](./assets/meerkat.svg)

Meerkat shows which coding agents are running and what they are doing. Its first developer runtime is Pi: the runner handles one Pi CLI coding task in a clean linked Git worktree, enforces wall-time/token caps, and writes a small private run summary. Codex reviews the resulting commit. No dependencies; needs Node 22+ and the `pi` CLI.

## Concepts and ownership

This is a generic plugin. example-project is its first pilot workspace, with an initial execution profile; the runner has no example-project-specific boundary. The current `example-project` marketplace name identifies a local installation source, not the only workspace the plugin can serve.

| Term | Meaning in this MVP |
| --- | --- |
| **Plugin** (`meerkat`) | The installable package containing a Codex Skill, the runner, execution profiles, and an optional read-only live monitor. Existing private data paths remain stable for compatibility. Installing it does not create a task board. |
| **Workspace** | A human-facing project or product area, such as example-project. It may contain several Git repositories. The MVP has no workspace registry. |
| **Coordinator** (Codex) | The main agent that scopes the task, selects the target repository and base commit, prepares or reuses a task branch and linked worktree, writes the task brief, starts the run, and reviews the result. |
| **Developer** (Pi) | The Pi CLI process launched for one run. It implements in the assigned worktree, runs local checks, and creates one scoped local commit. It does not create worktrees or branches. Its provider/model is a runtime choice, not another agent. |
| **Repository** | One Git root whose branch and commits are checked for the run. A run targets exactly one repository root. A workspace may contain more than one repository. |
| **Execution profile** (`projects/<id>.json`) | The configuration for instructions, provider/model, credential environment-variable name, and limits. Its current `projectId` field names the profile in prompts and summaries; it is not a workspace registry or proof that the selected worktree belongs to a repository. Use a separate profile for each distinct target repository. |
| **Task** | One bounded goal accepted by Codex, whether it comes from the user or an Issue. The runner does not store tasks and the monitor UI does not plan them. |
| **Task brief** (`--task <file>`) | The bounded execution contract: goal, scope, non-scope, and acceptance checks. The runner reads a file; it has no queue. |
| **Task ID** (`--task-id <uuid>`, optional) | An externally supplied UUID (for example from an issue tracker or a legacy dashboard task record). When passed, the runner validates it as a UUID and records it as `taskId` in the run summary. There is no dashboard task drawer. |
| **GitHub Issue** | An optional upstream record for requirements and discussion. This MVP does not read, create, comment on, or synchronize Issues. Codex must translate an Issue into a task brief. |
| **Task branch / worktree** | The branch is a Git ref; the worktree is its linked checkout directory. Codex owns their selection and lifecycle. A clean suitable worktree can be reused for later runs; one run does not imply a new worktree. |
| **Run** | One invocation of the runner and one Pi process. The runner checks the worktree, starts Pi, enforces limits, and records the result. It does not edit code or commit. There is no automatic retry or parallel scheduling. |
| **Run summary** | Private JSON under `<worktree>/.pi-developer/runs/` with time, tokens, outcome, branch, and before/after SHAs. `success` means Pi settled, committed, and left a clean worktree; it does not mean Codex review, QA, merge, Preview, production, or user acceptance passed. |

The machine, agent, model, branch, and worktree are different things: Codex prepares a local worktree, and the local Pi CLI uses the model selected by the execution profile. A nested Git repository needs its own target worktree; a commit inside it does not advance the outer repository's HEAD and cannot satisfy an outer-repository run's commit check.

## Setup

1. Configure the provider in `~/.pi/agent/models.json` so its `apiKey` reads an env var (e.g. `"$ZENMUX_PI_API_KEY"`).
2. `export ZENMUX_PI_API_KEY=...` — never commit, print, or pass it as an argument.
3. Execution profile `projects/<id>.json`: `projectId`, `provider`, `model`, `authEnv` (env var **name**), `instructions` (paths embedded in the prompt), `limits.maxWallSeconds`, `limits.maxTokens` (input + output + cache read/write). Optional `piCommand` array (default `["pi"]`). Token caps are guardrails, not dollar estimates.

`example-project.json` targets the outer example-project Git repository. `example-project-website.json` is a bounded starter profile for the nested Website/CMS monorepo (60,000 tokens, 600 seconds); use it only with a clean linked worktree of that nested repository. It injects the repository's root `AGENTS.md`. Codex must add the applicable package instructions and example-project Development contract to each task brief, then verify the selected Git root and resulting commit. An outer-repository run cannot count a nested-repository commit as its result.

## Example

```bash
git worktree add -b codex/root-readme ../example-project-root-readme HEAD
cat > /tmp/root-readme.md <<'EOF'
Goal: clarify one setup instruction in the outer repository README.
Scope: root README.md only. Non-scope: nested repositories and application code.
Acceptance: the instruction is accurate and there is one scoped local commit.
EOF
P=plugins/meerkat
node $P/scripts/run.mjs --config $P/projects/example-project.json --worktree ../example-project-root-readme --task /tmp/root-readme.md --dry-run
node $P/scripts/run.mjs --config $P/projects/example-project.json --worktree ../example-project-root-readme --task /tmp/root-readme.md
# optional: tag the run summary with an externally supplied task UUID
node $P/scripts/run.mjs --config $P/projects/example-project.json --worktree ../example-project-root-readme --task /tmp/root-readme.md --task-id <task UUID>
```

## Responsibilities

- **Script:** preflight (task/config exist; clean, non-primary worktree root on a non-protected branch; Pi and key present), one `spawn` of Pi (argv, no shell) with `--print --mode json --no-session --no-extensions --no-skills --no-prompt-templates --no-context-files --tools read,bash,edit,write`, streaming token accounting from completed assistant messages, caps, summary. It never edits code, creates branches/worktrees, commits, retries, pushes, or deploys.
- **Pi:** implement, run local checks, make one scoped local commit.
- **Codex:** prepare worktree/task, review the diff, rerun checks, decide next step.

Summary: `<worktree>/.pi-developer/runs/<timestamp>.json` (dir 0700, file 0600; added to `info/exclude` if not ignored). Holds `taskId` (only when `--task-id` was given), model, times, tokens, estimated cost (or `null`), baseline/result SHA, changed paths, exit/settled flags, outcome/reason — no transcript. Exit codes: `0` success, `1` failed/stopped (changes kept), `2` preflight error.

## Limitations

- One task, one run: no retries, queues, model switching, or parallel workers.
- Cost is `null` unless Pi reports a cost for every assistant message.
- Pi runs with your user permissions; the worktree is isolation for Git state only, not a sandbox.

## Live monitor (optional)

A dependency-free, loopback-only, **read-only** status page. It polls `GET /api/active` every 4 seconds and shows the number of running Pi processes and, for each, the task, model, worktree, and elapsed time. When the status cannot be read it shows an explicit unavailable state rather than stale data. It does not plan tasks, create branches or worktrees, or launch Pi or `scripts/run.mjs`; the runner workflow above is unchanged (Codex prepares the worktree and brief, Pi codes and commits, Codex reviews).

Start it (Node 22+, no install or build step) and open the printed URL, e.g. http://127.0.0.1:3000/ :

```bash
node plugins/meerkat/dashboard/server.mjs --port 3000
# optional: keep legacy data somewhere specific
node plugins/meerkat/dashboard/server.mjs --port 3000 --data-dir /path/to/private/dir
```

- `--port <0-65535>`: default `0` picks an ephemeral port; the URL and data directory are printed on start.
- `--data-dir <dir>`: defaults to `~/.codex-pi-developer/dashboard` (never inside the repository). The directory is created `0700`; each collection is a `0600` JSON file written atomically. If an existing data file is unreadable or malformed, the server refuses to start and names the file; it never discards existing data.
- If you set `--data-dir` on the monitor, pass the same directory to `scripts/run.mjs --data-dir <dir>` so the runner's heartbeat appears in that monitor. Use an absolute path when launching them from different directories.
- Binds `127.0.0.1` only.

Legacy compatibility: the old board/config/metrics UI has been removed, but the server still keeps the legacy workspace, repository, agent, task, and run-summary data and HTTP APIs (`GET /api/state`, `GET /api/runs`, `GET|POST /api/{workspaces,repositories,agents,tasks}`, `GET|PUT /api/<collection>/<id>`, `DELETE /api/tasks/<id>`) for existing callers. Writes require `Content-Type: application/json`, are limited to 256 KiB, and are rejected with 403 for a foreign `Origin` or `Sec-Fetch-Site: cross-site`.

### Codex desktop adapter (optional, NON-OFFICIAL)

An experimental adapter can show the same read-only monitor as a "Meerkat" entry in the Codex desktop sidebar. It is not an official Codex plugin API and is not permanent: it attaches over the Chrome DevTools Protocol to a Codex instance you start yourself, relies on version-sensitive DOM selectors that may break after Codex updates, and never modifies `app.asar` or Codex user data.

1. Start the monitor server as above.
2. Separately launch the Codex desktop app from a terminal with a loopback remote-debugging port (`--remote-debugging-address=127.0.0.1 --remote-debugging-port=9222`). It must be started explicitly this way; a normally launched Codex has no debugging port.
3. Run the adapter:

```bash
node plugins/meerkat/desktop/injector.mjs --cdp-port 9222 --status-url http://127.0.0.1:3000/
```

Stop it with Ctrl+C. The adapter polls the status URL itself and only accepts literal loopback addresses; no browser security settings need to be changed.

## Test

```bash
node --test plugins/meerkat/tests/*.test.mjs
```

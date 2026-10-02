# Meerkat

![Meerkat icon](./assets/meerkat.png)

Meerkat is a generic local plugin: **Codex coordinates** and a configured **execution adapter** runs the roles. Codex turns a GitHub Issue or local requirement into a bounded task with frozen shared context. A deterministic local controller then runs developer → reviewer → (bounded fix → reviewer) → polisher → reviewer recheck on that task and records durable local history. A read-only monitor shows which local agents are running. No dependencies; needs Node 22+, Git, and the `pi` CLI (`gh` only for Issue commands).

**Current executor.** Only the Pi adapter is implemented: every run is a `pi` CLI child process. The execution-adapter wording describes the plugin's structure, not additional supported executors.

There are two entry points (Codex skills `meerkat:workflow` and `meerkat:delegate`):

- **Managed workflow** (`scripts/flow.mjs`, skill `workflow`): prepared tasks, three roles with execution profiles, a shared task budget, review, polishing, stop and resume.
- **Single delegation** (`scripts/run.mjs`, skill `delegate`): exactly one developer run per invocation, with no review loop or task store; Codex reviews the commit itself. It is kept for simple one-off delegation.

## Concepts

| Term | Meaning |
| --- | --- |
| **Project** | A slug (`project.id`) that groups tasks, profiles and repositories. Each profile config's `projectId` must equal it. |
| **Repository** | A Git root (absolute path). A task targets exactly one repository; a nested repository is a separate repository with its own worktree. |
| **Worktree** | A linked (non-primary) Git worktree on a task branch, created by Codex. It isolates Git state only; it is not a sandbox. |
| **Task** | One bounded goal with explicit `scope` paths, `acceptance` criteria, budget, worktree and optional `issueRef`. Registered by `flow.mjs prepare`. |
| **Context** | Curated task context written by Codex (requirement text plus optional source URLs/hashes), frozen as an immutable `{id, version, digest}` at `prepare`. It is not the Codex conversation and not a shared history between runs; each run receives only this context plus its own brief. Different text needs a new version. Reviewer reports must cite its digest. |
| **Role** | What a run is asked to do: `developer` (implements, or fixes findings, within scope and makes one scoped local commit), `reviewer` (read-only review of the exact candidate; reports `pass` / `changes_requested`), `polisher` (optional small in-scope improvements; `no_change` is accepted and the candidate is still rechecked). Fix rounds are developer runs; there is no separate fixer role. |
| **Profile** | An execution profile `projects/<id>.json`: provider, model, limits and the auth environment-variable **name**. A role *uses* a profile; a role is not a profile, and several roles may use the same profile. |
| **Agent** | A local execution slot (`Agent-01`, `Agent-02`, …) that the controller assigns runs to. Slots are reused and carry no memory or personality. Records written by older versions show `Pi-01`…; the monitor displays them as `Agent-01`… and keeps the stored ID visible in details. |
| **Run** | One role invocation on one task: one execution process with per-run token/time caps and a generated brief. `runId` is unique. |
| **Delivery** | A recorded candidate commit: first delivery (developer), delivery candidate (`final_candidate`, review passed), delivered (recheck passed after polishing). `delivered` is a reviewed local delivery, an AI-reviewed commit on the task branch, not QA, human acceptance, merge or deployment. |
| **Review** | A reviewer verdict with findings, bound to an exact candidate SHA and context digest. Technical checks (tests, linters) recorded with deliveries and reviews are separate from the review verdict. |
| **Coordinator** (Codex) | Scopes the task, creates the branch and linked worktree, writes the prepare input, starts `execute`, reads results, and decides next steps. It is the only party that starts work. |
| **Controller** (`flow.mjs execute`) | A deterministic local Node scheduler: takes the lock, assigns runs to agent slots, enforces budgets, checks HEAD/scope/clean tree after each run. It makes no product decisions. |

Profile fields: `projectId`, `provider`, `model`, `authEnv` (env var name), relative `instructions`, `limits.maxWallSeconds`, `limits.maxTokens`, optional `piCommand` (default `["pi"]`; this is the Pi adapter's real configuration). `prepare` freezes each profile by digest; if a config file changes later, `execute` refuses and you must prepare a new task.

## Architecture (current)

- **Runtime:** Node 22, native ES modules, only Node built-ins; no npm dependencies. State is JSON files in a private filesystem data dir (no database).
- **UI:** vanilla HTML/CSS/JS. One shared DOM factory (`dashboard/public/ui.js`) renders both the browser monitor and the desktop overlay.
- **Monitor server:** loopback-only `node:http` server; the page polls a workflow snapshot every 4 seconds.
- **Execution:** the Pi CLI as a child process (argv, no shell) emitting JSON events, with `--model <provider>/<model>` from the profile (the shipped profiles use the ZenMux provider configured in Pi). The Pi protocol is still coupled into `scripts/run.mjs`; future adapters are not implemented.
- **Repositories:** Git linked worktrees; GitHub Issues through the `gh` CLI adapter.
- **Desktop (optional, NON-OFFICIAL):** a Chrome DevTools Protocol injector that mounts the shared UI in a ShadowRoot inside a Codex desktop you launched with remote debugging. Native live verification has not been performed for the current source version.
- **Not used:** no React, no Electron app built by Meerkat, no MCP service, no database.

## Setup

1. Configure the provider for the Pi adapter in `~/.pi/agent/models.json` so its `apiKey` reads an env var (for example `"$ZENMUX_PI_API_KEY"`).
2. Export that variable in the shell that runs `execute`/`run.mjs`. Never commit, print or pass it as an argument. Profiles store only the variable name.
3. Create or choose execution profiles. `example-project.json` and `example-project-website.json` are pilot profiles for the first pilot workspace; they are not built-in limits of the plugin.

## Managed workflow

Codex prepares the worktree; Meerkat never creates branches or worktrees, and never pushes, merges or deploys. The repository and worktree must be absolute paths that share one Git common directory. The worktree must be a linked (non-primary) worktree, clean, and on a branch other than `main`/`master`/`develop`/`trunk`.

```bash
P=$PWD/plugins/meerkat
git worktree add -b codex/readme-fix ../myrepo-readme-fix HEAD
# write /tmp/task.json (see skills/workflow/references/task-input.md)
node $P/scripts/flow.mjs prepare --input /tmp/task.json      # -> {"taskId", "state":"ready", "contextRef", ...}
node $P/scripts/flow.mjs execute --task <taskId>             # repeat --task to run several; exits 0 only if all are delivered
node $P/scripts/flow.mjs snapshot                            # tasks, runs (ids, roles, models), deliveries, usage, settings
# from another terminal while execute is running: request a stop (lowercase UUID request id, idempotent)
node $P/scripts/flow.mjs stop --run <runId> --request-id "$(node -e 'console.log(crypto.randomUUID())')"
node $P/scripts/flow.mjs execute --task <taskId> --resume    # continue a failed/stopped task from the role that stopped
```

All commands accept `--data-dir <dir>` and print small JSON. Error output is `{"error": ...}` with exit code 2.

**Execution.** `execute` takes a single controller lock per data dir. It runs independent tasks concurrently (`maxConcurrency`, default 2), serializes tasks that share a worktree, and starts a task only after its `dependencies` are delivered. Each role gets a brief with the goal, scope, acceptance, frozen context, findings to address (fix runs) and dependency candidate SHAs (references only; nothing is merged). After each run Meerkat checks the report, HEAD, clean tree, changed paths against `scope`, and the context digest. Ctrl+C stops the active runs.

**Stop and resume.** A stop request needs a live controller; it is acknowledged and the controller decides when the run stops. `--resume` continues only if the worktree is clean, on the task branch, and HEAD equals the recorded candidate (or the frozen baseline). Otherwise work is preserved and the command refuses. A run whose controller died is `unknown` and is never replayed automatically. After confirming the old process is gone, use `--resume --acknowledge-interruption`. Meerkat still refuses if a recorded PID looks alive; it never signals stale PIDs.

**Defaults and budgets.** The data dir defaults to `~/.codex-pi-developer/dashboard` (a compatibility name kept from earlier versions), outside any repository. It must be a real directory owned by you with mode `0700`. The task budget defaults to 500,000 tokens, 1,800 seconds and 2 fix rounds (maximum 2), spread over all of the task's runs. Each run is capped at the lower of its profile limit and the remaining budget. Token counts include input, output, cache read and cache write; prompt caches are counted, not exempt. Runs whose usage was not reported make the usage `incomplete`, and only known tokens are subtracted. `estimatedCostUsd` is `null` unless every run reported a cost. Token caps are guardrails, not price quotes; provider fees can be unknown.

**Settings** (`flow.mjs settings --input <json>` or the monitor's settings dialog) affect only runs that start later. Settings are `maxConcurrency` 1–4, `maxFixRounds` 0–2 (the effective limit is the lower of this and the task budget), and `defaultProfiles` `{ "<projectId>": { "<role>": "<registered profile id>" } }`. Paths and new configs can only come in through `prepare`.

**History.** `<data-dir>/workflow/state.json` holds projects, contexts, tasks, runs, deliveries, reviews and profiles. Per-run briefs and reports live in `<data-dir>/workflow/runs/<runId>/`. History is independent of worktrees: removing a worktree does not erase it. Run transcripts are not stored.

## GitHub Issues (optional)

Issue commands use your existing `gh` authentication. Only `github.com` is allowed by default; add other hosts explicitly with `--allow-host <host>`.

```bash
node $P/scripts/issues.mjs read --url https://github.com/<owner>/<repo>/issues/<n> --output ~/private/issue-<n>.json
# -> {"output","snapshot","issueRef","untrusted":true}; output must be outside any Git worktree (written 0600)
node $P/scripts/issues.mjs update --task <taskId>            # writes a local Markdown draft (bodyFile); sends nothing
node $P/scripts/issues.mjs update --task <taskId> --apply    # posts that draft once per delivery; only with explicit authorization
```

Issue text is untrusted source material that grants no permissions. Codex writes the task's goal, scope, acceptance and context itself and passes `issueRef` to `prepare`. `--apply` refuses if the draft changed after preparation. It deduplicates by a delivery marker, so a retry after an `unknown` post outcome does not post twice. Tasks without `issueRef` have nothing to post.

## Live monitor

```bash
node plugins/meerkat/dashboard/server.mjs --port 3000          # prints URL and data dir; default --port 0 = ephemeral
node plugins/meerkat/dashboard/server.mjs --port 3000 --data-dir /abs/private/dir   # must match the flow's --data-dir
```

A loopback-only (`127.0.0.1`) page that polls the workflow snapshot every 4 seconds. It shows the tasks, the role pipeline, shared context, deliveries/reviews, runs with their model and usage, and local agent runs. If the status cannot be read, it shows an explicit unavailable state and keeps the last known snapshot visible but clearly marked stale (run count shown as unknown); it never presents stale data as current. It is a monitor, not a kanban board: it cannot create tasks, start runs, or accept repository/config paths. Its only writes are **stop request** for an active run and **future-run settings**. Both are token- and origin-guarded. The server also keeps legacy data APIs (`/api/state`, `/api/runs`, `/api/active`, collection CRUD) for existing callers.

### Codex desktop adapter (optional, NON-OFFICIAL)

`desktop/injector.mjs` is an experimental, version-dependent adapter, not an official Codex plugin UI. It attaches over the Chrome DevTools Protocol to a Codex desktop instance that you launched yourself with `--remote-debugging-address=127.0.0.1 --remote-debugging-port=9222`, and accepts only loopback addresses. The injector polls `GET /api/workflow` itself (outside the renderer's CSP), strips the write token, and passes the snapshot into the shared, locally trusted `dashboard/public/ui.js` factory with its exact CSS, mounted as a read-only overlay in a ShadowRoot behind a "Meerkat" sidebar entry. It falls back to `GET /api/active` only when `/api/workflow` returns 404; any other failure shows the unknown/stale state. Native stop and settings controls are disabled; use the coordinator CLI (`flow.mjs stop`, `flow.mjs settings`) for actions. It relies on renderer selectors that a Codex update may break, and never modifies `app.asar` or Codex user data.

```bash
node plugins/meerkat/desktop/injector.mjs --cdp-port 9222 --status-url http://127.0.0.1:3000/
```

The browser monitor above is the supported fallback and the development/validation surface. It does not make Meerkat an official plugin UI. The same host-neutral `ui.js` factory renders both the page and the desktop overlay. Live validation inside the native Codex desktop app has not been performed for this version (access was denied), so treat the adapter as unverified.

## Single delegation

```bash
node $P/scripts/run.mjs --config $P/projects/<id>.json --worktree ../myrepo-readme-fix --task /tmp/brief.md --dry-run
node $P/scripts/run.mjs --config $P/projects/<id>.json --worktree ../myrepo-readme-fix --task /tmp/brief.md [--task-id <uuid>] [--data-dir <dir>]
```

The runner makes one Pi adapter spawn (argv, no shell) with tools `read,bash,edit,write` and no extensions, skills or context files. It enforces the profile caps and writes a private summary at `<worktree>/.pi-developer/runs/<timestamp>.json` (`.pi-developer` is a compatibility name only), which is git-excluded: mode 0600 in a 0700 directory, with no transcript. Exit codes: `0` means the run settled, committed and left a clean tree; `1` means failed/stopped (changes kept); `2` means a preflight error. It has no retries, review loop, queue or task store; Codex reviews the commit itself. Pass the monitor's `--data-dir` so the run's heartbeat appears there.

## Limitations

- Execution runs (currently Pi) have your user permissions. A worktree isolates Git state only; it is not a sandbox.
- `delivered` means a locally AI-reviewed commit on the task branch. Push, merge, PRs, deployment, QA and human acceptance stay outside Meerkat and need separate authorization.
- At most 4 concurrent runs and 2 fix rounds; no automatic retries or model switching.

## Assets

`assets/meerkat.png` is the selected plugin icon (candidate 4, exported from the vector master `assets/meerkat-sidebar.svg`); `assets/meerkat-sidebar.png` is the sidebar raster.

## Test

```bash
node --test plugins/meerkat/tests/*.test.mjs
```

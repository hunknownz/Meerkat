# Meerkat

![Meerkat icon](./assets/meerkat.png)

Meerkat is a generic local plugin: **Codex coordinates** and **Pi implements**. Codex turns a GitHub Issue or local requirement into a bounded task with frozen shared context. The managed flow runs developer → reviewer → (bounded fix → reviewer) → polisher → reviewer recheck on that task and records durable local history. A read-only monitor shows which agents are running. No dependencies; needs Node 22+, Git, and the `pi` CLI (`gh` only for Issue commands).

There are two entry points:

- **Managed flow** (`scripts/flow.mjs`, skill `meerkat-flow`): prepared tasks, three role profiles, a shared task budget, review, polishing, stop and resume.
- **Legacy standalone runner** (`scripts/run.mjs`, skill `pi-developer`): exactly one Pi developer run per invocation, with no review loop or task store. It is kept for simple one-off delegation.

## Concepts

| Term | Meaning |
| --- | --- |
| **Project** | A slug (`project.id`) that groups tasks, profiles and repositories. Each profile config's `projectId` must equal it. |
| **Repository** | A Git root (absolute path). A task targets exactly one repository; a nested repository is a separate repository with its own worktree. |
| **Context** | Shared requirement text (plus optional source URLs/hashes) frozen as `{id, version, digest}` at `prepare`. A context version is immutable: different text needs a new version. Every run brief embeds it, and reviewer reports must cite its digest. |
| **Task** | One bounded goal with explicit `scope` paths, `acceptance` criteria, budget, worktree and optional `issueRef`. Registered by `flow.mjs prepare`. |
| **Run** | One Pi process for one role on one task, with per-run token/time caps and a generated brief. |
| **Delivery** | A recorded candidate commit: first delivery (developer), final candidate (review passed), delivered (recheck passed after polishing). `delivered` is a local AI-reviewed commit, not QA, human acceptance, merge or deployment. |
| **Review** | A reviewer verdict (`pass` / `changes_requested`) with findings, bound to an exact candidate SHA and context digest. |
| **Coordinator** (Codex) | Scopes the task, creates the branch and linked worktree, writes the prepare input, starts `execute`, reads results, and decides next steps. It is the only party that starts work. |
| **Developer** (Pi) | Implements or fixes findings within scope and makes one scoped local commit. |
| **Reviewer** (Pi) | Read-only check of the exact candidate; reports a verdict. |
| **Polisher** (Pi) | Optional small in-scope improvements. Reporting `no_change` (HEAD untouched) is accepted; the candidate is still rechecked by the reviewer. |

Roles are profiles, not separate products: each role uses an execution profile `projects/<id>.json` (`projectId`, `provider`, `model`, `authEnv` = env var **name**, relative `instructions`, `limits.maxWallSeconds`, `limits.maxTokens`, optional `piCommand`, default `["pi"]`). Several roles may use the same file. `prepare` freezes each profile by digest; if a config file changes later, `execute` refuses and you must prepare a new task.

## Setup

1. Configure the Pi provider in `~/.pi/agent/models.json` so its `apiKey` reads an env var (for example `"$ZENMUX_PI_API_KEY"`).
2. Export that variable in the shell that runs `execute`/`run.mjs`. Never commit, print or pass it as an argument. Profiles store only the variable name.
3. Create or choose execution profiles. `example-project.json` and `example-project-website.json` are pilot profiles for the first pilot workspace; they are not built-in limits of the plugin.

## Managed flow

Codex prepares the worktree; Meerkat never creates branches or worktrees, and never pushes, merges or deploys. The repository and worktree must be absolute paths that share one Git common directory. The worktree must be a linked (non-primary) worktree, clean, and on a branch other than `main`/`master`/`develop`/`trunk`.

```bash
P=$PWD/plugins/meerkat
git worktree add -b codex/readme-fix ../myrepo-readme-fix HEAD
# write /tmp/task.json (see skills/meerkat-flow/references/task-input.md)
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

**Defaults and budgets.** The data dir defaults to `~/.codex-pi-developer/dashboard`, outside any repository. It must be a real directory owned by you with mode `0700`. The task budget defaults to 500,000 tokens, 1,800 seconds and 2 fix rounds (maximum 2), spread over all of the task's runs. Each run is capped at the lower of its profile limit and the remaining budget. Token counts include input, output, cache read and cache write; prompt caches are counted, not exempt. Runs whose usage was not reported make the usage `incomplete`, and only known tokens are subtracted. `estimatedCostUsd` is `null` unless every run reported a cost. Token caps are guardrails, not price quotes; provider fees can be unknown.

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

A loopback-only (`127.0.0.1`) page that polls the workflow snapshot every 4 seconds. It shows the tasks, the role pipeline, shared context, deliveries/reviews, runs with their model and usage, and running Pi agents. If the status cannot be read, it shows an explicit unavailable state and keeps the last known snapshot visible but clearly marked stale (run count shown as unknown); it never presents stale data as current. It is a monitor, not a kanban board: it cannot create tasks, start runs, or accept repository/config paths. Its only writes are **stop request** for an active run and **future-run settings**. Both are token- and origin-guarded. The server also keeps legacy data APIs (`/api/state`, `/api/runs`, `/api/active`, collection CRUD) for existing callers.

### Codex desktop adapter (optional, NON-OFFICIAL)

`desktop/injector.mjs` is an experimental, version-dependent adapter, not an official Codex plugin UI. It attaches over the Chrome DevTools Protocol to a Codex desktop instance that you launched yourself with `--remote-debugging-address=127.0.0.1 --remote-debugging-port=9222`, and accepts only loopback addresses. The injector polls `GET /api/workflow` itself (outside the renderer's CSP), strips the write token, and passes the snapshot into the shared, locally trusted `dashboard/public/ui.js` factory with its exact CSS, mounted as a read-only overlay in a ShadowRoot behind a "Meerkat" sidebar entry. It falls back to `GET /api/active` only when `/api/workflow` returns 404; any other failure shows the unknown/stale state. Native stop and settings controls are disabled; use the coordinator CLI (`flow.mjs stop`, `flow.mjs settings`) for actions. It relies on renderer selectors that a Codex update may break, and never modifies `app.asar` or Codex user data.

```bash
node plugins/meerkat/desktop/injector.mjs --cdp-port 9222 --status-url http://127.0.0.1:3000/
```

The browser monitor above is the supported fallback and the development/validation surface. It does not make Meerkat an official plugin UI. The same host-neutral `ui.js` factory renders both the page and the desktop overlay. Live validation inside the native Codex desktop app has not been performed for this version (access was denied), so treat the adapter as unverified.

## Legacy standalone runner

```bash
node $P/scripts/run.mjs --config $P/projects/<id>.json --worktree ../myrepo-readme-fix --task /tmp/brief.md --dry-run
node $P/scripts/run.mjs --config $P/projects/<id>.json --worktree ../myrepo-readme-fix --task /tmp/brief.md [--task-id <uuid>] [--data-dir <dir>]
```

The runner makes one Pi spawn (argv, no shell) with tools `read,bash,edit,write` and no extensions, skills or context files. It enforces the profile caps and writes a private summary at `<worktree>/.pi-developer/runs/<timestamp>.json`, which is git-excluded: mode 0600 in a 0700 directory, with no transcript. Exit codes: `0` means Pi settled, committed and left a clean tree; `1` means failed/stopped (changes kept); `2` means a preflight error. It has no retries, review loop, queue or task store; Codex reviews the commit itself. Pass the monitor's `--data-dir` so the run's heartbeat appears there.

## Limitations

- Pi runs with your user permissions. A worktree isolates Git state only; it is not a sandbox.
- `delivered` means a locally reviewed commit on the task branch. Push, merge, PRs, deployment, QA and human acceptance stay outside Meerkat and need separate authorization.
- At most 4 concurrent runs and 2 fix rounds; no automatic retries or model switching.

## Assets

`assets/meerkat.png` is the selected plugin icon (candidate 4, exported from the vector master `assets/meerkat-sidebar.svg`); `assets/meerkat-sidebar.png` is the sidebar raster.

## Test

```bash
node --test plugins/meerkat/tests/*.test.mjs
```

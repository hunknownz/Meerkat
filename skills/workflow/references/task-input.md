# `meerkat prepare` input

One JSON object (≤ 1 MiB). Unknown keys are rejected. Repository/worktree paths
are absolute. Profile references accept managed names or private absolute paths.

```json
{
  "project": { "id": "myproject", "name": "My Project" },
  "repository": "/abs/path/myrepo",
  "worktree": "/abs/path/myrepo-readme-fix",
  "title": "Clarify README setup",
  "goal": "Make the setup section accurate for Node 22.",
  "scope": ["README.md"],
  "acceptance": ["Setup steps match package.json engines", "One scoped local commit"],
  "context": { "version": 1, "text": "Shared decisions all roles must follow." },
  "profiles": {
    "developer": "shared-pi",
    "reviewer": "shared-pi",
    "polisher": "shared-pi"
  }
}
```

Required: `project` (`id` lowercase slug ≤ 63, `name`), `repository`, `worktree`, `title`, `goal`, `scope` (1–50 explicit relative paths; no globs, `..`, or secret-like segments), `acceptance` (1–50), `context`, `profiles`. Workflow `prepare` requires all three roles. Delegate `run` and its `--dry-run` require only `developer`; older three-role delegate inputs remain accepted and frozen, but only the developer executes or reserves a Provider slot. A reusable execution config omits `projectId` and works for any task project. An existing config with `projectId` remains restricted to that exact `project.id`.

Optional:

- `context.id` (lowercase UUID) to add a version to an existing context family; `context.sources`: up to 50 `{ "url": "https://…", "title", "hash": "<sha256 hex>" }`. Same id + version with different text is refused.
- `issueRef`: the object printed by `meerkat issue read` (`url`, `title`, optional `updatedAt`, `bodyHash`).
- `dependencies`: task UUIDs of the same project that must be delivered first.
- `budget`: time defaults to 1800 seconds and fix rounds to 2. Token monitoring is the default (`mode: "monitor"`, 500000-token warning threshold). Select a hard cap with `{ "mode": "enforce", "maxTokens": 150000 }`; an explicit `maxTokens` without `mode` also keeps legacy hard-cap behavior. Explicit monitor mode may set a custom warning threshold. Profile limits still cap each Run.

Optional `budget.stageReserves` contains nonnegative `reviewTokens`, `fixTokens`,
`polishTokens`, `wrapUpTokens` and `wrapUpSeconds` (omitted fields default to zero).
Go keeps remaining role allowances and requests early wrap-up within the existing
cap; it rejects reserves that consume the entire task authorization. See
[stage budgets](../../../docs/stage-budgets.md) for a complete example. Zero disables
the corresponding wrap-up trigger. Verified dirty-worktree [checkpoint continuation](../../../docs/checkpoints.md) is available through explicit resume; uncertain state remains blocked.

Optional `changeId` links task and run metrics to one change. Profiles must be private files owned by the current user. Old configs default to executor pi; new ones should declare it explicitly.

## Private Profile example

All roles may use the same file, or separate models and limits. Store it outside Git with mode 0600.

```json
{
  "executor": "pi",
  "provider": "configured-provider-id",
  "model": "configured-model-id",
  "authEnv": "SERVICE_PROVIDER_KEY",
  "instructions": [],
  "piCommand": ["pi"],
  "limits": { "maxTokens": 200000, "maxWallSeconds": 300 }
}
```

The service inherits the API key environment. The profile freezes the environment-variable name and configuration digest, never the credential. Configure the provider in the executor before running. For one delegated run, use `"profiles": { "developer": "shared-pi" }` with the same task fields.

Use `profile list` for safe read-only names, models, limits and legacy binding
metadata. A name such as `shared-pi` resolves to the service data-dir's
`profiles/shared-pi.json`; explicit private absolute paths remain supported.
No default is selected silently. A customer-specific Profile is not required
when a reusable one exists. Each task gets separate project-owned
frozen Profile IDs, Context and sessions even when the source path and digest
match. Project concurrency remains keyed by the task's `project.id`. `instructions`
paths, when used, resolve only inside that task's worktree. Keep shared configs
free of customer requirements and supply the necessary business/role contract
through the task's curated Context. Never install routing rules or private config
references into a customer repository automatically.

For legacy compatibility and migration without changing frozen tasks, see
[reusable execution Profiles](../../../docs/execution.md#reusable-execution-profiles).

Default to `--input -` and stream the JSON through stdin. Prepare/run save the
frozen contract as JSON in the plugin's private SQLite data-dir; no staging file
is needed in a customer tree. If a file is useful for review, save it under
`<data-dir>/inputs/<unique-id>.json` with private permissions, not merely outside
the worktree. Reports, execution records, sessions and control receipts already
live under that data-dir and its database. Use the plugin/service's private
data-dir even when the customer's Git repository is nested inside a larger
project directory. Do not copy its knowledge base into shared execution configs.

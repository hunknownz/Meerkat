# `meerkat prepare` input

One JSON object (≤ 1 MiB). Unknown keys are rejected. Paths are absolute unless noted.

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
    "developer": "/private/meerkat/profiles/myproject.json",
    "reviewer": "/private/meerkat/profiles/myproject.json",
    "polisher": "/private/meerkat/profiles/myproject.json"
  }
}
```

Required: `project` (`id` lowercase slug ≤ 63, `name`), `repository`, `worktree`, `title`, `goal`, `scope` (1–50 explicit relative paths; no globs, `..`, or secret-like segments), `acceptance` (1–50), `context`, `profiles` (all three roles; each config's `projectId` must equal `project.id`).

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
  "projectId": "myproject",
  "executor": "pi",
  "provider": "configured-provider-id",
  "model": "configured-model-id",
  "authEnv": "PROJECT_API_KEY",
  "instructions": [],
  "piCommand": ["pi"],
  "limits": { "maxTokens": 200000, "maxWallSeconds": 300 }
}
```

The service inherits the API key environment. The profile freezes the environment-variable name and configuration digest, never the credential. Configure the provider in the executor before running. A single-delegation input currently includes all three profile references for schema consistency, but only its developer runs.

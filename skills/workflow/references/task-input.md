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
- `budget`: `{ "maxTokens": 500000, "maxWallSeconds": 1800, "maxFixRounds": 2 }` (defaults shown; fix rounds 0–2).

Optional `changeId` links task and run metrics to one change. Profiles must be private files owned by the current user. Old configs default to executor pi; new ones should declare it explicitly.

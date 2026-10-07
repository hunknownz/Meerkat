# Reusable execution Profiles and module boundary — 2026-10-07

Source baseline: `63c6842d541c39280f8894cddd182fcfac0744eb`.
Candidate branch: `codex/reusable-execution-profiles`, in a clean linked worktree
before implementation. Execution was direct coordinator work. No customer
workspace, installed service or private production configuration was modified.

## Owning-source review

The baseline source and cached beta.19 generator still required a project-bound
execution config. Preparation compared its `projectId` to the task project, and
the generator used the project ID to name both config and provider directory.
This was a general execution-selection limitation rather than a missing
customer-specific executor integration.

Several requested boundaries already existed and remain in use:

- Delegate returns `first_delivery` / `delegate_candidate`, a local unreviewed
  candidate. It does not claim customer QA, acceptance, repository integration
  or deployment.
- The candidate/report binds to the expected baseline, actual result SHA and
  frozen Context. Snapshot queries retain scope, reported checks/gaps and usage.
- Go supplies transient reports/bridge material under private `data-dir/runs`;
  it consumes and cleans them after persisting evidence. Persistent sessions,
  execution history and control receipts remain in private data-dir/SQLite.
  The adapter's standalone report-path check alone only excludes the worktree.
- Tasks own project/repository/worktree, Context and scheduling limits. Business
  and role contracts need no plugin-specific routing inside customer files.

## Changes and compatibility

- Private configs may omit `projectId`. Their configuration digest is independent
  of the consuming project. Each prepared task still gets project-owned frozen
  Profile IDs; a private optional `reusable` field preserves the source digest
  after attaching task ownership. Legacy snapshots omit that field and retain
  their prior JSON contract and digest. SQLite schema/public snapshot shape did
  not change.
- `configure.mjs --profile-id <name>` creates a reusable config by default.
  Optional `--project-id` preserves explicit binding; the old flag alone keeps
  the old filename/binding behavior. Existing files are never overwritten.
- Offline `profile list` returns safe managed names, executor/model/provider,
  limits and binding metadata. It reads no credential values, starts no executor,
  creates no database, and exposes no auth reference, command, config content or
  private path. Validation is structural, not provider readiness. Invalid managed
  configs remain visible as invalid. Missing directories return an empty list.
- Task Profile references and `doctor --profile` accept managed names resolving
  in `data-dir/profiles`, as well as legacy absolute private paths. There is no
  silent default/fallback. Externally stored configs are not automatically scanned.
- Delegate requires only developer. Explicitly supplied legacy extra roles remain
  frozen/verified, while Provider reservation covers only its executed developer.
  Workflow still requires all three actual roles and reserves their Providers.
- The thin launcher inserts a selected data-dir after grouped subcommands,
  including profile discovery, rather than between the command and subcommand.
- Skills/docs default to stdin task JSON. The plugin persists its frozen contract
  JSON in SQLite; no staging file is required in a customer tree. Optional private
  review files belong in `data-dir/inputs`, outside the entire customer outer tree.
  No automatic customer AGENTS/skill/config editing or knowledge-base copying was
  introduced. Optional handwritten staging files have no new garbage-collection
  mechanism; stdin avoids that lifecycle entirely.

For migration, create a new private unbound config and select it only for new
tasks. Keep files referenced by older frozen tasks. Changing either a legacy or
reusable source config still yields `profile_changed_requires_prepare`; no task,
session, original budget or unknown Run is adapted or replayed.

## Verification

- Frontend types/check and all three embedded builds passed before the Go binary
  build; existing public assets remained unchanged. The 46 frontend tests passed.
- `go test -race ./...` and `go vet ./...` passed. Targeted race checks passed again
  after strengthening the legacy-digest and changed-reusable-config assertions.
- All 75 Node tests passed with no skips, including config generation, launcher
  forwarding, release/setup regressions and both new CLI integration tests.
- Two independent fixture Git repositories prepared/dry-ran with one generated
  named Pi Profile. Ownership, repository/worktree, change IDs and Context stayed
  separate, while the source digest matched. Workflow froze its three roles;
  developer-only dry-run required one. Missing shared configuration produced the
  actual configuration-selection error rather than a customer-project requirement.
- Local stateful fixtures exercised workflow isolation and two developer-only
  delegates, cross-project Profile/Context rejection, legacy Provider reservation,
  private output projection, unchanged legacy frozen task/config records and
  developer-only checkpoint continuation retaining its session and prior usage.
- **Real pinned Pi 0.99.1 + the built Go CLI** completed two separate single-role
  delegates against deterministic loopback HTTP SSE responses. Both customer
  layouts had a Git repository nested inside an outer project folder. One named
  reusable config was selected through stdin input; no task staging files were
  needed. Each task made exactly one scoped commit and returned its own unreviewed
  candidate receipt. Snapshot queries verified distinct Profile/Context/Session
  identity, reported checks and gaps, and 60 known tokens per fixture. Each made
  four local requests, with no review/polish or retry. Provider request assertions
  rejected any mixed customer Context. Primary checkouts, business files and the
  outer directories stayed unchanged; execution worktrees contained only Git
  metadata and their authorized file. Reports were consumed/cleaned privately,
  and two persistent session directories remained under the disposable data-dir.
- Credentials/auth references, private config fields and raw Context/session
  content were absent from public candidate/snapshot output. Local fixture requests
  are not paid-model or customer acceptance evidence.

The loopback fixture uses a short temporary root for Unix socket path limits and
accepts assistant tool-call messages with null content. Early fixture attempts
failed before corrected test assertions; they ran only in disposable stores and
did not retry or modify production unknown history. No diagnosis of the earlier
native renderer incident is implied by these checks.

## Delivery boundaries

The installed plugin manifest remains `0.4.0-beta.19`; its generator still requires
`--project-id`. The new binary was built only in the task worktree, not installed
or substituted into the running service. A read-only live snapshot still showed
44 Tasks, 75 Runs and four historical unknown Runs. None was relabeled/replayed.

Native Windows amd64 cross-compilation passed; it is not a real Windows device
or Codex host acceptance. No new MCP/native UI selection surface was added.
Discovery and invocation use the existing CLI/skills. Existing native panel
stability evidence remains separate.

Repository integration, push, plugin release, installation cutover and running
service update remain separate actions. A new installed artifact/runtime is
required before customers can use these source changes through the plugin.
Additional SDKs, schedulers, customer process gates and business integrations
are outside this change.

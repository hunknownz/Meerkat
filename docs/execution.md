# Choosing how work is executed

Using Meerkat does not require delegating every edit. The coordinator chooses
the execution path for each bounded task; the user can override that choice.

| Path | Choose it when | Who implements and checks |
| --- | --- | --- |
| Direct | A small edit, diagnosis or integration is clearer to finish in the current chat. | The coordinator implements, reviews the diff and runs relevant checks. |
| Delegate | One isolated coding task should run independently. | Meerkat runs the configured developer; the coordinator reviews the candidate. |
| Workflow | Development needs independent review, bounded fixes and polish. | Meerkat runs the frozen role Profiles, then the coordinator checks the final delivery. |

These are coordination choices, not three executor adapters or a new scheduler
setting. Direct work does not create a Meerkat Run or inherit its process controls;
report it as coordinator work. Delegate uses `run`; Workflow uses `prepare` and
`dispatch`/`execute`. The [delegate](../skills/delegate/SKILL.md) and
[workflow](../skills/workflow/SKILL.md) skills apply after choosing delegation.

## Executor and model selection

For delegated work, each private Profile selects `executor`, `provider`, `model`,
credential reference and limits. Task Profiles select development, independent
review and polish separately. The current implemented executor is Pi. A model
such as DeepSeek or Claude is a model selection inside that executor; it is not
a separate executor. Codex is currently a coordinator, not a Meerkat executor.
No Codex adapter is implied by choosing Direct.

## Reusable execution Profiles

An execution config selects Pi, provider/model, credential environment-variable
reference, command and limits. Omit `projectId` to reuse it for independent
projects. Create one with `configure.mjs --profile-id shared-pi` and the provider,
model and auth-reference flags documented in [install](install.md). Select its
managed name or absolute private path in each task's `profiles.developer`,
`reviewer` and `polisher`; the roles may use different configs. `profile list`
provides read-only discovery of names, models, limits and project bindings in
`<data-dir>/profiles`. It excludes credential references, commands and config
contents, and never chooses a default. Names resolve in the service's data-dir;
externally stored legacy configs still work via an explicit absolute path.
`valid` means structural/private-file validation passed; it does not establish
credential presence, provider availability, credit or paid-model readiness.
`doctor --profile NAME|ABS` checks
the selected file without preparing or executing a task. `run --input FILE
--dry-run` validates the complete contract without recording it or calling a
provider. Neither command silently chooses a fallback model or Profile.

Delegate requires only `profiles.developer`. Older three-role delegate inputs
still freeze the explicitly supplied configs, but execute/reserve only the
developer's Provider. Full workflow preparation requires developer, reviewer
and polisher; its Provider reservations cover all of those roles. Role selection
never changes a frozen task or turns a delegate candidate into reviewed delivery.

Every input still supplies `project.id`, repository, linked worktree, goal,
scope, acceptance, necessary Context and optional `changeId`. Prepare freezes
separate project-owned Profile snapshots even when their private source path
and configuration digest match. Go uses task ownership for project concurrency
and dependencies; session selection is bound to the task, role, worktree,
Profile and contract. Provider settings can be shared, but conversation files
and project Context cannot. Shared configs should contain execution settings;
curate customer requirements and role contracts into the task Context.

Legacy configs with `projectId` keep their exact project restriction and digest.
`configure.mjs --project-id example` keeps its old bound output; combine it with
`--profile-id named-profile` only when an explicit binding is intended. To migrate
for future tasks, create a **new private file** without `projectId`, preserving
the selected provider/model, credential reference, command and limits. Either
use the generator with a new neutral Profile ID or copy the old config privately
and omit the binding after reviewing any customer-specific instruction paths.
Choose the new path only when preparing new tasks. Keep old config files for
existing frozen tasks; changing their file does not adapt their snapshots and
execution will refuse the changed digest. No frozen task, session, historical
summary or unknown Run is rewritten or replayed by this migration.

Using delegate/workflow is an explicit caller choice. The plugin does not add
entrypoints, Profiles, credentials or mandatory Pi execution rules to customer
AGENTS, skills or application configuration. A missing selected file should be
reported as missing execution configuration, not as a need for a customer-specific
executor setup. Runtimes before this change, including beta.19, still require
project-bound configs; the new source and installed runtime are separate states.

## Private materials and candidate boundary

Default to task JSON through `--input -`. The plugin persists its immutable
contract JSON in private SQLite, so a task file in the customer project is
unnecessary. Reviewable input files, if used, belong in private
`<data-dir>/inputs`, not the outer customer folder around a nested repository.
Managed Profiles/provider settings, execution reports, database history, sessions
and control receipts stay under the plugin's data-dir. The executor's standalone
report validator checks private ownership and exclusion from the worktree;
the Go scheduler supplies the stronger default `data-dir/runs/.../report.json`.
The executor check alone does not certify an arbitrary caller-selected folder.

`run` returns the unreviewed local candidate's Task ID/state and SHA. Query
`snapshot` for its frozen baseline/scope, Run summary with SHA/Context binding,
reported checks and known gaps, delivery record and actual/unknown usage. Verify
the Git diff and checks under the customer's own rules before integration or
publication. Execution completion does not imply customer QA, acceptance or
deployment, and a module call does not append customer process gates.

Useful instructions are: “handle this directly”, “delegate implementation and
review it here”, or “use a full workflow with the selected review Profile”.
Project preferences belong in private project guidance; task overrides belong
in the brief and selected Profiles. Meerkat does not add a global requirement
that Pi write the project's code.

## Changing the choice

Choose Profiles before preparing the Task. Once prepared, its scope, Context,
Profiles and allowance remain frozen. Verified checkpoints continue that same
contract. A different executor/model or a coordinator takeover uses a separate
bounded worktree and reviewed transfer; preserve the original Task's history,
partial files and unknown usage. Do not relabel coordinator work as executor
delivery or automatically resend an uncertain request through another model.

The coordinator can inspect an unsuccessful Task, reuse a reviewed patch in a
new worktree and finish it directly when the user's execution preference allows
that. Record who produced the original patch, who completed it, checks and gaps.
Remote integration and deployment keep their existing authorization boundaries.

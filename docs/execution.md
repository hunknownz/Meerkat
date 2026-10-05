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

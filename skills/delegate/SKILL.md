---
name: delegate
description: Delegate one bounded coding run through a configured Meerkat executor, then review the local commit. Use when the coordinator will perform the review; use workflow for an automated multi-role delivery.
---

# Meerkat delegation

The coordinator prepares a clean linked Git worktree on a task branch and a strict task JSON file outside it: goal, explicit paths, acceptance checks, Context and Profiles. See [task input](../workflow/references/task-input.md). The configured executor implements and makes one scoped local commit.

1. Locate the plugin root relative to this skill (`../..`) and run commands as `node <root>/scripts/launch.mjs <command>`. An existing absolute `meerkat` binary (`MEERKAT_BIN`) is an acceptable fallback. If no runtime or profile exists, follow [get started](../get-started/SKILL.md) / [install](../../docs/install.md). Credentials stay in the profile's named environment variable.
2. Run `launch.mjs run --input <task-json> --dry-run`.
3. With the service running, run without --dry-run. This yields the first local candidate only, unreviewed. New tasks default to token monitoring; explicit `maxTokens` retains hard-cap behavior. Respect selected caps and Profile Run/time limits; if stopped, retain changes and inspect the reason before another run.
4. Read the receipt, review the baseline-to-result diff and rerun relevant checks. Report local SHA, checks, gaps and actual usage; missing cost remains unknown.

For an existing paused delegate task, use `launch.mjs run --task <task-id> --resume`.
This verifies its original checkpoint, session, contract and remaining allowance;
it neither prepares another task nor enters automated review. Workflow `execute`
cannot continue delegate tasks. An exhausted allowance needs a separately
[authorized budget decision](../../docs/budget-decisions.md) before continuing.
Unknown or changed evidence stays blocked; never recreate the task to bypass it.

For an authorized graceful pause, use the panel or `control pause` bound to the
exact Run/Session and a stable UUID. Query the same receipt; acknowledgement
alone is not a checkpoint. Bounded follow-ups use the same Session and frozen
scope. See [Run controls](../../docs/run-controls.md).

This path runs only the developer, with explicit continuation when needed and no automated review loop. Push, merge, Issue comments and deployment require corresponding authorization. Worktrees isolate Git state; they are not an execution sandbox. Never print keys or pass them as arguments.

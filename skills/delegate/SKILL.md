---
name: delegate
description: Delegate one bounded coding run through a configured Meerkat executor, then review the local commit. Use when the coordinator will perform the review; use workflow for an automated multi-role delivery.
---

# Meerkat delegation

The coordinator prepares a clean linked Git worktree on a task branch and a strict task JSON file outside it: goal, explicit paths, acceptance checks, Context and Profiles. See [task input](../workflow/references/task-input.md). The configured executor implements and makes one scoped local commit.

1. Locate the plugin root relative to this skill (`../..`) and run commands as `node <root>/scripts/launch.mjs <command>`. An existing absolute `meerkat` binary (`MEERKAT_BIN`) is an acceptable fallback. If no runtime or profile exists, follow [get started](../get-started/SKILL.md) / [install](../../docs/install.md). Credentials stay in the profile's named environment variable.
2. Run `launch.mjs run --input <task-json> --dry-run`.
3. With the service running, run without --dry-run. This yields the first local candidate only, unreviewed. Respect the declared budget; if stopped, retain changes and inspect the reason before another run.
4. Read the receipt, review the baseline-to-result diff and rerun relevant checks. Report local SHA, checks, gaps and actual usage; missing cost remains unknown.

This path performs one run without an automated review loop. Push, merge, Issue comments and deployment require corresponding authorization. Worktrees isolate Git state; they are not an execution sandbox. Never print keys or pass them as arguments.

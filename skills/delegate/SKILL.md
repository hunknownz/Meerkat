---
name: delegate
description: Delegate one bounded coding run through a configured Meerkat executor, then review the local commit. Use when the coordinator will perform the review; use workflow for an automated multi-role delivery.
---

# Meerkat delegation

The coordinator prepares a clean linked Git worktree on a task branch and a brief outside it: goal, explicit paths, acceptance checks and relevant decisions. The configured executor implements and makes one scoped local commit. Pi is currently supported.

1. Locate the plugin's bin/meerkat (or build cmd/meerkat using the repository README). Inspect the private profile; credentials stay in its named environment variable.
2. Run `meerkat run --config <private-profile> --worktree <linked-worktree> --task <brief> --dry-run`.
3. Run without --dry-run. Respect the declared budget; if stopped, retain changes and inspect the reason before another run.
4. Read the receipt, review the baseline-to-result diff and rerun relevant checks. Report local SHA, checks, gaps and actual usage; missing cost remains unknown.

This path performs one run without an automated review loop. Push, merge, Issue comments and deployment require corresponding authorization. Worktrees isolate Git state; they are not an execution sandbox. Never print keys or pass them as arguments.

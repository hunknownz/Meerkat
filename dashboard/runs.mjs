// Read-only, best-effort view of private Pi run summaries under each task's
// declared worktree (<worktree>/.pi-developer/runs/<timestamp>.json).
// Node 22 built-ins only. Never writes, never executes a path entered in the
// UI, and never reads credential values.
//
// Attribution: a summary belongs to a task only when its recorded taskId is an
// exact string match for that task's id. Summaries are deduplicated by the
// actual summary file path, so a run appears at most once even when several
// tasks point at the same (reused) worktree. Tasks without a worktreePath
// contribute no rows, and summaries recorded without a taskId (recorded before
// --task-id existed) are not attributed to any task.
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

const MAX_PER_TASK = 5;
const MAX_TOTAL = 50;

function normalize(raw) {
  const safe = (v) => (typeof v === 'string' ? v : null);
  return {
    projectId: safe(raw.projectId),
    model: safe(raw.model),
    outcome: safe(raw.outcome),
    reason: safe(raw.reason),
    startedAt: safe(raw.startedAt),
    endedAt: safe(raw.endedAt),
    elapsedSeconds: typeof raw.elapsedSeconds === 'number' ? raw.elapsedSeconds : null,
    tokens: raw.tokens && typeof raw.tokens === 'object' ? raw.tokens : null,
    estimatedCostUsd: typeof raw.estimatedCostUsd === 'number' ? raw.estimatedCostUsd : null,
    branch: safe(raw.branch),
    changedPaths: Array.isArray(raw.changedPaths) ? raw.changedPaths.filter((p) => typeof p === 'string') : [],
  };
}

/** Collects normalized run summaries, newest first. Never throws. */
export function collectRuns(tasks) {
  const byFile = new Map(); // actual summary file path -> owning-task row (dedup)
  for (const task of Array.isArray(tasks) ? tasks : []) {
    if (!task || typeof task.worktreePath !== 'string' || !task.worktreePath) continue;
    const dir = join(task.worktreePath, '.pi-developer', 'runs');
    let names;
    try {
      names = readdirSync(dir);
    } catch {
      continue; // no worktree or no runs yet
    }
    for (const name of names) {
      if (!name.endsWith('.json')) continue;
      const file = join(dir, name);
      if (byFile.has(file)) continue; // a run appears once, keyed by summary file path
      let raw;
      let mtimeMs;
      try {
        const stat = statSync(file);
        raw = JSON.parse(readFileSync(file, 'utf8'));
        mtimeMs = stat.mtimeMs;
      } catch {
        continue; // skip unreadable or corrupt summaries
      }
      if (raw.taskId !== task.id) continue; // exact taskId match only
      byFile.set(file, {
        taskId: task.id,
        taskTitle: typeof task.title === 'string' ? task.title : null,
        workspaceId: task.workspaceId ?? null,
        file: name,
        mtimeMs,
        summary: normalize(raw),
      });
    }
  }

  // Bound newest MAX_PER_TASK rows per owning task, then MAX_TOTAL overall.
  const perTask = new Map();
  for (const row of byFile.values()) {
    const list = perTask.get(row.taskId) || [];
    list.push(row);
    perTask.set(row.taskId, list);
  }
  const rows = [];
  for (const list of perTask.values()) {
    list.sort((a, b) => b.mtimeMs - a.mtimeMs);
    rows.push(...list.slice(0, MAX_PER_TASK));
  }
  rows.sort((a, b) => b.mtimeMs - a.mtimeMs);
  return rows.slice(0, MAX_TOTAL).map(({ mtimeMs, ...rest }) => rest);
}
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { collectRuns } from '../dashboard/runs.mjs';

const A = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee';
const B = '22222222-2222-4222-8222-222222222222';
const OTHER = '33333333-3333-4333-8333-333333333333';

test('collectRuns attributes summaries by exact taskId in a shared worktree', () => {
  const worktree = mkdtempSync(join(tmpdir(), 'pi-runs-test-'));
  try {
    const dir = join(worktree, '.pi-developer', 'runs');
    mkdirSync(dir, { recursive: true });
    const write = (name, body) => writeFileSync(join(dir, name), JSON.stringify(body));
    write('a.json', { taskId: A, outcome: 'success', model: 'p/m' });
    write('b.json', { taskId: B, outcome: 'failure', model: 'p/m' });
    write('legacy.json', { outcome: 'success', model: 'p/m' }); // no taskId
    write('wrong.json', { taskId: OTHER, outcome: 'success', model: 'p/m' });
    write('upper.json', { taskId: A.toUpperCase(), outcome: 'success' }); // not an exact match

    const tasks = [
      { id: A, title: 'Task A', workspaceId: 'ws', worktreePath: worktree },
      { id: B, title: 'Task B', workspaceId: 'ws', worktreePath: worktree },
    ];
    const rows = collectRuns(tasks);

    const pairs = rows.map((r) => `${r.taskId}:${r.file}`).sort();
    assert.deepEqual(pairs, [`${A}:a.json`, `${B}:b.json`].sort());
    assert.equal(new Set(rows.map((r) => r.file)).size, rows.length, 'no duplicate summaries');
    const a = rows.find((r) => r.taskId === A);
    assert.equal(a.taskTitle, 'Task A');
    assert.equal(a.summary.outcome, 'success');
    assert.equal(rows.find((r) => r.taskId === B).summary.outcome, 'failure');
    assert.ok(!rows.some((r) => r.file === 'legacy.json' || r.file === 'wrong.json' || r.file === 'upper.json'));

    // Listing the same task twice must not duplicate its run.
    const dup = collectRuns([tasks[0], tasks[0]]);
    assert.deepEqual(dup.map((r) => r.file), ['a.json']);
  } finally {
    rmSync(worktree, { recursive: true, force: true });
  }
});

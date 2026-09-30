import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = fileURLToPath(new URL('.', import.meta.url));
const runner = join(here, '../scripts/run.mjs');
const fakePi = join(here, 'fixtures/fake-pi.mjs');
const g = (cwd, ...a) => execFileSync('git', ['-c', 'user.name=t', '-c', 'user.email=t@t', ...a], { cwd, encoding: 'utf8' }).trim();

function setup(limits = {}) {
  const root = mkdtempSync(join(tmpdir(), 'pidev-'));
  const main = join(root, 'main');
  const wt = join(root, 'wt');
  execFileSync('git', ['init', '-q', '-b', 'main', main]);
  writeFileSync(join(main, 'AGENTS.md'), 'Be careful.\n');
  g(main, 'add', 'AGENTS.md'); g(main, 'commit', '-qm', 'init');
  g(main, 'worktree', 'add', '-q', '-b', 'task/demo', wt);
  const config = join(root, 'project.json');
  writeFileSync(config, JSON.stringify({
    projectId: 'demo', provider: 'fake', model: 'm1', authEnv: 'FAKE_PI_KEY',
    instructions: ['AGENTS.md'], limits: { maxWallSeconds: 20, maxTokens: 100000, ...limits },
    piCommand: [process.execPath, fakePi],
  }));
  const task = join(root, 'task.md');
  writeFileSync(task, 'Add feature.txt\n');
  return { root, main, wt, config, task };
}

function run(ctx, { scenario = 'ok', key = 'secret-value-123', extra = [] } = {}) {
  const env = { ...process.env, FAKE_PI_SCENARIO: scenario };
  delete env.FAKE_PI_KEY;
  if (key) env.FAKE_PI_KEY = key;
  const r = spawnSync(process.execPath, [runner, '--config', ctx.config, '--worktree', ctx.wt, '--task', ctx.task, ...extra], { env, encoding: 'utf8' });
  assert.ok(!(r.stdout + r.stderr).includes('secret-value-123'), 'key must never be printed');
  return r;
}

const summary = (wt) => {
  const dir = join(wt, '.pi-developer/runs');
  return JSON.parse(readFileSync(join(dir, readdirSync(dir)[0]), 'utf8'));
};

test('dry-run needs no key and makes no changes', () => {
  const ctx = setup();
  const r = run(ctx, { key: null, extra: ['--dry-run'] });
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /dry run/);
  assert.match(r.stdout, /fake\/m1/);
  assert.equal(existsSync(join(ctx.wt, '.pi-developer')), false);
});

test('preflight refuses primary worktree and dirty tree', () => {
  const ctx = setup();
  assert.equal(spawnSync(process.execPath, [runner, '--config', ctx.config, '--worktree', ctx.main, '--task', ctx.task, '--dry-run']).status, 2);
  writeFileSync(join(ctx.wt, 'stray.txt'), 'x');
  const r = run(ctx, { extra: ['--dry-run'] });
  assert.equal(r.status, 2);
  assert.match(r.stderr, /not clean/);
});

test('success aggregates assistant tokens only and writes ignored summary', () => {
  const ctx = setup();
  const base = g(ctx.wt, 'rev-parse', 'HEAD');
  const r = run(ctx);
  assert.equal(r.status, 0, r.stdout + r.stderr);
  const s = summary(ctx.wt);
  assert.deepEqual(s.tokens, { input: 300, output: 30, cacheRead: 50, cacheWrite: 5, total: 385, assistantMessages: 2 });
  assert.equal(s.estimatedCostUsd, 0.003);
  assert.equal(s.outcome, 'success');
  assert.equal(s.baselineSha, base);
  assert.notEqual(s.resultSha, base);
  assert.deepEqual(s.changedPaths, ['feature.txt']);
  assert.equal(g(ctx.wt, 'status', '--porcelain'), '');
  const runs = join(ctx.wt, '.pi-developer/runs');
  assert.equal(statSync(join(ctx.wt, '.pi-developer')).mode & 0o777, 0o700);
  assert.equal(statSync(runs).mode & 0o777, 0o700);
  assert.equal(statSync(join(runs, readdirSync(runs)[0])).mode & 0o777, 0o600);
  assert.ok(!readFileSync(join(ctx.wt, '.pi-developer/runs', readdirSync(join(ctx.wt, '.pi-developer/runs'))[0]), 'utf8').includes('Be careful'));
});

test('dirty result fails and preserves changes', () => {
  const ctx = setup();
  const r = run(ctx, { scenario: 'dirty' });
  assert.equal(r.status, 1);
  const s = summary(ctx.wt);
  assert.equal(s.outcome, 'failed');
  assert.equal(s.reason, 'no new commit on task branch');
  assert.equal(s.clean, false);
  assert.ok(existsSync(join(ctx.wt, 'feature.txt')));
});

test('non-zero exit fails', () => {
  const ctx = setup();
  const r = run(ctx, { scenario: 'fail' });
  assert.equal(r.status, 1);
  assert.equal(summary(ctx.wt).reason, 'pi exited 1');
});

test('token limit stops a running Pi', () => {
  const ctx = setup({ maxTokens: 1000 });
  const r = run(ctx, { scenario: 'overtokens' });
  assert.equal(r.status, 1);
  const s = summary(ctx.wt);
  assert.equal(s.outcome, 'stopped');
  assert.equal(s.reason, 'token limit exceeded');
  assert.equal(s.tokens.total, 165);
});

test('missing key fails before calling Pi', () => {
  const ctx = setup();
  const r = run(ctx, { key: null });
  assert.equal(r.status, 2);
  assert.match(r.stderr, /missing env var FAKE_PI_KEY/);
});

test('--task-id is recorded in the summary and shown in the dry-run plan', () => {
  const ctx = setup();
  const id = '123e4567-e89b-42d3-a456-426614174000';
  const r = run(ctx, { extra: ['--task-id', id] });
  assert.equal(r.status, 0, r.stdout + r.stderr);
  assert.equal(summary(ctx.wt).taskId, id);

  const dry = run(ctx, { key: null, extra: ['--task-id', id, '--dry-run'] });
  assert.equal(dry.status, 0, dry.stderr);
  assert.ok(dry.stdout.includes(id), dry.stdout);
  assert.match(dry.stdout, /taskId:    /);
});

test('without --task-id the summary carries no taskId key', () => {
  const ctx = setup();
  const r = run(ctx);
  assert.equal(r.status, 0, r.stdout + r.stderr);
  assert.equal('taskId' in summary(ctx.wt), false);
});

test('--task-id must be a valid UUID or preflight rejects the run', () => {
  const ctx = setup();
  for (const bad of ['', 'not-a-uuid', '123', '123e4567-e89b-42d3-a456-42661417400Z', '123e4567e89b42d3a456426614174000']) {
    const r = run(ctx, { key: null, extra: ['--task-id', bad, '--dry-run'] });
    assert.equal(r.status, 2, `task-id=${bad}: ${r.stderr}`);
    assert.match(r.stderr, /invalid --task-id/);
  }
  assert.equal(existsSync(join(ctx.wt, '.pi-developer')), false);
});

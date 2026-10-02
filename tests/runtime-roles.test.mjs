import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { executeRun, loadConfig, UsageTracker } from '../scripts/run.mjs';
import { listActive, writeActive } from '../dashboard/active.mjs';

const here = fileURLToPath(new URL('.', import.meta.url));
const fakePi = join(here, 'fixtures/fake-pi.mjs');
const g = (cwd, ...a) => execFileSync('git', ['-c', 'user.name=t', '-c', 'user.email=t@t', ...a], { cwd, encoding: 'utf8' }).trim();
const CTX = 'sha256:abc123';

function setup(limits = {}) {
  const root = mkdtempSync(join(tmpdir(), 'pirole-'));
  const main = join(root, 'main');
  const wt = join(root, 'wt');
  const priv = join(root, 'private');
  mkdirSync(priv, { mode: 0o700 });
  execFileSync('git', ['init', '-q', '-b', 'main', main]);
  writeFileSync(join(main, 'AGENTS.md'), 'Be careful.\n');
  g(main, 'add', 'AGENTS.md'); g(main, 'commit', '-qm', 'init');
  g(main, 'worktree', 'add', '-q', '-b', 'task/demo', wt);
  const configPath = join(root, 'project.json');
  writeFileSync(configPath, JSON.stringify({
    projectId: 'demo', provider: 'fake', model: 'm1', authEnv: 'FAKE_PI_KEY',
    instructions: ['AGENTS.md'], limits: { maxWallSeconds: 20, maxTokens: 100000, ...limits },
    piCommand: [process.execPath, fakePi],
  }));
  const task = join(root, 'task.md');
  writeFileSync(task, 'Do the thing\n');
  return { root, main, wt, priv, config: loadConfig(configPath), task, data: join(root, 'data'), base: g(wt, 'rev-parse', 'HEAD') };
}

function exec(ctx, { scenario, report = 'none', usage = 'ok', role = 'developer', reportFile = join(ctx.priv, 'report.json'), extraEnv = {}, ...rest } = {}) {
  const env = { ...process.env, FAKE_PI_KEY: 'secret-value-123', FAKE_PI_SCENARIO: scenario, FAKE_PI_REPORT: report, FAKE_PI_USAGE: usage, ...extraEnv };
  return executeRun({ config: ctx.config, worktree: ctx.wt, taskPath: ctx.task, role, reportFile, contextDigest: CTX, dataDir: ctx.data, env, ...rest });
}

const noSecrets = (s) => {
  const txt = JSON.stringify(s);
  for (const bad of ['secret-value-123', 'sk-secret-provider', 'SECRET-ARG', '/etc/secret', 'HTTP 401']) assert.ok(!txt.includes(bad), `leaked ${bad}`);
};

test('reviewer pass: no commit, read-only tools, verdict recorded', async () => {
  const ctx = setup();
  const argsFile = join(ctx.root, 'args.json');
  const events = []; const usages = []; const starts = [];
  const s = await exec(ctx, {
    scenario: 'review', report: 'pass', role: 'reviewer', candidateSha: ctx.base, runId: 'run-1', agentId: 'agent-1',
    extraEnv: { FAKE_PI_ARGS_FILE: argsFile },
    onEvent: (e) => events.push(e), onUsage: (u) => usages.push(u), onStart: (x) => starts.push(x),
  });
  assert.equal(s.outcome, 'success', s.reason);
  assert.equal(s.role, 'reviewer'); assert.equal(s.verdict, 'pass');
  assert.equal(s.runId, 'run-1'); assert.equal(s.agentId, 'agent-1');
  assert.equal(s.candidateSha, ctx.base); assert.equal(s.contextDigest, CTX);
  assert.equal(s.resultSha, ctx.base); assert.equal(s.committed, false); assert.equal(s.clean, true);
  assert.equal(s.report.verdict, 'pass');
  assert.equal(s.usageCompleteness, 'complete');
  assert.deepEqual(s.tokens, { input: 100, output: 10, cacheRead: 0, cacheWrite: 0, total: 110, assistantMessages: 1 });
  const args = JSON.parse(readFileSync(argsFile, 'utf8'));
  assert.equal(args[args.indexOf('--tools') + 1], 'read,bash');
  assert.match(args.at(-1), /You are the reviewer/);
  assert.equal(starts.length, 1); assert.ok(Number.isInteger(starts[0].pid));
  assert.ok(events.some((e) => e.type === 'tool' && e.summary === 'read'));
  for (const e of events) assert.deepEqual(Object.keys(e).sort(), ['observedAt', 'summary', 'type']);
  assert.ok(usages.length >= 1 && usages.at(-1).usageCompleteness === 'complete');
  noSecrets([s, events, usages]);
  const sf = JSON.parse(readFileSync(s.summaryFile, 'utf8'));
  assert.equal(sf.verdict, 'pass');
});

test('reviewer changes_requested succeeds without commit', async () => {
  const ctx = setup();
  const s = await exec(ctx, { scenario: 'review', report: 'changes', role: 'reviewer' });
  assert.equal(s.outcome, 'success', s.reason);
  assert.equal(s.verdict, 'changes_requested');
  assert.equal(s.report.findings[0].id, 'F1');
  assert.equal(s.committed, false);
});

test('reviewer mutation is rejected', async () => {
  const ctx = setup();
  const s = await exec(ctx, { scenario: 'review-mutate', report: 'pass', role: 'reviewer' });
  assert.equal(s.outcome, 'failed');
  assert.equal(s.errorCategory, 'reviewer_mutation');
  assert.ok(s.changedPaths.includes('evil.txt'));
});

test('polisher no_change succeeds; polisher changed commits', async () => {
  const a = setup();
  const s1 = await exec(a, { scenario: 'polish-nochange', report: 'no_change', role: 'polisher' });
  assert.equal(s1.outcome, 'success', s1.reason);
  assert.equal(s1.report.decision, 'no_change'); assert.equal(s1.resultSha, a.base);
  const b = setup();
  const s2 = await exec(b, { scenario: 'polish-changed', report: 'changed', role: 'polisher' });
  assert.equal(s2.outcome, 'success', s2.reason);
  assert.equal(s2.committed, true); assert.deepEqual(s2.changedPaths, ['feature.txt']);
  const c = setup();
  const s3 = await exec(c, { scenario: 'polish-changed', report: 'no_change', role: 'polisher' });
  assert.equal(s3.errorCategory, 'decision_mismatch');
});

test('developer with report succeeds; developer no_change fails', async () => {
  const a = setup();
  const s = await exec(a, { scenario: 'dev-report', report: 'changed' });
  assert.equal(s.outcome, 'success', s.reason);
  const b = setup();
  const s2 = await exec(b, { scenario: 'review', report: 'no_change' });
  assert.equal(s2.errorCategory, 'no_commit');
});

test('missing, stale, wrong-schema and symlinked reports fail', async () => {
  for (const [report, cat] of [['none', 'report_missing'], ['stale', 'report_stale'], ['invalid', 'report_invalid'], ['symlink', 'report_invalid']]) {
    const ctx = setup();
    const s = await exec(ctx, { scenario: 'review', report, role: 'reviewer' });
    assert.equal(s.outcome, 'failed', report);
    assert.equal(s.errorCategory, cat, report);
    assert.equal(s.report, null);
  }
  const ctx = setup();
  const s = await exec(ctx, { scenario: 'review', report: 'pass', role: 'reviewer', extraEnv: { FAKE_PI_CTX: 'sha256:other' } });
  assert.equal(s.errorCategory, 'report_stale');
});

test('report path must be new, outside worktree; role reports required', async () => {
  const ctx = setup();
  writeFileSync(join(ctx.priv, 'old.json'), '{}');
  await assert.rejects(exec(ctx, { scenario: 'review', role: 'reviewer', reportFile: join(ctx.priv, 'old.json') }), /already exists/);
  await assert.rejects(exec(ctx, { scenario: 'review', role: 'reviewer', reportFile: join(ctx.wt, 'r.json') }), /outside the worktree/);
  await assert.rejects(exec(ctx, { scenario: 'review', role: 'reviewer', reportFile: 'rel.json' }), /absolute/);
  await assert.rejects(exec(ctx, { scenario: 'review', role: 'reviewer', reportFile: null }), /requires a report file/);
  await assert.rejects(exec(ctx, { scenario: 'review', role: 'reviewer', candidateSha: '1'.repeat(40) }), /candidate sha/);
  await assert.rejects(exec(ctx, { scenario: 'review', role: 'boss' }), /invalid role/);
});

test('missing and invalid usage is partial, never coerced to zero; cost unknown', async () => {
  const a = setup();
  const s = await exec(a, { scenario: 'review', report: 'pass', role: 'reviewer', usage: 'missing' });
  assert.equal(s.outcome, 'success', s.reason);
  assert.equal(s.usageCompleteness, 'partial');
  assert.deepEqual(s.tokens, { input: 100, output: null, cacheRead: null, cacheWrite: null, total: 100, assistantMessages: 1 });
  assert.equal(s.estimatedCostUsd, null);
  const b = setup();
  const s2 = await exec(b, { scenario: 'review', report: 'pass', role: 'reviewer', usage: 'none' });
  assert.equal(s2.usageCompleteness, 'unknown');
  assert.equal(s2.tokens.total, null); assert.equal(s2.tokens.input, null);
  assert.equal(s2.estimatedCostUsd, null);
});

test('provider error yields sanitized category', async () => {
  const ctx = setup();
  const s = await exec(ctx, { scenario: 'review', report: 'pass', role: 'reviewer', usage: 'error' });
  assert.equal(s.outcome, 'failed');
  assert.equal(s.errorCategory, 'provider_error');
  assert.equal(s.usageCompleteness, 'partial');
  assert.equal(s.estimatedCostUsd, null);
  noSecrets(s);
  noSecrets(readFileSync(s.summaryFile, 'utf8'));
});

const alive = (pid) => { try { process.kill(pid, 0); return true; } catch { return false; } };

test('abort stops the owned Pi child and its group, then finalizes stopped', async () => {
  const ctx = setup();
  const pidFile = join(ctx.root, 'pids.json');
  const ac = new AbortController();
  const events = [];
  const p = exec(ctx, {
    scenario: 'hang', role: 'developer', reportFile: null, signal: ac.signal, runId: 'run-abort',
    extraEnv: { FAKE_PI_PID_FILE: pidFile }, onEvent: (e) => events.push(e),
    onStart: () => { throw new Error('callback errors must not leak'); },
  });
  for (let i = 0; i < 200 && !existsSync(pidFile); i++) await new Promise((r) => setTimeout(r, 25));
  const pids = JSON.parse(readFileSync(pidFile, 'utf8'));
  for (let i = 0; i < 100; i++) { if ((await listActive(ctx.data)).length) break; await new Promise((r) => setTimeout(r, 25)); }
  const active = await listActive(ctx.data);
  assert.equal(active.length, 1);
  assert.equal(active[0].role, 'developer'); assert.equal(active[0].runId, 'run-abort');
  ac.abort();
  const s = await p;
  assert.equal(s.outcome, 'stopped'); assert.equal(s.errorCategory, 'aborted');
  await new Promise((r) => setTimeout(r, 100));
  assert.equal(alive(pids.pid), false, 'pi child must be stopped');
  assert.equal(alive(pids.grandchild), false, 'no orphan in own process group');
  assert.deepEqual(await listActive(ctx.data), []);
  assert.ok(events.some((e) => e.type === 'tool' && e.summary === 'bash'));
  noSecrets([s, events]);
});

test('UsageTracker excludes user messages and invalid counters', () => {
  const t = new UsageTracker();
  t.line(JSON.stringify({ type: 'message_end', message: { role: 'user', usage: { input: 5, output: 5, cacheRead: 0, cacheWrite: 0 } } }));
  assert.equal(t.completeness(), 'unknown');
  t.line(JSON.stringify({ type: 'message_update', message: { role: 'user', usage: { input: 99 } } }));
  assert.equal(t.inFlight, 0);
  t.line(JSON.stringify({ type: 'message_end', message: { role: 'assistant', usage: { input: -1, output: 3, cacheRead: Infinity, cacheWrite: '2', cost: { total: 0 } } } }));
  assert.deepEqual(t.tokens, { input: null, output: 3, cacheRead: null, cacheWrite: null });
  assert.equal(t.completeness(), 'partial');
  assert.equal(t.estimatedCost(), null);
  t.line('x'.repeat(10));
  assert.equal(t.badLines, 1);
});

test('active records: optional metadata listed, old records still valid', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'piact-'));
  const now = new Date().toISOString();
  const base = { task: 't', model: 'm', projectId: 'p', worktree: '/w', pid: process.pid, startedAt: now, updatedAt: now };
  await writeActive({ ...base, id: '123e4567-e89b-42d3-a456-426614174000' }, dir);
  await writeActive({ ...base, id: '123e4567-e89b-42d3-a456-426614174001', role: 'reviewer', runId: 'r1', agentId: 'a1', taskId: '123e4567-e89b-42d3-a456-426614174999' }, dir);
  await writeActive({ ...base, id: '123e4567-e89b-42d3-a456-426614174002', role: 'hacker', runId: 'bad id!' }, dir);
  const list = await listActive(dir);
  const byId = Object.fromEntries(list.map((r) => [r.id.slice(-1), r]));
  assert.deepEqual(Object.keys(byId['0']).sort(), ['id', 'model', 'projectId', 'startedAt', 'task', 'updatedAt', 'worktree']);
  assert.equal(byId['1'].role, 'reviewer'); assert.equal(byId['1'].agentId, 'a1'); assert.equal(byId['1'].runId, 'r1');
  assert.equal('role' in byId['2'], false); assert.equal('runId' in byId['2'], false);
  assert.equal(readdirSync(join(dir, 'active')).length, 3);
});

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { aggregateUsage, executeTasks, prepareTask, readWorkflow, requestStop, updateSettings, WorkflowInputError } from '../workflow/core.mjs';
import { WorkflowStore } from '../workflow/store.mjs';

const here = fileURLToPath(new URL('.', import.meta.url));
const fakePi = join(here, 'fixtures/fake-pi.mjs');
const flowCli = join(here, '../scripts/flow.mjs');
const g = (cwd, ...a) => execFileSync('git', ['-c', 'user.name=t', '-c', 'user.email=t@t', ...a], { cwd, encoding: 'utf8' }).trim();
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const DEAD_PID = (() => { for (let p = 4_000_000; p > 90_000; p -= 7919) { try { process.kill(p, 0); } catch (e) { if (e.code === 'ESRCH') return p; } } return 99_999_999; })();
const ROLES = ['developer', 'reviewer', 'polisher'];

function setup({ worktrees = ['wt'], limits = { maxWallSeconds: 60, maxTokens: 1000 } } = {}) {
  const root = mkdtempSync(join(tmpdir(), 'mk-exec-'));
  const main = join(root, 'main');
  execFileSync('git', ['init', '-q', '-b', 'main', main]);
  writeFileSync(join(main, 'AGENTS.md'), 'Be careful.\n');
  mkdirSync(join(main, 'src'));
  writeFileSync(join(main, 'src/a.js'), 'a\n');
  writeFileSync(join(main, 'src/b.js'), 'b\n');
  g(main, 'add', '.'); g(main, 'commit', '-qm', 'init');
  const wts = {};
  for (const n of worktrees) { wts[n] = join(root, n); g(main, 'worktree', 'add', '-q', '-b', `task/${n}`, wts[n]); }
  const cfgDir = join(root, 'cfg');
  mkdirSync(cfgDir, { mode: 0o700 });
  const writeProfile = (name, role, extra = {}) => {
    const p = join(cfgDir, `${name}.json`);
    writeFileSync(p, JSON.stringify({
      projectId: 'demo', provider: 'fake', model: `m-${role}`, authEnv: 'FAKE_PI_KEY', instructions: ['AGENTS.md'],
      limits, piCommand: [process.execPath, fakePi], ...extra,
    }));
    return p;
  };
  const profiles = Object.fromEntries(ROLES.map((r) => [r, writeProfile(r, r)]));
  const data = join(root, 'data');
  const prepare = (over = {}) => prepareTask(data, {
    project: { id: 'demo', name: 'Demo' }, repository: main, worktree: wts[worktrees[0]], title: 'Change a', goal: 'Update src/a.js',
    scope: ['src/a.js'], acceptance: ['a updated'], context: { version: 1, text: 'Shared frozen context v1', sources: [] }, profiles, ...over,
  });
  return { root, main, wts, wt: wts[worktrees[0]], data, profiles, prepare, writeProfile, cfgDir };
}

function editState(data, fn) {
  const ws = new WorkflowStore(data);
  const lease = ws.acquireController();
  try { const s = ws.read(); fn(s); ws.write(s); } finally { ws.releaseController(lease); }
}

const OK_USAGE = () => ({ tokens: { input: 100, output: 10, cacheRead: 5, cacheWrite: 1, total: 116, assistantMessages: 1 }, usageCompleteness: 'complete', estimatedCostUsd: 0.001 });
const UNKNOWN_USAGE = () => ({ tokens: { input: null, output: null, cacheRead: null, cacheWrite: null, total: null, assistantMessages: 0 }, usageCompleteness: 'unknown', estimatedCostUsd: null });

/**
 * Deterministic stand-in for runner.executeRun. Does real Git work in the worktree; behave(role, call, args)
 * may override: { commit, file, dirty, report:'ok'|'none'|'stale'|'wrongctx', verdict, usage, hold, outcome, errorCategory }.
 */
function fakeRunner(behave = () => ({}), log = []) {
  const stats = { active: 0, maxActive: 0, byWorktree: new Map(), overlapSameWorktree: false };
  const fn = async (args) => {
    const { role, worktree, candidateSha, contextDigest, runId, signal, config } = args;
    const call = log.length;
    log.push({ role, runId, agentId: args.agentId, taskId: args.taskId, worktree, candidateSha, limits: config.limits, model: config.model, brief: readFileSync(args.taskPath, 'utf8'), reportFile: args.reportFile });
    assert.equal(g(worktree, 'rev-parse', 'HEAD'), candidateSha, 'runner starts on exact candidate');
    const b = { commit: role === 'developer', verdict: 'pass', report: 'ok', usage: OK_USAGE(), file: 'src/a.js', ...(behave(role, call, args) ?? {}) };
    stats.active += 1; stats.maxActive = Math.max(stats.maxActive, stats.active);
    const wtCount = (stats.byWorktree.get(worktree) ?? 0) + 1;
    stats.byWorktree.set(worktree, wtCount);
    if (wtCount > 1) stats.overlapSameWorktree = true;
    try {
      args.onStart({ pid: DEAD_PID, startedAt: new Date().toISOString() });
      args.onEvent({ type: 'tool', summary: 'read', observedAt: new Date().toISOString(), raw: 'SECRET-ARG' });
      await sleep(20);
      if (b.hold) await Promise.race([b.hold, new Promise((r) => signal.addEventListener('abort', r, { once: true }))]);
      if (b.usage) args.onUsage(b.usage);
      const base = { runId, role, contextDigest, candidateSha, report: null, ...(b.usage ?? UNKNOWN_USAGE()) };
      if (signal.aborted) return { ...base, resultSha: g(worktree, 'rev-parse', 'HEAD'), outcome: 'stopped', errorCategory: 'aborted', reason: 'aborted' };
      if (b.outcome) return { ...base, resultSha: g(worktree, 'rev-parse', 'HEAD'), outcome: b.outcome, errorCategory: b.errorCategory, reason: b.errorCategory };
      if (b.commit) {
        writeFileSync(join(worktree, b.file), `${role} ${call} ${randomUUID()}\n`);
        g(worktree, 'add', b.file); g(worktree, 'commit', '-qm', `${role} ${call}`);
      }
      if (b.dirty) writeFileSync(join(worktree, b.dirty), 'dirty\n');
      const head = g(worktree, 'rev-parse', 'HEAD');
      let report = null;
      if (b.report !== 'none') {
        report = {
          candidateSha: b.report === 'stale' ? '0'.repeat(40) : head,
          contextDigest: b.report === 'wrongctx' ? `sha256:${'1'.repeat(64)}` : contextDigest,
          summary: 'done', checks: [{ command: 'node --test', result: 'pass' }], knownGaps: [],
          ...(role === 'reviewer'
            ? { verdict: b.verdict, findings: b.verdict === 'changes_requested' ? [{ id: 'F1', summary: 'tighten a.js', path: 'src/a.js', line: 1 }] : [] }
            : { decision: head === candidateSha ? 'no_change' : 'changed' }),
        };
      }
      return { ...base, resultSha: head, report, outcome: 'success', errorCategory: null, reason: null };
    } finally {
      stats.active -= 1; stats.byWorktree.set(worktree, stats.byWorktree.get(worktree) - 1);
    }
  };
  return { fn, log, stats };
}

const noSecrets = (obj) => {
  const s = JSON.stringify(obj);
  for (const bad of ['secret-value-123', 'SECRET-ARG', '/etc/secret', 'FAKE_PI_KEY', 'authEnv', 'piCommand', 'configFile', 'instructions', '/cfg/']) {
    assert.ok(!s.includes(bad), `leaked ${bad}`);
  }
};
const runsOf = (snap, taskId) => snap.runs.filter((r) => r.taskId === taskId);

test('full path: developer -> changes_requested -> fix -> pass -> polish (changed) -> recheck on new SHA -> delivered', async () => {
  const ctx = setup();
  const task = await ctx.prepare();
  const reviews = ['changes_requested', 'pass', 'pass'];
  let r = 0;
  const fr = fakeRunner((role) => (role === 'reviewer' ? { verdict: reviews[r++] } : role === 'polisher' ? { commit: true } : {}));
  const summaries = [];
  const res = await executeTasks({ dataDir: ctx.data, taskIds: [task.id], executeRunImpl: fr.fn, onSummary: (s) => summaries.push(s) });
  assert.equal(res.fatal, null);
  assert.equal(res.tasks[0].state, 'delivered', JSON.stringify(res));
  assert.deepEqual(fr.log.map((l) => l.role), ['developer', 'reviewer', 'developer', 'reviewer', 'polisher', 'reviewer']);
  assert.match(fr.log[2].brief, /F1: tighten a\.js/, 'fix brief carries findings');
  assert.match(fr.log[0].brief, /Shared frozen context v1/);
  assert.doesNotMatch(fr.log[0].brief, /F1/);
  for (const l of fr.log) assert.equal(l.agentId, 'Pi-01');
  for (const l of fr.log) assert.ok(!l.reportFile.startsWith(ctx.wt), 'report outside worktree');
  const snap = await readWorkflow(ctx.data);
  const t = snap.tasks[0];
  const head = g(ctx.wt, 'rev-parse', 'HEAD');
  assert.equal(t.candidateSha, head);
  const ds = snap.deliveries.filter((d) => d.taskId === task.id);
  assert.deepEqual(ds.map((d) => d.state), ['first', 'final_candidate', 'delivered']);
  assert.notEqual(ds[1].candidateSha, ds[2].candidateSha, 'polish produced a new final SHA');
  assert.equal(ds[2].candidateSha, head);
  assert.equal(fr.log[5].candidateSha, head, 'recheck reviewed exact new SHA');
  assert.ok(ds[2].checks.some((c) => c.name === 'context_digest_matches' && c.status === 'pass'));
  assert.ok(ds[2].checks.filter((c) => c.name === 'reported').every((c) => c.status === 'reported'));
  assert.equal(snap.reviews.length, 3);
  assert.equal(snap.reviews.at(-1).candidateSha, head);
  const runs = runsOf(snap, task.id);
  assert.ok(runs.every((x) => x.state === 'succeeded'), 'changes_requested is not a runtime failure');
  for (const x of runs) assert.deepEqual(Object.keys(x.modelSnapshot).sort(), ['model', 'profileId', 'provider']);
  assert.equal(runs[0].modelSnapshot.model, 'm-developer');
  assert.equal(t.usage.completeness, 'complete');
  assert.equal(t.usage.tokens.total, 116 * 6);
  assert.equal(t.usage.tokens.cacheRead, 30);
  assert.equal(summaries.length, 6);
  assert.equal(snap.controller.state, 'idle');
  assert.equal(existsSync(join(ctx.data, 'workflow/controller.lock')), false);
  assert.deepEqual(readdirSync(join(ctx.data, 'workflow/runs')), [], 'private run scratch cleaned');
  noSecrets(snap);
});

test('no-change polish still requires a fresh recheck; delivered SHA equals final candidate', async () => {
  const ctx = setup();
  const task = await ctx.prepare();
  const fr = fakeRunner();
  const res = await executeTasks({ dataDir: ctx.data, taskIds: [task.id], executeRunImpl: fr.fn });
  assert.equal(res.tasks[0].state, 'delivered');
  assert.deepEqual(fr.log.map((l) => l.role), ['developer', 'reviewer', 'polisher', 'reviewer']);
  const snap = await readWorkflow(ctx.data);
  const ds = snap.deliveries;
  assert.equal(ds.find((d) => d.state === 'final_candidate').candidateSha, ds.find((d) => d.state === 'delivered').candidateSha);
  assert.equal(snap.runs[2].summary.decision, 'no_change');
});

test('missing, stale or wrong-context reports and scope violations cannot deliver', async () => {
  const cases = [
    [{ role: 'reviewer', report: 'none' }, 'report_missing'],
    [{ role: 'developer', report: 'stale' }, 'report_stale'],
    [{ role: 'reviewer', report: 'wrongctx' }, 'report_stale'],
    [{ role: 'developer', file: 'src/b.js' }, 'scope_violation'],
    [{ role: 'developer', commit: false }, 'no_commit'],
  ];
  for (const [bad, category] of cases) {
    const ctx = setup();
    const task = await ctx.prepare();
    const fr = fakeRunner((role) => (role === bad.role ? bad : {}));
    const res = await executeTasks({ dataDir: ctx.data, taskIds: [task.id], executeRunImpl: fr.fn });
    assert.equal(res.tasks[0].state, 'failed', category);
    assert.equal(res.tasks[0].stateReason, category);
    const snap = await readWorkflow(ctx.data);
    assert.ok(!snap.deliveries.some((d) => d.state === 'delivered' || d.state === 'final_candidate'), category);
    if (category === 'scope_violation') assert.ok(g(ctx.wt, 'log', '--oneline').includes('developer'), 'out-of-scope commit preserved');
  }
});

test('fix rounds are bounded by settings then blocked', async () => {
  const ctx = setup();
  const task = await ctx.prepare();
  await updateSettings(ctx.data, { maxFixRounds: 1 });
  const fr = fakeRunner((role) => (role === 'reviewer' ? { verdict: 'changes_requested' } : {}));
  const res = await executeTasks({ dataDir: ctx.data, taskIds: [task.id], executeRunImpl: fr.fn });
  assert.equal(res.tasks[0].state, 'blocked');
  assert.equal(res.tasks[0].stateReason, 'fix_rounds_exhausted');
  assert.deepEqual(fr.log.map((l) => l.role), ['developer', 'reviewer', 'developer', 'reviewer']);
  await assert.rejects(executeTasks({ dataDir: ctx.data, taskIds: [task.id], resume: true, executeRunImpl: fr.fn }), /blocked/);
});

test('parallel max 2, queued reasons, same-worktree serialization', async () => {
  const ctx = setup({ worktrees: ['w1', 'w2', 'w3'] });
  const ids = [];
  for (const n of ['w1', 'w2', 'w3']) ids.push((await ctx.prepare({ worktree: ctx.wts[n], title: `t-${n}` })).id);
  const shared = (await ctx.prepare({ worktree: ctx.wts.w1, title: 'shared w1' })).id;
  let queuedSeen = null;
  const fr = fakeRunner((role, call) => (call === 0 ? { hold: sleep(80).then(async () => { queuedSeen = await readWorkflow(ctx.data); }) } : {}));
  const res = await executeTasks({ dataDir: ctx.data, taskIds: [...ids, shared], executeRunImpl: fr.fn });
  assert.ok(res.tasks.every((t) => t.state === 'delivered'), JSON.stringify(res.tasks));
  assert.equal(fr.stats.maxActive, 2);
  assert.equal(fr.stats.overlapSameWorktree, false);
  const reasons = queuedSeen.tasks.filter((t) => t.state === 'queued').map((t) => t.stateReason).sort();
  assert.ok(reasons.includes('concurrency') || reasons.includes('worktree'), JSON.stringify(reasons));
  assert.equal(queuedSeen.controller.state, 'running');
  assert.ok(queuedSeen.counts.queued >= 1);
  assert.deepEqual([...new Set(fr.log.map((l) => l.agentId))].sort(), ['Pi-01', 'Pi-02']);
  // The serialized shared-worktree task started from the delivered candidate of its predecessor.
  const snap = await readWorkflow(ctx.data);
  const first = snap.deliveries.find((d) => d.taskId === ids[0] && d.state === 'delivered');
  const sharedDev = snap.runs.find((r) => r.taskId === shared && r.role === 'developer');
  assert.equal(sharedDev.summary.startSha, first.candidateSha);
});

test('dependencies: wait, block until deliberately integrated, failed dependency blocks; cycles and bad IDs rejected', async () => {
  const ctx = setup({ worktrees: ['w1', 'w2', 'w3', 'w4'] });
  const a = await ctx.prepare({ worktree: ctx.wts.w1, title: 'A' });
  const b = await ctx.prepare({ worktree: ctx.wts.w2, title: 'B', dependencies: [a.id] });
  const fr = fakeRunner();
  let res = await executeTasks({ dataDir: ctx.data, taskIds: [b.id, a.id], executeRunImpl: fr.fn });
  assert.equal(res.tasks.find((t) => t.id === a.id).state, 'delivered');
  assert.deepEqual(res.tasks.find((t) => t.id === b.id), { id: b.id, state: 'blocked', stateReason: 'dependency_not_integrated', candidateSha: null, resumeRole: null });
  assert.ok(fr.log.every((l) => l.taskId === a.id), 'dependent never ran');
  // Codex integrates deliberately, then the dependent can run.
  g(ctx.wts.w2, 'merge', '--ff-only', '-q', res.tasks.find((t) => t.id === a.id).candidateSha);
  res = await executeTasks({ dataDir: ctx.data, taskIds: [b.id], executeRunImpl: fr.fn });
  assert.equal(res.tasks[0].state, 'delivered');
  assert.match(fr.log.find((l) => l.taskId === b.id).brief, /Dependency candidates/);

  const c = await ctx.prepare({ worktree: ctx.wts.w3, title: 'C' });
  const d = await ctx.prepare({ worktree: ctx.wts.w4, title: 'D', dependencies: [c.id] });
  const bad = fakeRunner((role) => (role === 'developer' ? { report: 'none' } : {}));
  res = await executeTasks({ dataDir: ctx.data, taskIds: [c.id, d.id], executeRunImpl: bad.fn });
  assert.equal(res.tasks.find((t) => t.id === c.id).state, 'failed');
  assert.equal(res.tasks.find((t) => t.id === d.id).stateReason, 'dependency_failed');

  editState(ctx.data, (s) => { s.tasks.find((t) => t.id === c.id).dependencies = [d.id]; });
  await assert.rejects(executeTasks({ dataDir: ctx.data, taskIds: [d.id], executeRunImpl: fr.fn }), /cycle/);
  await assert.rejects(executeTasks({ dataDir: ctx.data, taskIds: ['nope'], executeRunImpl: fr.fn }), WorkflowInputError);
  await assert.rejects(executeTasks({ dataDir: ctx.data, taskIds: [randomUUID()], executeRunImpl: fr.fn }), /does not exist/);
  editState(ctx.data, (s) => { s.tasks.find((t) => t.id === c.id).dependencies = []; s.tasks.find((t) => t.id === d.id).projectId = 'other'; });
  await assert.rejects(executeTasks({ dataDir: ctx.data, taskIds: [d.id], executeRunImpl: fr.fn }), /different project/);
});

test('budget: per-run cap from remaining budget, stop retained across resume; unknown usage stays unknown; history survives root cleanup', async () => {
  const ctx = setup();
  const task = await ctx.prepare({ budget: { maxTokens: 150, maxWallSeconds: 600, maxFixRounds: 2 } });
  const fr = fakeRunner((role, call, args) => (args.config.limits.maxTokens < 116
    ? { outcome: 'stopped', errorCategory: 'token_limit', usage: { ...OK_USAGE(), tokens: { input: 30, output: 10, cacheRead: 0, cacheWrite: 0, total: 40 }, usageCompleteness: 'partial' } }
    : {}));
  let res = await executeTasks({ dataDir: ctx.data, taskIds: [task.id], executeRunImpl: fr.fn });
  assert.equal(fr.log[1].limits.maxTokens, 34, 'cap = min(profile, remaining known budget)');
  assert.ok(fr.log[1].limits.maxWallSeconds <= 60);
  assert.deepEqual([res.tasks[0].state, res.tasks[0].stateReason, res.tasks[0].resumeRole], ['stopped', 'budget_tokens', 'reviewer']);
  res = await executeTasks({ dataDir: ctx.data, taskIds: [task.id], resume: true, executeRunImpl: fr.fn });
  assert.equal(res.tasks[0].stateReason, 'budget_tokens');
  assert.equal(fr.log.length, 2, 'no run started without budget');
  let snap = await readWorkflow(ctx.data);
  assert.equal(snap.tasks[0].usage.completeness, 'partial');
  assert.equal(snap.tasks[0].usage.tokens.total, null);
  assert.equal(snap.tasks[0].usage.knownSubtotal, 156);

  // unknown usage never becomes zero
  const u = aggregateUsage([{ pid: 1, usage: OK_USAGE() }, { pid: 2 }]);
  assert.equal(u.completeness, 'partial'); assert.equal(u.tokens.total, null); assert.equal(u.tokens.input, null); assert.equal(u.estimatedCostUsd, null);
  assert.equal(aggregateUsage([{ pid: 3 }]).completeness, 'unknown');

  g(ctx.main, 'worktree', 'remove', '--force', ctx.wt);
  snap = await readWorkflow(ctx.data);
  assert.equal(snap.tasks[0].worktreeExists, false);
  assert.equal(snap.runs.length, 2);
  assert.equal(snap.deliveries[0].state, 'first');
});

test('stop request aborts only the targeted run; other task delivers', async () => {
  const ctx = setup({ worktrees: ['w1', 'w2'] });
  const a = await ctx.prepare({ worktree: ctx.wts.w1, title: 'A' });
  const b = await ctx.prepare({ worktree: ctx.wts.w2, title: 'B' });
  let release;
  const gate = new Promise((r) => { release = r; });
  const fr = fakeRunner((role, call) => (role === 'developer' ? { hold: gate } : {}));
  const p = executeTasks({ dataDir: ctx.data, taskIds: [a.id, b.id], executeRunImpl: fr.fn, pollMs: 20 });
  let runA;
  for (let i = 0; i < 200 && !runA; i += 1) {
    await sleep(10);
    runA = (await readWorkflow(ctx.data)).runs.find((r) => r.taskId === a.id && r.state === 'running');
  }
  assert.ok(runA, 'run A observed running');
  const reqId = randomUUID();
  const st = await requestStop(ctx.data, runA.id, reqId);
  assert.equal(st.duplicate, false);
  assert.equal((await requestStop(ctx.data, runA.id, reqId)).duplicate, true);
  await sleep(150);
  release();
  const res = await p;
  assert.deepEqual([res.tasks[0].state, res.tasks[0].stateReason, res.tasks[0].resumeRole], ['stopped', 'stop_requested', 'developer']);
  assert.equal(res.tasks[1].state, 'delivered');
  const snap = await readWorkflow(ctx.data);
  assert.equal(snap.runs.find((r) => r.id === runA.id).state, 'stopped');
  assert.equal(readdirSync(join(ctx.data, 'workflow/requests')).length, 0, 'handled request removed');
});

test('resume: explicit flag, refuses dirty/diverged worktree, preserves work; then continues from failed role', async () => {
  const ctx = setup();
  const task = await ctx.prepare();
  let n = 0;
  const fr = fakeRunner((role) => (role === 'reviewer' && n++ === 0 ? { report: 'none' } : {}));
  let res = await executeTasks({ dataDir: ctx.data, taskIds: [task.id], executeRunImpl: fr.fn });
  assert.deepEqual([res.tasks[0].state, res.tasks[0].resumeRole], ['failed', 'reviewer']);
  const cand = res.tasks[0].candidateSha;
  await assert.rejects(executeTasks({ dataDir: ctx.data, taskIds: [task.id], executeRunImpl: fr.fn }), /--resume/);
  writeFileSync(join(ctx.wt, 'src/a.js'), 'local edits\n');
  await assert.rejects(executeTasks({ dataDir: ctx.data, taskIds: [task.id], resume: true, executeRunImpl: fr.fn }), /uncommitted/);
  assert.equal(readFileSync(join(ctx.wt, 'src/a.js'), 'utf8'), 'local edits\n', 'dirty work preserved');
  g(ctx.wt, 'commit', '-qam', 'manual');
  await assert.rejects(executeTasks({ dataDir: ctx.data, taskIds: [task.id], resume: true, executeRunImpl: fr.fn }), /does not match the recorded candidate/);
  g(ctx.wt, 'reset', '-q', '--hard', cand);
  const before = fr.log.length;
  res = await executeTasks({ dataDir: ctx.data, taskIds: [task.id], resume: true, executeRunImpl: fr.fn });
  assert.equal(res.tasks[0].state, 'delivered');
  assert.equal(fr.log[before].role, 'reviewer', 'resumed from failed role');
  const snap = await readWorkflow(ctx.data);
  assert.equal(snap.runs.filter((r) => r.state === 'failed').length, 1, 'prior run preserved');
});

test('unknown interrupted runs: never auto-replayed, live PID refuses, acknowledgement required', async () => {
  const ctx = setup();
  const task = await ctx.prepare();
  let n = 0;
  const fr = fakeRunner((role) => (role === 'reviewer' && n++ === 0 ? { report: 'none' } : {}));
  await executeTasks({ dataDir: ctx.data, taskIds: [task.id], executeRunImpl: fr.fn });
  editState(ctx.data, (s) => {
    const run = s.runs.at(-1); run.state = 'running'; run.pid = process.pid;
    s.tasks[0].state = 'checking';
  });
  let snap = await readWorkflow(ctx.data);
  assert.equal(snap.runs.at(-1).state, 'unknown');
  assert.equal(snap.counts.unknown, 1);
  const calls = fr.log.length;
  await assert.rejects(executeTasks({ dataDir: ctx.data, taskIds: [task.id], executeRunImpl: fr.fn }), /--resume/);
  await assert.rejects(executeTasks({ dataDir: ctx.data, taskIds: [task.id], resume: true, executeRunImpl: fr.fn }), /acknowledge/);
  editState(ctx.data, (s) => { s.runs.at(-1).pid = process.pid; });
  await assert.rejects(executeTasks({ dataDir: ctx.data, taskIds: [task.id], resume: true, acknowledgeInterruption: true, executeRunImpl: fr.fn }), /appears alive/);
  assert.equal(fr.log.length, calls, 'nothing replayed');
  editState(ctx.data, (s) => { s.runs.at(-1).pid = DEAD_PID; });
  const res = await executeTasks({ dataDir: ctx.data, taskIds: [task.id], resume: true, acknowledgeInterruption: true, executeRunImpl: fr.fn });
  assert.equal(res.tasks[0].state, 'delivered');
  snap = await readWorkflow(ctx.data);
  assert.ok(snap.runs.some((r) => r.state === 'interrupted'));
});

test('changed profile config requires a new prepare; settings select registered profile for the next run', async () => {
  const ctx = setup();
  const task = await ctx.prepare();
  const original = readFileSync(ctx.profiles.reviewer, 'utf8');
  writeFileSync(ctx.profiles.reviewer, original.replace('m-reviewer', 'm-other'));
  const fr = fakeRunner();
  await assert.rejects(executeTasks({ dataDir: ctx.data, taskIds: [task.id], executeRunImpl: fr.fn }), /prepare a new task/);
  assert.equal(fr.log.length, 0);
  writeFileSync(ctx.profiles.reviewer, original);

  // Register a second reviewer profile through a trusted prepare, then select it for future runs.
  const alt = ctx.writeProfile('reviewer-alt', 'reviewer', { model: 'm-reviewer-v2' });
  const other = await ctx.prepare({ profiles: { ...ctx.profiles, reviewer: alt }, title: 'other' });
  const altId = (await readWorkflow(ctx.data)).tasks.find((t) => t.id === other.id).profileIds.reviewer;
  await updateSettings(ctx.data, { defaultProfiles: { demo: { reviewer: altId } } });
  const res = await executeTasks({ dataDir: ctx.data, taskIds: [task.id], executeRunImpl: fr.fn });
  assert.equal(res.tasks[0].state, 'delivered');
  const snap = await readWorkflow(ctx.data);
  const revRuns = snap.runs.filter((r) => r.taskId === task.id && r.role === 'reviewer');
  assert.ok(revRuns.every((r) => r.modelSnapshot.model === 'm-reviewer-v2' && r.profileId === altId));
  assert.equal(snap.runs.find((r) => r.taskId === task.id && r.role === 'developer').modelSnapshot.model, 'm-developer');

  // Profile limits above practical caps are rejected at registration.
  const huge = ctx.writeProfile('dev-huge', 'developer', { limits: { maxWallSeconds: 86_401, maxTokens: 10 } });
  await assert.rejects(ctx.prepare({ profiles: { ...ctx.profiles, developer: huge } }), /maxWallSeconds/);
});

test('controller signal aborts all owned runs, waits for summaries, then releases the lease', async () => {
  const ctx = setup({ worktrees: ['w1', 'w2'] });
  const a = await ctx.prepare({ worktree: ctx.wts.w1 });
  const b = await ctx.prepare({ worktree: ctx.wts.w2 });
  const ac = new AbortController();
  const fr = fakeRunner(() => ({ hold: new Promise(() => {}) }));
  const p = executeTasks({ dataDir: ctx.data, taskIds: [a.id, b.id], executeRunImpl: fr.fn, signal: ac.signal });
  for (let i = 0; i < 200 && fr.log.length < 2; i += 1) await sleep(10);
  ac.abort();
  const res = await p;
  assert.ok(res.tasks.every((t) => t.state === 'stopped' && t.stateReason === 'controller_signal'));
  assert.equal(existsSync(join(ctx.data, 'workflow/controller.lock')), false);
  const snap = await readWorkflow(ctx.data);
  assert.ok(snap.runs.every((r) => r.state === 'stopped'));
});

test('persistence callback failure aborts the run and success is impossible', { skip: process.getuid?.() === 0 }, async () => {
  const ctx = setup();
  const task = await ctx.prepare();
  let release;
  const gate = new Promise((r) => { release = r; });
  const fr = fakeRunner((role) => (role === 'developer' ? { hold: gate } : {}));
  const p = executeTasks({ dataDir: ctx.data, taskIds: [task.id], executeRunImpl: fr.fn });
  for (let i = 0; i < 200 && fr.log.length < 1; i += 1) await sleep(10);
  await sleep(40);
  const dir = join(ctx.data, 'workflow');
  chmodSync(dir, 0o500);
  release();
  let res;
  try { res = await p; } finally { chmodSync(dir, 0o700); }
  assert.equal(res.fatal, 'persistence_failed');
  assert.notEqual(res.tasks[0].state, 'delivered');
  assert.equal(fr.log.length, 1, 'no further roles after fatal write failure');
  const snap = await readWorkflow(ctx.data);
  assert.ok(!snap.deliveries.length);
  assert.equal(snap.runs[0].state, 'unknown', 'last durable record is unverified, not success');
});

test('lost controller lease aborts own runs and makes no further authoritative writes', async () => {
  const ctx = setup();
  const task = await ctx.prepare();
  const fr = fakeRunner(() => ({ hold: new Promise(() => {}) }));
  const p = executeTasks({ dataDir: ctx.data, taskIds: [task.id], executeRunImpl: fr.fn, heartbeatMs: 30 });
  for (let i = 0; i < 200 && fr.log.length < 1; i += 1) await sleep(10);
  await sleep(40);
  const owner = join(ctx.data, 'workflow/controller.lock/owner.json');
  const o = JSON.parse(readFileSync(owner, 'utf8'));
  writeFileSync(owner, JSON.stringify({ ...o, token: randomUUID() }));
  const stateBefore = readFileSync(join(ctx.data, 'workflow/state.json'), 'utf8');
  const res = await p;
  assert.equal(res.fatal, 'controller_lost');
  assert.equal(readFileSync(join(ctx.data, 'workflow/state.json'), 'utf8'), stateBefore, 'no stale writes');
  assert.ok(existsSync(owner), 'foreign lock not removed');
});

function realEnv(ctx, extra = {}) {
  return { ...process.env, FAKE_PI_KEY: 'secret-value-123', FAKE_PI_SCENARIO: 'chain', FAKE_PI_COUNTER: join(ctx.root, 'counter'), ...extra };
}

test('actual runner + fake Pi: full chain with fix, polish and recheck verified from real reports', async () => {
  const ctx = setup();
  const task = await ctx.prepare();
  const env = realEnv(ctx, { FAKE_PI_REVIEWS: 'changes,pass,pass', FAKE_PI_POLISH: 'changed' });
  const res = await executeTasks({ dataDir: ctx.data, taskIds: [task.id], env });
  assert.equal(res.fatal, null);
  assert.equal(res.tasks[0].state, 'delivered', JSON.stringify(res));
  const snap = await readWorkflow(ctx.data);
  const runs = runsOf(snap, task.id);
  assert.deepEqual(runs.map((r) => r.role), ['developer', 'reviewer', 'developer', 'reviewer', 'polisher', 'reviewer']);
  assert.deepEqual(runs.map((r) => r.summary.verdict ?? r.summary.decision), ['changed', 'changes_requested', 'changed', 'pass', 'changed', 'pass']);
  assert.equal(snap.deliveries.at(-1).candidateSha, g(ctx.wt, 'rev-parse', 'HEAD'));
  assert.ok(runs.every((r) => r.usage.tokens.total === 116));
  assert.ok(runs.every((r) => r.events.some((e) => e.type === 'tool' && e.summary === 'read')));
  noSecrets(snap);
  noSecrets(readFileSync(join(ctx.data, 'workflow/state.json'), 'utf8').replace(/"authEnv"[^,]*,|"piCommand":\[[^\]]*\],|"instructions":\[[^\]]*\],|"configFile":"[^"]*",/g, ''));
});

test('flow CLI: private data dir, prepare/execute/snapshot/stop/settings with small JSON', () => {
  const ctx = setup();
  const data = join(ctx.root, 'cli-data');
  const input = join(ctx.root, 'prepare.json');
  writeFileSync(input, JSON.stringify({
    project: { id: 'demo', name: 'Demo' }, repository: ctx.main, worktree: ctx.wt, title: 'cli', goal: 'g', scope: ['src/a.js'],
    acceptance: ['ok'], context: { version: 1, text: 'ctx', sources: [] }, profiles: ctx.profiles,
  }));
  const cli = (args, env = process.env) => spawnSync(process.execPath, [flowCli, ...args], { encoding: 'utf8', env, cwd: ctx.root });
  let r = cli(['prepare', '--input', 'prepare.json', '--data-dir', 'cli-data']);
  assert.equal(r.status, 0, r.stderr);
  const { taskId } = JSON.parse(r.stdout);
  assert.match(taskId, /^[0-9a-f-]{36}$/);
  r = cli(['execute', '--task', taskId, '--data-dir', data], realEnv(ctx));
  assert.equal(r.status, 0, r.stderr + r.stdout);
  assert.equal(JSON.parse(r.stdout).tasks[0].state, 'delivered');
  assert.ok(!r.stdout.includes('secret-value-123') && !r.stderr.includes('secret-value-123'));
  r = cli(['snapshot', '--data-dir', data]);
  assert.equal(r.status, 0);
  const snap = JSON.parse(r.stdout);
  assert.equal(snap.tasks[0].state, 'delivered');
  noSecrets(snap);
  r = cli(['stop', '--run', randomUUID(), '--request-id', randomUUID(), '--data-dir', data]);
  assert.equal(r.status, 2);
  assert.match(JSON.parse(r.stderr).error, /run does not exist/);
  writeFileSync(join(ctx.root, 'settings.json'), JSON.stringify({ maxConcurrency: 3 }));
  r = cli(['settings', '--input', join(ctx.root, 'settings.json'), '--data-dir', data]);
  assert.equal(JSON.parse(r.stdout).maxConcurrency, 3);
  const open = join(ctx.root, 'open-data');
  mkdirSync(open, { mode: 0o755 }); chmodSync(open, 0o755);
  r = cli(['snapshot', '--data-dir', open]);
  assert.equal(r.status, 2);
  assert.match(r.stderr, /private/);
  r = cli(['start', '--data-dir', data]);
  assert.equal(r.status, 2);
});

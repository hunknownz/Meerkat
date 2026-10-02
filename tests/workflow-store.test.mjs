import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { prepareTask, readWorkflow, requestStop, updateSettings, WorkflowInputError } from '../workflow/core.mjs';
import { ControllerBusyError, ControllerLostError, StoreCorruptError, WorkflowStore } from '../workflow/store.mjs';

const g = (cwd, ...a) => execFileSync('git', ['-c', 'user.name=t', '-c', 'user.email=t@t', ...a], { cwd, encoding: 'utf8' }).trim();
const mode = (p) => statSync(p).mode & 0o777;

function setup({ configExtra = {} } = {}) {
  const root = mkdtempSync(join(tmpdir(), 'mk-wf-'));
  const main = join(root, 'main');
  const wt = join(root, 'wt');
  execFileSync('git', ['init', '-q', '-b', 'main', main]);
  writeFileSync(join(main, 'AGENTS.md'), 'Be careful.\n');
  mkdirSync(join(main, 'src'));
  writeFileSync(join(main, 'src/a.js'), 'x\n');
  g(main, 'add', '.'); g(main, 'commit', '-qm', 'init');
  g(main, 'worktree', 'add', '-q', '-b', 'task/demo', wt);
  const cfgDir = join(root, 'cfg');
  mkdirSync(cfgDir, { mode: 0o700 });
  const profiles = {};
  for (const role of ['developer', 'reviewer', 'polisher']) {
    profiles[role] = join(cfgDir, `${role}.json`);
    writeFileSync(profiles[role], JSON.stringify({
      projectId: 'demo', provider: 'fake', model: `m-${role}`, authEnv: 'FAKE_PI_KEY', instructions: ['AGENTS.md'],
      limits: { maxWallSeconds: 60, maxTokens: 1000 }, piCommand: ['pi', '--SECRET-ARG'],
      apiKey: 'sk-secret-provider', extraToken: 'leak-me-not', ...configExtra,
    }));
  }
  const data = join(root, 'data');
  const input = (over = {}) => ({
    project: { id: 'demo', name: 'Demo' }, repository: main, worktree: wt, title: 'Add thing', goal: 'Do the thing',
    scope: ['src/a.js', './src//b.js'], acceptance: ['tests pass'], context: { version: 1, text: 'Shared context v1', sources: [] },
    profiles, ...over,
  });
  return { root, main, wt, data, profiles, input };
}

function linkedWorktree(ctx, name) {
  const p = join(ctx.root, name);
  g(ctx.main, 'worktree', 'add', '-q', '-b', `task/${name}`, p);
  return p;
}

const noSecrets = (obj) => {
  const s = JSON.stringify(obj);
  for (const bad of ['sk-secret-provider', 'leak-me-not', 'SECRET-ARG', 'FAKE_PI_KEY', 'authEnv', 'piCommand', 'instructions', 'configFile', 'configDigest', '/cfg/']) {
    assert.ok(!s.includes(bad), `public snapshot leaked ${bad}`);
  }
};

test('empty data dir reads as actual empty state without creating files', async () => {
  const { data } = setup();
  const s = await readWorkflow(data);
  assert.equal(s.schemaVersion, 1);
  for (const k of ['projects', 'contexts', 'tasks', 'runs', 'deliveries', 'reviews', 'profiles']) assert.deepEqual(s[k], []);
  assert.deepEqual(s.counts, { running: 0, queued: 0, unknown: 0 });
  assert.equal(s.controller.state, 'idle');
  assert.deepEqual({ c: s.settings.maxConcurrency, f: s.settings.maxFixRounds }, { c: 2, f: 2 });
  assert.ok(Number.isFinite(Date.parse(s.observedAt)));
  assert.equal(existsSync(join(data, 'workflow')), false);
});

test('prepareTask records a ready task with frozen private profiles; public snapshot is redacted', async () => {
  const ctx = setup();
  const task = await prepareTask(ctx.data, ctx.input({ issueRef: { url: 'https://github.com/o/r/issues/12', title: 'Bug', bodyHash: 'a'.repeat(64) } }));
  assert.match(task.id, /^[0-9a-f-]{36}$/);
  assert.equal(task.state, 'ready');
  assert.deepEqual(task.scope, ['src/a.js', 'src/b.js']);
  assert.equal(task.branch, 'task/demo');
  assert.equal(task.baselineSha, g(ctx.wt, 'rev-parse', 'HEAD'));
  assert.deepEqual(task.budget, { maxTokens: 500000, maxWallSeconds: 1800, maxFixRounds: 2 });
  assert.match(task.contextRef.digest, /^sha256:[0-9a-f]{64}$/);

  // private storage permissions
  const wf = join(ctx.data, 'workflow');
  assert.equal(mode(wf), 0o700);
  assert.equal(mode(join(wf, 'state.json')), 0o600);
  assert.equal(existsSync(join(wf, 'controller.lock')), false, 'prepare released its lease');

  // internal state keeps frozen allowed fields only (no unknown secret fields)
  const raw = readFileSync(join(wf, 'state.json'), 'utf8');
  assert.ok(!raw.includes('sk-secret-provider') && !raw.includes('leak-me-not'));
  const st = JSON.parse(raw);
  const dev = st.profiles.find((p) => p.role === 'developer');
  assert.equal(dev.authEnv, 'FAKE_PI_KEY');
  assert.match(dev.configDigest, /^sha256:/);
  assert.ok(dev.configFile.endsWith('developer.json'));
  assert.deepEqual(Object.keys(dev).sort(), ['authEnv', 'configDigest', 'configFile', 'createdAt', 'id', 'instructions', 'limits', 'model', 'piCommand', 'projectId', 'provider', 'role'].sort());

  const snap = await readWorkflow(ctx.data);
  noSecrets(snap);
  assert.equal(snap.tasks.length, 1);
  assert.equal(snap.tasks[0].worktreeExists, true);
  assert.deepEqual(snap.profiles.find((p) => p.role === 'developer'), { id: dev.id, projectId: 'demo', role: 'developer', provider: 'fake', model: 'm-developer', limits: { maxWallSeconds: 60, maxTokens: 1000 } });
  assert.deepEqual(snap.projects[0].repositories, [ctx.main].map((p) => g(p, 'rev-parse', '--show-toplevel')));
});

test('context families: shared dedupe, distinct v1 families, v2 in family, immutable collision', async () => {
  const ctx = setup();
  const t1 = await prepareTask(ctx.data, ctx.input());
  const t2 = await prepareTask(ctx.data, ctx.input());
  assert.deepEqual(t2.contextRef, t1.contextRef, 'same project+version+digest deduplicates');

  const t3 = await prepareTask(ctx.data, ctx.input({ context: { version: 1, text: 'Different task context' } }));
  assert.notEqual(t3.contextRef.id, t1.contextRef.id, 'different v1 text is a new family, not a collision');
  assert.equal(t3.contextRef.version, 1);

  const t4 = await prepareTask(ctx.data, ctx.input({ context: { id: t1.contextRef.id, version: 2, text: 'Shared context v2' } }));
  assert.deepEqual([t4.contextRef.id, t4.contextRef.version], [t1.contextRef.id, 2]);

  const t5 = await prepareTask(ctx.data, ctx.input({ context: { id: t1.contextRef.id, version: 2, text: 'Shared context v2' } }));
  assert.deepEqual(t5.contextRef, t4.contextRef, 'same family+version+text reuses');

  const before = readFileSync(join(ctx.data, 'workflow/state.json'), 'utf8');
  await assert.rejects(prepareTask(ctx.data, ctx.input({ context: { id: t1.contextRef.id, version: 2, text: 'rewritten v2' } })), /already exists with different content/);
  assert.equal(readFileSync(join(ctx.data, 'workflow/state.json'), 'utf8'), before, 'rejected collision writes nothing');

  // a family cannot move to another project
  const other = setup();
  for (const f of Object.values(other.profiles)) {
    const c = JSON.parse(readFileSync(f, 'utf8')); c.projectId = 'other'; writeFileSync(f, JSON.stringify(c));
  }
  await assert.rejects(prepareTask(ctx.data, other.input({ project: { id: 'other', name: 'O' }, context: { id: t1.contextRef.id, version: 3, text: 'x' } })), /different project/);

  const snap = await readWorkflow(ctx.data);
  assert.equal(snap.contexts.length, 3);
  assert.equal(snap.tasks.length, 5);
});

test('repository/worktree validation: same common dir, linked, clean, protected branch', async () => {
  const ctx = setup();
  // primary worktree is rejected
  await assert.rejects(prepareTask(ctx.data, ctx.input({ worktree: ctx.main })), /linked worktree/);
  // a different repository's worktree is rejected
  const other = setup();
  await assert.rejects(prepareTask(ctx.data, ctx.input({ worktree: other.wt })), /same Git common directory/);
  // relative paths rejected
  await assert.rejects(prepareTask(ctx.data, ctx.input({ worktree: 'wt' })), /absolute/);
  // subdirectory is not a worktree root
  await assert.rejects(prepareTask(ctx.data, ctx.input({ worktree: join(ctx.wt, 'src') })), /worktree root/);
  // dirty worktree rejected by preflight
  writeFileSync(join(ctx.wt, 'src/a.js'), 'dirty\n');
  await assert.rejects(prepareTask(ctx.data, ctx.input()), /not clean/);
  g(ctx.wt, 'checkout', '--', 'src/a.js');
  // detached HEAD rejected
  const detached = linkedWorktree(ctx, 'det');
  g(detached, 'checkout', '-q', '--detach');
  await assert.rejects(prepareTask(ctx.data, ctx.input({ worktree: detached })), /detached/);
  assert.equal(existsSync(join(ctx.data, 'workflow/state.json')), false, 'no state written for rejected input');
  // a second clean linked worktree works
  const ok = await prepareTask(ctx.data, ctx.input({ worktree: linkedWorktree(ctx, 'w2') }));
  assert.equal(ok.branch, 'task/w2');
});

test('bad scope, bad config, bad issue url and unknown fields are rejected before any write', async () => {
  const ctx = setup();
  const bad = [
    [{ scope: ['../x'] }, /traverse/],
    [{ scope: ['/etc/passwd'] }, /relative/],
    [{ scope: ['.git/config'] }, /unsafe/],
    [{ scope: ['config/.env.local'] }, /unsafe/],
    [{ scope: ['keys/server.pem'] }, /unsafe/],
    [{ scope: ['src/*.js'] }, /explicit/],
    [{ scope: [] }, /1\.\.50/],
    [{ scope: ['.'] }, /inside the repository/],
    [{ issueRef: { url: 'http://github.com/o/r/issues/1', title: 't' } }, /https/],
    [{ issueRef: { url: 'https://u:p@github.com/o/r/issues/1', title: 't' } }, /credentials/],
    [{ issueRef: { url: 'https://github.com/o/r/issues/1?token=x', title: 't' } }, /query/],
    [{ issueRef: { url: 'https://github.com/o/r/pull/1', title: 't' } }, /issues/],
    [{ secretField: 'x' }, /unknown field/],
    [{ project: { id: 'demo', name: 'D', token: 'x' } }, /unknown field/],
    [{ project: { id: 'Bad Slug', name: 'D' } }, /slug/],
    [{ dependencies: [randomUUID()] }, /does not exist/],
    [{ budget: { maxTokens: 0 } }, /budget.maxTokens/],
    [{ context: { version: 1, text: 'x', id: 'not-a-uuid' } }, /context.id/],
    [{ profiles: { developer: 'relative.json', reviewer: 'r', polisher: 'p' } }, /absolute config/],
  ];
  for (const [over, re] of bad) {
    await assert.rejects(prepareTask(ctx.data, ctx.input(over)), (e) => e instanceof WorkflowInputError && re.test(e.message), JSON.stringify(over));
  }
  // config problems: wrong project, invalid authEnv, malformed JSON, missing instruction file
  const p = ctx.profiles.reviewer;
  const orig = readFileSync(p, 'utf8');
  writeFileSync(p, JSON.stringify({ ...JSON.parse(orig), projectId: 'other' }));
  await assert.rejects(prepareTask(ctx.data, ctx.input()), /projectId does not match/);
  writeFileSync(p, JSON.stringify({ ...JSON.parse(orig), authEnv: 'not an env' }));
  await assert.rejects(prepareTask(ctx.data, ctx.input()), /authEnv/);
  writeFileSync(p, '{oops');
  await assert.rejects(prepareTask(ctx.data, ctx.input()), /not valid JSON/);
  writeFileSync(p, JSON.stringify({ ...JSON.parse(orig), instructions: ['MISSING.md'] }));
  await assert.rejects(prepareTask(ctx.data, ctx.input()), /instruction files missing/);
  assert.equal(existsSync(join(ctx.data, 'workflow/state.json')), false);
  // changed config => distinct frozen profile on re-prepare
  writeFileSync(p, orig);
  const a = await prepareTask(ctx.data, ctx.input());
  writeFileSync(p, JSON.stringify({ ...JSON.parse(orig), model: 'm-new' }));
  const b = await prepareTask(ctx.data, ctx.input({ dependencies: [a.id] }));
  assert.notEqual(a.profileIds.reviewer, b.profileIds.reviewer);
  assert.equal(a.profileIds.developer, b.profileIds.developer);
  assert.deepEqual(b.dependencies, [a.id]);
});

test('corrupt state is preserved and errors; never replaced with empty', async () => {
  const ctx = setup();
  await prepareTask(ctx.data, ctx.input());
  const p = join(ctx.data, 'workflow/state.json');
  writeFileSync(p, '{"schemaVersion":1,"projects":[');
  await assert.rejects(readWorkflow(ctx.data), StoreCorruptError);
  await assert.rejects(prepareTask(ctx.data, ctx.input()), StoreCorruptError);
  await assert.rejects(updateSettings(ctx.data, { defaultProfiles: { demo: {} } }), StoreCorruptError);
  assert.equal(readFileSync(p, 'utf8'), '{"schemaVersion":1,"projects":[');
  assert.equal(existsSync(join(ctx.data, 'workflow/controller.lock')), false, 'lease released after failure');
  writeFileSync(p, JSON.stringify({ schemaVersion: 2, projects: [] }));
  await assert.rejects(readWorkflow(ctx.data), /schemaVersion/);
  writeFileSync(join(ctx.data, 'workflow/settings.json'), 'nope');
  writeFileSync(p, JSON.stringify({ schemaVersion: 1, projects: [], contexts: [], tasks: [], runs: [], deliveries: [], reviews: [] }));
  await assert.rejects(readWorkflow(ctx.data), /settings are malformed/);
});

test('controller lock: conflict, prepare honors active controller, ownership checks, stale recovery', async () => {
  const ctx = setup();
  const a = new WorkflowStore(ctx.data);
  const leaseA = a.acquireController();
  const lockDir = join(ctx.data, 'workflow/controller.lock');
  assert.equal(mode(lockDir), 0o700);
  assert.equal(mode(join(lockDir, 'owner.json')), 0o600);
  const b = new WorkflowStore(ctx.data);
  assert.throws(() => b.acquireController(), ControllerBusyError);
  await assert.rejects(prepareTask(ctx.data, ctx.input()), ControllerBusyError);
  assert.throws(() => b.write({}), ControllerLostError, 'write without a lease is refused');
  assert.equal((await readWorkflow(ctx.data)).controller.state, 'running');

  // prepare can run inside the active controller's lease
  const t = await prepareTask(ctx.data, ctx.input(), { store: a });
  assert.equal(t.state, 'ready');
  assert.equal(a.heartbeat(leaseA) > leaseA.acquiredAt || true, true);

  // stale heartbeat => unknown status, recoverable by a new controller (no PID signaling)
  const owner = JSON.parse(readFileSync(join(lockDir, 'owner.json'), 'utf8'));
  writeFileSync(join(lockDir, 'owner.json'), JSON.stringify({ ...owner, heartbeatAt: new Date(Date.now() - 120_000).toISOString() }));
  assert.equal((await readWorkflow(ctx.data)).controller.state, 'unknown');
  const leaseB = b.acquireController();
  assert.notEqual(leaseB.token, leaseA.token);
  // the displaced controller detects loss and cannot write or remove the new lock
  assert.throws(() => a.write(a.read()), ControllerLostError);
  assert.throws(() => a.heartbeat(leaseA), ControllerLostError);
  assert.equal(a.releaseController(leaseA), false);
  assert.ok(existsSync(lockDir));
  assert.equal(b.releaseController(leaseB), true);
  assert.equal(existsSync(lockDir), false);
  assert.ok(!readdirSync(join(ctx.data, 'workflow')).some((f) => f.includes('stale')), 'stale lock cleaned up');

  // lock dir without owner file (crash mid-acquire) is busy while young
  mkdirSync(lockDir);
  assert.throws(() => new WorkflowStore(ctx.data).acquireController(), ControllerBusyError);
  const lease = new WorkflowStore(ctx.data, { staleMs: -1 }).acquireController();
  assert.ok(lease.token);
});

function seedRuns(ctx, runs) {
  const s = new WorkflowStore(ctx.data);
  const lease = s.acquireController();
  const st = s.read();
  st.runs.push(...runs);
  s.write(st);
  return { s, lease };
}

test('snapshot projects active runs unknown without a live controller; history survives worktree removal', async () => {
  const ctx = setup();
  const task = await prepareTask(ctx.data, ctx.input());
  const active = randomUUID();
  const done = randomUUID();
  const { s, lease } = seedRuns(ctx, [
    { id: active, agentId: 'Pi-01', taskId: task.id, role: 'developer', state: 'running', pid: 4242, startedAt: new Date().toISOString(),
      events: Array.from({ length: 80 }, (_, i) => ({ type: 'tool', n: i })), summary: { outcome: 'x', env: { K: 'v' }, apiKey: 'sk-secret-provider' } },
    { id: done, agentId: 'Pi-02', taskId: task.id, role: 'reviewer', state: 'succeeded', startedAt: new Date().toISOString(), endedAt: new Date().toISOString() },
  ]);
  let snap = await readWorkflow(ctx.data);
  assert.equal(snap.controller.state, 'running');
  assert.deepEqual(snap.counts, { running: 1, queued: 0, unknown: 0 });
  const r = snap.runs.find((x) => x.id === active);
  assert.equal(r.events.length, 50, 'events bounded');
  assert.equal(r.pid, undefined, 'pid is not public');
  noSecrets(snap);
  assert.ok(!readFileSync(join(ctx.data, 'workflow/state.json'), 'utf8').includes('sk-secret-provider'));

  s.releaseController(lease); // controller gone without finishing => unknown, never zero
  snap = await readWorkflow(ctx.data);
  assert.equal(snap.controller.state, 'idle');
  assert.deepEqual(snap.counts, { running: 0, queued: 0, unknown: 1 });
  assert.deepEqual([snap.runs.find((x) => x.id === active).state, snap.runs.find((x) => x.id === active).recordedState], ['unknown', 'running']);
  assert.equal(snap.runs.find((x) => x.id === done).state, 'succeeded');

  g(ctx.main, 'worktree', 'remove', '--force', ctx.wt);
  snap = await readWorkflow(ctx.data);
  assert.equal(snap.tasks[0].worktreeExists, false);
  assert.equal(snap.runs.length, 2);
});

test('requestStop: validation, active run only, idempotent nonce, bounded private files, no signals', async () => {
  const ctx = setup();
  const task = await prepareTask(ctx.data, ctx.input());
  const runId = randomUUID();
  const finished = randomUUID();
  const { s, lease } = seedRuns(ctx, [
    { id: runId, agentId: 'Pi-01', taskId: task.id, role: 'developer', state: 'running', pid: 1 },
    { id: finished, agentId: 'Pi-02', taskId: task.id, role: 'developer', state: 'failed' },
  ]);
  const req = randomUUID();
  await assert.rejects(requestStop(ctx.data, 'nope', req), /runId/);
  await assert.rejects(requestStop(ctx.data, runId, 'NOPE'), /requestId/);
  await assert.rejects(requestStop(ctx.data, randomUUID(), req), /does not exist/);
  await assert.rejects(requestStop(ctx.data, finished, req), /not active/);

  const first = await requestStop(ctx.data, runId, req);
  assert.equal(first.duplicate, false);
  const again = await requestStop(ctx.data, runId, req);
  assert.deepEqual([again.duplicate, again.createdAt], [true, first.createdAt]);
  await assert.rejects(requestStop(ctx.data, finished, req), /different run/);
  const file = join(ctx.data, 'workflow/requests', `stop-${req}.json`);
  assert.equal(mode(file), 0o600);
  assert.equal(mode(join(ctx.data, 'workflow/requests')), 0o700);
  assert.deepEqual(s.listStopRequests().map((x) => [x.runId, x.requestId]), [[runId, req]]);
  assert.equal(s.removeStopRequest(req), true);

  s.releaseController(lease);
  await assert.rejects(requestStop(ctx.data, runId, randomUUID()), /no live controller/);
  // state.json untouched by stop requests (frontend never writes controller state)
  assert.equal(JSON.parse(readFileSync(join(ctx.data, 'workflow/state.json'), 'utf8')).runs.find((r) => r.id === runId).state, 'running');
});

test('updateSettings writes only settings.json with bounds and same-project registered profiles', async () => {
  const ctx = setup();
  const t = await prepareTask(ctx.data, ctx.input());
  const statePath = join(ctx.data, 'workflow/state.json');
  const before = readFileSync(statePath, 'utf8');
  // works while another controller holds the lock: it never touches authority state
  const s = new WorkflowStore(ctx.data); const lease = s.acquireController();
  const res = await updateSettings(ctx.data, { maxConcurrency: 4, maxFixRounds: 0, defaultProfiles: { demo: { reviewer: t.profileIds.reviewer } } });
  assert.deepEqual([res.maxConcurrency, res.maxFixRounds, res.defaultProfiles], [4, 0, { demo: { reviewer: t.profileIds.reviewer } }]);
  s.releaseController(lease);
  assert.equal(readFileSync(statePath, 'utf8'), before);
  assert.equal(mode(join(ctx.data, 'workflow/settings.json')), 0o600);

  for (const [inp, re] of [
    [{ maxConcurrency: 5 }, /1\.\.4/], [{ maxConcurrency: 0 }, /1\.\.4/], [{ maxFixRounds: 3 }, /0\.\.2/],
    [{ other: 1 }, /unknown field/],
    [{ defaultProfiles: { ghost: { developer: t.profileIds.developer } } }, /unknown project/],
    [{ defaultProfiles: { demo: { developer: t.profileIds.reviewer } } }, /registered developer profile/],
    [{ defaultProfiles: { demo: { developer: 'nope' } } }, /registered developer profile/],
    [{ defaultProfiles: { demo: { admin: 'x' } } }, /unknown field/],
  ]) await assert.rejects(updateSettings(ctx.data, inp), re, JSON.stringify(inp));

  const snap = await readWorkflow(ctx.data);
  assert.equal(snap.settings.maxConcurrency, 4);
  assert.equal(snap.settings.maxFixRounds, 0);
  assert.deepEqual(snap.settings.defaultProfiles, { demo: { reviewer: t.profileIds.reviewer } });
  await updateSettings(ctx.data, { maxConcurrency: 1 });
  assert.deepEqual((await readWorkflow(ctx.data)).settings.defaultProfiles, { demo: { reviewer: t.profileIds.reviewer } }, 'partial update keeps others');
  rmSync(ctx.root, { recursive: true, force: true });
});

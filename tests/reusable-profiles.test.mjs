import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { once } from 'node:events';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const binary = join(root, 'bin', process.platform === 'win32' ? 'meerkat.exe' : 'meerkat');

test('generated reusable Pi Profile prepares two independent projects through the real CLI without a model call', {
  skip: !existsSync(binary) && 'build frontend assets and bin/meerkat first', timeout: 30000,
}, async () => {
  const base = realpathSync(mkdtempSync(join(tmpdir(), 'mk-shared-cli-')));
  const data = join(base, 'data');
  const marker = join(base, 'executor-was-started');
  const fakePi = join(base, 'pi-fixture');
  writeFileSync(fakePi, '#!/bin/sh\nprintf forbidden > "' + marker + '"\nexit 99\n', { mode: 0o700 });
  const secret = 'fixture-credential-value-never-published';
  const env = { PATH: process.env.PATH, HOME: base, FIXTURE_PROVIDER_KEY: secret,
    GIT_AUTHOR_NAME: 'Fixture', GIT_COMMITTER_NAME: 'Fixture', GIT_AUTHOR_EMAIL: 'fixture@example.test',
    GIT_COMMITTER_EMAIL: 'fixture@example.test', GIT_CONFIG_GLOBAL: process.platform === 'win32' ? 'NUL' : '/dev/null' };
  const invoke = (cmd, args, cwd = root) => {
    const r = spawnSync(cmd, args, { cwd, env, encoding: 'utf8', timeout: 10000 });
    assert.doesNotMatch(r.stdout + r.stderr, new RegExp(secret));
    return r;
  };
  const cli = (args, success = true) => {
    const r = invoke(binary, [...args, '--data-dir', data]);
    if (success) assert.equal(r.status, 0, r.stderr);
    assert.doesNotMatch(r.stdout + r.stderr, /FIXTURE_PROVIDER_KEY|pi-fixture|PI_CODING_AGENT_DIR/);
    return r;
  };
  let service;
  let closed;
  try {
    const configured = invoke(process.execPath, [join(root, 'scripts', 'configure.mjs'), '--profile-id', 'shared-pi',
      '--data-dir', data, '--provider', 'fixture', '--model', 'fixture-model', '--auth-env', 'FIXTURE_PROVIDER_KEY',
      '--base-url', 'https://provider.example.test/v1', '--api', 'openai-completions', '--pi-command', fakePi]);
    assert.equal(configured.status, 0, configured.stderr);
    const profile = join(data, 'profiles', 'shared-pi.json');
    const original = readFileSync(profile, 'utf8');
    assert.equal(Object.hasOwn(JSON.parse(original), 'projectId'), false);
    const choices = JSON.parse(cli(['profile', 'list']).stdout).data;
    assert.equal(choices.length, 1);
    assert.equal(choices[0].id, 'shared-pi');
    assert.equal(choices[0].binding, 'reusable');
    assert.equal(choices[0].valid, true);
    const doctor = JSON.parse(cli(['doctor', '--profile', 'shared-pi']).stdout).data;
    assert.ok(doctor.checks.some(check => check.id === 'executor.profile' && check.status === 'ok'));
    mkdirSync(join(data, 'inputs'), { mode: 0o700 });
    service = spawn(binary, ['serve', '--data-dir', data, '--port', '0'], { cwd: root, env, stdio: ['ignore', 'pipe', 'pipe'] });
    closed = once(service, 'close');
    let startup = '', errors = '';
    service.stderr.on('data', part => { errors += part; });
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('fixture service did not start: ' + errors)), 10000);
      service.once('error', err => { clearTimeout(timer); reject(err); });
      service.once('exit', code => { clearTimeout(timer); reject(new Error('fixture service exited: ' + code + ' ' + errors)); });
      service.stdout.on('data', part => {
        startup += part;
        if (startup.includes('\n')) { clearTimeout(timer); resolve(); }
      });
    });
    const receipts = [], dryRuns = [];
    for (const project of ['fixture-a', 'fixture-b']) {
      const repo = join(base, project), worktree = join(base, project + '-worktree');
      assert.equal(invoke('git', ['init', '-q', '-b', 'main', repo], base).status, 0);
      writeFileSync(join(repo, 'README.md'), project + '\n');
      assert.equal(invoke('git', ['add', 'README.md'], repo).status, 0);
      assert.equal(invoke('git', ['commit', '-qm', 'fixture baseline'], repo).status, 0);
      assert.equal(invoke('git', ['worktree', 'add', '-q', '-b', 'codex/shared-profile', worktree], repo).status, 0);
      const input = join(data, 'inputs', project + '.json');
      const taskInput = { project: { id: project, name: project }, repository: repo, worktree,
        title: 'Shared Profile fixture', goal: 'Verify preparation only', scope: ['README.md'], acceptance: ['No executor process'],
        changeId: project + '-change', context: { version: 1, text: 'PRIVATE-' + project },
        profiles: { developer: 'shared-pi' } };
      writeFileSync(input, JSON.stringify(taskInput), { mode: 0o600 });
      const dry = JSON.parse(cli(['run', '--input', input, '--dry-run']).stdout).data;
      dryRuns.push(dry);
      assert.equal(dry.projectId, project);
      assert.equal(dry.repository, repo);
      assert.equal(dry.worktree, worktree);
      assert.deepEqual(Object.keys(dry.profiles), ['developer']);
      assert.doesNotMatch(JSON.stringify(dry), /PRIVATE-/);
      // Full workflow preparation still freezes the roles it actually needs.
      taskInput.profiles = { developer: 'shared-pi', reviewer: 'shared-pi', polisher: 'shared-pi' };
      writeFileSync(input, JSON.stringify(taskInput), { mode: 0o600 });
      receipts.push(JSON.parse(cli(['prepare', '--input', input]).stdout).data);
      assert.equal(receipts.at(-1).projectId, project);
      assert.equal(receipts.at(-1).changeId, project + '-change');
    }
    assert.equal(dryRuns[0].profiles.developer.configDigest, dryRuns[1].profiles.developer.configDigest);
    assert.notEqual(receipts[0].contextRef.id, receipts[1].contextRef.id);
    assert.notEqual(receipts[0].profileIds.developer, receipts[1].profileIds.developer);
    const snapshot = JSON.parse(cli(['snapshot']).stdout).data;
    assert.deepEqual(snapshot.tasks.map(task => task.projectId).sort(), ['fixture-a', 'fixture-b']);
    assert.equal(snapshot.contexts.length, 2);
    assert.equal(snapshot.profiles.length, 6);
    assert.equal(snapshot.runs.length, 0);
    assert.doesNotMatch(JSON.stringify(snapshot), /PRIVATE-|authEnv|configFile|piCommand/);
    const missingInput = JSON.parse(readFileSync(join(data, 'inputs', 'fixture-b.json'), 'utf8'));
    missingInput.profiles.developer = 'missing-profile';
    const missing = join(data, 'inputs', 'missing-input.json');
    writeFileSync(missing, JSON.stringify(missingInput), { mode: 0o600 });
    const failed = cli(['run', '--input', missing, '--dry-run'], false);
    assert.notEqual(failed.status, 0);
    assert.match(failed.stderr, /config not found.*execution Profile/);
    assert.doesNotMatch(failed.stderr, /projectId does not match/);
    assert.equal(readFileSync(profile, 'utf8'), original);
    assert.equal(existsSync(marker), false, 'prepare/dry-run must never start Pi');
    assert.doesNotMatch(startup + errors, new RegExp(secret));
  } finally {
    if (service && service.exitCode === null) service.kill('SIGTERM');
    if (closed) await closed;
    rmSync(base, { recursive: true, force: true });
  }
});

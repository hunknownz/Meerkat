import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const SECRET = 'sk-test-value-that-must-never-appear-0123456789';
const base = ['--project-id', 'demo', '--provider', 'acme', '--model', 'acme-large-1', '--auth-env', 'ACME_API_KEY'];

function run(args, home) {
  const r = spawnSync(process.execPath, [join(root, 'scripts/configure.mjs'), ...args],
    { encoding: 'utf8', env: { PATH: process.env.PATH, HOME: home, ACME_API_KEY: SECRET } });
  assert.doesNotMatch(r.stdout + r.stderr, /sk-test-value/);
  return r;
}
const tmp = () => mkdtempSync(join(tmpdir(), 'mk-conf-'));

test('writes a strict private profile with references only (provider config mode)', () => {
  const home = tmp();
  const r = run(base, home);
  assert.equal(r.status, 0, r.stderr);
  const file = join(home, '.meerkat', 'profiles', 'demo.json');
  assert.ok(r.stdout.includes(file) && r.stdout.includes('ACME_API_KEY'));
  assert.equal(statSync(file).mode & 0o777, 0o600);
  assert.equal(statSync(dirname(file)).mode & 0o777, 0o700);
  assert.deepEqual(JSON.parse(readFileSync(file, 'utf8')), {
    projectId: 'demo', executor: 'pi', provider: 'acme', model: 'acme-large-1', authEnv: 'ACME_API_KEY',
    piCommand: ['pi', '--thinking', 'low'], instructions: [], limits: { maxTokens: 500000, maxWallSeconds: 600 },
  });
  assert.ok(!existsSync(join(home, '.meerkat', 'pi')), 'no custom model file without --base-url');
  rmSync(home, { recursive: true, force: true });
});

test('custom endpoint uses an isolated Pi agent dir with env interpolation, never the key', () => {
  const home = tmp();
  const data = join(home, 'data');
  const pi = join(home, 'pi-bin');
  writeFileSync(pi, '#!/bin/sh\n');
  chmodSync(pi, 0o755);
  const r = run([...base, '--data-dir', data, '--base-url', 'https://llm.example.test/v1', '--api', 'openai-completions', '--pi-command', pi], home);
  assert.equal(r.status, 0, r.stderr);
  const piDir = join(data, 'pi', 'demo');
  const models = join(piDir, 'models.json');
  assert.equal(statSync(models).mode & 0o777, 0o600);
  assert.equal(statSync(piDir).mode & 0o777, 0o700);
  assert.deepEqual(JSON.parse(readFileSync(models, 'utf8')), { providers: { acme: {
    baseUrl: 'https://llm.example.test/v1', api: 'openai-completions', apiKey: '${ACME_API_KEY}',
    models: [{ id: 'acme-large-1', input: ['text'], contextWindow: 200000, maxTokens: 16384 }],
  } } });
  const profile = JSON.parse(readFileSync(join(data, 'profiles', 'demo.json'), 'utf8'));
  assert.deepEqual(profile.piCommand, ['env', `PI_CODING_AGENT_DIR=${piDir}`, pi, '--thinking', 'low']);
  for (const f of [models, join(data, 'profiles', 'demo.json')]) assert.doesNotMatch(readFileSync(f, 'utf8'), /sk-test-value/);
  assert.ok(!existsSync(join(home, '.pi')), 'global Pi config untouched');
  rmSync(home, { recursive: true, force: true });
});

test('refuses to overwrite an existing profile', () => {
  const home = tmp();
  assert.equal(run(base, home).status, 0);
  const file = join(home, '.meerkat', 'profiles', 'demo.json');
  const before = readFileSync(file, 'utf8');
  const r = run([...base.slice(0, -1), 'OTHER_KEY'], home);
  assert.equal(r.status, 1);
  assert.match(r.stderr, /already exists/);
  assert.equal(readFileSync(file, 'utf8'), before);
  rmSync(home, { recursive: true, force: true });
});

test('isolated loopback gateway profiles store canonical endpoints and only auth references', () => {
  for (const endpoint of ['http://127.0.0.1:3425/v1', 'http://[::1]:3425/v1']) {
    const home = tmp();
    try {
      const r = run([...base, '--base-url', endpoint, '--api', 'openai-completions'], home);
      assert.equal(r.status, 0, r.stderr);
      const file = join(home, '.meerkat', 'pi', 'demo', 'models.json');
      const provider = JSON.parse(readFileSync(file, 'utf8')).providers.acme;
      assert.equal(provider.baseUrl, endpoint);
      assert.equal(provider.apiKey, '${ACME_API_KEY}');
      assert.equal(statSync(file).mode & 0o777, 0o600);
      assert.ok(!existsSync(join(home, '.pi')), 'global Pi configuration stays separate');
    } finally { rmSync(home, { recursive: true, force: true }); }
  }
});

test('refuses symlinked or shared directories', () => {
  const home = tmp();
  mkdirSync(join(home, 'real'), { mode: 0o700 });
  symlinkSync(join(home, 'real'), join(home, 'link'));
  let r = run([...base, '--data-dir', join(home, 'link')], home);
  assert.equal(r.status, 1);
  assert.match(r.stderr, /symlink/);
  mkdirSync(join(home, 'd', 'profiles'), { recursive: true });
  chmodSync(join(home, 'd', 'profiles'), 0o755);
  r = run([...base, '--data-dir', join(home, 'd')], home);
  assert.equal(r.status, 1);
  assert.match(r.stderr, /private/);
  assert.equal(statSync(join(home, 'd', 'profiles')).mode & 0o777, 0o755);
  rmSync(home, { recursive: true, force: true });
});

test('rejects keys, unsafe names, partial or unsafe endpoints and shell commands', () => {
  const home = tmp();
  const bad = [
    [['--project-id', 'Demo!', ...base.slice(2)], /project-id/],
    [[...base.slice(0, -1), SECRET], /auth-env/],
    [[...base.slice(0, -1), 'acme_api_key'], /auth-env/],
    [[...base, '--api-key', SECRET], /unknown/],
    [[...base.slice(0, 4), '--model', SECRET, ...base.slice(6)], /model/],
    [[...base, '--base-url', 'https://llm.example.test/v1'], /both/],
    [[...base, '--api', 'openai-completions'], /both/],
    [[...base, '--base-url', 'http://llm.example.test', '--api', 'openai-completions'], /HTTPS/],
    ...['http://192.168.1.20:3425/v1', 'http://0.0.0.0:3425/v1', 'http://localhost:3425/v1',
      'http://127.0.0.1.example.test:3425/v1', 'http://u:p@127.0.0.1:3425/v1',
      'http://127.0.0.1:3425/v1?', 'http://[::1]:3425/v1#'].map(endpoint =>
      [[...base, '--base-url', endpoint, '--api', 'openai-completions'], /HTTPS/]),
    [[...base, '--base-url', 'https://u:p@llm.example.test', '--api', 'openai-completions'], /HTTPS/],
    [[...base, '--base-url', 'https://llm.example.test/?key=x', '--api', 'openai-completions'], /HTTPS/],
    [[...base, '--base-url', 'https://llm.example.test/#x', '--api', 'openai-completions'], /HTTPS/],
    [[...base, '--base-url', 'https://llm.example.test', '--api', 'other'], /--api/],
    [[...base, '--base-url', 'https://llm.example.test', '--api', 'anthropic-messages'], /--api/],
    [[...base, '--pi-command', 'pi --yolo; rm -rf /'], /pi-command/],
    [[...base, '--pi-command', join(home, 'missing')], /pi-command/],
    [[...base, '--data-dir', 'relative'], /data-dir/],
  ];
  for (const [args, re] of bad) {
    const r = run(args, home);
    assert.equal(r.status, 1, args.join(' '));
    assert.match(r.stderr, re);
  }
  assert.ok(!existsSync(join(home, '.meerkat', 'profiles', 'demo.json')));
  rmSync(home, { recursive: true, force: true });
});

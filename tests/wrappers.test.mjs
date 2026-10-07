import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { chmodSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, existsSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { resolveBinary, withDataDir } from '../scripts/lib/go-cli.mjs';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const FAKE = join(root, 'tests/fixtures/fake-meerkat.mjs');

function run(script, args, extraEnv = {}, onSpawn) {
  const dir = mkdtempSync(join(tmpdir(), 'mk-wrap-'));
  const record = join(dir, 'rec.json');
  const env = { PATH: process.env.PATH, MEERKAT_BIN: FAKE, FAKE_RECORD: record, ...extraEnv };
  return new Promise((done) => {
    const child = spawn(process.execPath, [join(root, script), ...args], { env });
    let stdout = '', stderr = '';
    child.stdout.on('data', (d) => { stdout += d; });
    child.stderr.on('data', (d) => { stderr += d; });
    onSpawn?.(child, record);
    child.on('exit', (code) => {
      const rec = existsSync(record) ? JSON.parse(readFileSync(record, 'utf8')) : null;
      rmSync(dir, { recursive: true, force: true });
      done({ code, stdout, stderr, rec });
    });
  });
}

const cases = [
  ['scripts/flow.mjs', ['snapshot', '--data-dir', '/tmp/x y'], ['snapshot', '--data-dir', '/tmp/x y']],
  ['scripts/issues.mjs', ['read', '--url', 'https://e/x;rm -rf', '--output', 'o'], ['issue', 'read', '--url', 'https://e/x;rm -rf', '--output', 'o']],
  ['scripts/run.mjs', ['--input', '-', '--acknowledge'], ['run', '--input', '-', '--acknowledge']],
  ['dashboard/server.mjs', ['--port', '0'], ['serve', '--port', '0']],
];
for (const [script, args, expected] of cases) {
  test(`${script} forwards argv verbatim without a shell`, async () => {
    const r = await run(script, args);
    assert.equal(r.code, 0, r.stderr);
    assert.deepEqual(r.rec.argv, expected);
  });
}

test('child exit code is propagated', async () => {
  const r = await run('scripts/flow.mjs', ['execute'], { FAKE_EXIT: '7' });
  assert.equal(r.code, 7);
});

test('SIGTERM is forwarded to the Go child and its exit code mirrored', async () => {
  const r = await run('dashboard/server.mjs', [], { FAKE_WAIT: '1', FAKE_EXIT: '3' }, (child, record) => {
    const poll = setInterval(() => { if (existsSync(record)) { clearInterval(poll); child.kill('SIGTERM'); } }, 20);
  });
  assert.equal(r.code, 3);
  assert.equal(r.rec.signal, 'SIGTERM');
});

test('relative MEERKAT_BIN is rejected without spawning or echoing env', async () => {
  const r = await run('scripts/flow.mjs', ['version'], { MEERKAT_BIN: 'fake', SECRET_TOKEN: 'sk-should-not-leak' });
  assert.equal(r.code, 2);
  assert.equal(r.rec, null);
  assert.match(r.stderr, /absolute/);
  assert.doesNotMatch(r.stderr + r.stdout, /sk-should-not-leak/);
});

test('missing private binary prints the exact go build command', () => {
  const dir = mkdtempSync(join(tmpdir(), 'mk-root-'));
  const res = resolveBinary({}, dir);
  rmSync(dir, { recursive: true, force: true });
  assert.ok(res.error.includes(`go build -o bin/meerkat ./cmd/meerkat`));
  assert.ok(res.error.includes(dir));
});

test('legacy run flags fail with honest help and do not spawn', async () => {
  const r = await run('scripts/run.mjs', ['--config', 'c.json']);
  assert.equal(r.code, 2);
  assert.equal(r.rec, null);
  assert.match(r.stderr, /--config/);
  assert.match(r.stderr, /--input/);
});

test('launch.mjs forwards a read-only mcp command with MEERKAT_DATA_DIR and nothing else', async () => {
  const r = await run('scripts/launch.mjs', ['mcp'], { MEERKAT_DATA_DIR: '/tmp/mk data' });
  assert.equal(r.code, 0, r.stderr);
  assert.deepEqual(r.rec.argv, ['mcp', '--data-dir', '/tmp/mk data']);
  assert.equal(r.stdout, '');
  assert.equal(r.stderr, '');
});

test('launch.mjs keeps an explicit --data-dir and forwards SIGTERM', async () => {
  const r = await run('scripts/launch.mjs', ['serve', '--data-dir=/x'], { MEERKAT_DATA_DIR: '/y', FAKE_WAIT: '1', FAKE_EXIT: '4' }, (child, record) => {
    const poll = setInterval(() => { if (existsSync(record)) { clearInterval(poll); child.kill('SIGTERM'); } }, 20);
  });
  assert.equal(r.code, 4);
  assert.deepEqual(r.rec.argv, ['serve', '--data-dir=/x']);
  assert.equal(r.rec.signal, 'SIGTERM');
});

test('withDataDir inserts after the command, skips version/help, rejects relative dirs', () => {
  const env = { MEERKAT_DATA_DIR: '/d' };
  assert.deepEqual(withDataDir(['issue', 'read', '--url', 'u'], env), ['issue', 'read', '--data-dir', '/d', '--url', 'u']);
  assert.deepEqual(withDataDir(['version'], env), ['version']);
  assert.deepEqual(withDataDir(['snapshot', '--data-dir', '/e'], env), ['snapshot', '--data-dir', '/e']);
  assert.deepEqual(withDataDir(['snapshot'], {}), ['snapshot']);
  assert.deepEqual(withDataDir(['profile', 'list'], env), ['profile', 'list', '--data-dir', '/d']);
  assert.deepEqual(withDataDir(['control', 'receipt', '--request-id', 'id'], env), ['control', 'receipt', '--data-dir', '/d', '--request-id', 'id']);
  assert.throws(() => withDataDir(['snapshot'], { MEERKAT_DATA_DIR: 'rel' }), /absolute/);
});

function pluginRoot(version) {
  const dir = mkdtempSync(join(tmpdir(), 'mk-plugin-'));
  mkdirSync(join(dir, '.codex-plugin'));
  writeFileSync(join(dir, '.codex-plugin', 'plugin.json'), JSON.stringify({ name: 'meerkat', version }));
  return dir;
}

test('installed plugin root resolves the private runtime binary for the manifest version', () => {
  const plugin = pluginRoot('0.4.0-beta.1');
  const rt = mkdtempSync(join(tmpdir(), 'mk-rt-'));
  const target = { os: 'linux', arch: 'amd64' };
  const missing = resolveBinary({ MEERKAT_RUNTIME_DIR: rt }, plugin, target);
  assert.ok(missing.error.includes(`node ${JSON.stringify(join(plugin, 'scripts', 'setup.mjs'))}`), missing.error);
  const dest = join(rt, '0.4.0-beta.1', 'linux-amd64', 'meerkat');
  mkdirSync(dirname(dest), { recursive: true, mode: 0o700 });
  copyFileSync(FAKE, dest);
  chmodSync(dest, 0o755);
  assert.deepEqual(resolveBinary({ MEERKAT_RUNTIME_DIR: rt }, plugin, target), { path: dest });
  assert.match(resolveBinary({ MEERKAT_RUNTIME_DIR: 'rel' }, plugin, target).error, /absolute/);
  chmodSync(dest, 0o777);
  assert.match(resolveBinary({ MEERKAT_RUNTIME_DIR: rt }, plugin, target).error, /private regular file/);
  rmSync(dest);
  symlinkSync(FAKE, dest);
  assert.match(resolveBinary({ MEERKAT_RUNTIME_DIR: rt }, plugin, target).error, /private regular file/);
  assert.match(resolveBinary({ MEERKAT_RUNTIME_DIR: rt }, plugin, null).error, /unsupported platform/);
  rmSync(plugin, { recursive: true, force: true });
  rmSync(rt, { recursive: true, force: true });
});

test('a source checkout bin/meerkat wins over the installed runtime binary', () => {
  const plugin = pluginRoot('1.0.0');
  mkdirSync(join(plugin, 'bin'));
  copyFileSync(FAKE, join(plugin, 'bin', 'meerkat'));
  chmodSync(join(plugin, 'bin', 'meerkat'), 0o755);
  assert.deepEqual(resolveBinary({ MEERKAT_RUNTIME_DIR: '/nonexistent' }, plugin), { path: join(plugin, 'bin', 'meerkat') });
  rmSync(plugin, { recursive: true, force: true });
});

test('launch.mjs never downloads: a missing install prints the setup command on stderr only', async () => {
  const r = await run('scripts/launch.mjs', ['mcp'], { MEERKAT_BIN: '', MEERKAT_RUNTIME_DIR: mkdtempSync(join(tmpdir(), 'mk-empty-')) });
  if (existsSync(join(root, 'bin', 'meerkat'))) return; // a local source build takes precedence
  assert.equal(r.code, 2);
  assert.equal(r.stdout, '');
  assert.match(r.stderr, /scripts\/setup\.mjs/);
});

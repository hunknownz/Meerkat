import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtempSync, readFileSync, existsSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { resolveBinary } from '../scripts/lib/go-cli.mjs';

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

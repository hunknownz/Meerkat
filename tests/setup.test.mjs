import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { manifestVersion, selectTarget } from '../scripts/lib/go-cli.mjs';
import { allowedUrl, artifactName, install, parseSums } from '../scripts/setup.mjs';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const target = selectTarget();
const SRC = 'a'.repeat(40);
const sha = (b) => createHash('sha256').update(b).digest('hex');
const tmp = () => mkdtempSync(join(tmpdir(), 'mk-setup-'));
const fakeBin = (reports, tag = '') => `#!/usr/bin/env node\n// ${tag}\nprocess.stdout.write(${JSON.stringify(reports)} + '\\n');\n`;

// Write a release artifact set for version/target into dir; override pieces to make it hostile.
function artifacts(dir, version, { reports = version, tag = '', sums, release, bin } = {}) {
  mkdirSync(dir, { recursive: true });
  const name = artifactName(version, target);
  const body = bin ?? fakeBin(reports, tag);
  writeFileSync(join(dir, name), body);
  const hash = sha(body);
  writeFileSync(join(dir, 'SHA256SUMS'), sums ?? `${hash}  ${name}\n${'b'.repeat(64)}  meerkat_${version}_linux_arm64\n`);
  const meta = release ?? { name: 'meerkat', version, sourceSha: SRC, artifacts: [{ file: name, os: target.os, arch: target.arch, sha256: hash }] };
  writeFileSync(join(dir, 'release.json'), JSON.stringify(meta));
  return { name, hash };
}

const runSetup = (args, env = {}) => spawnSync(process.execPath, [join(root, 'scripts/setup.mjs'), ...args],
  { encoding: 'utf8', env: { PATH: process.env.PATH, HOME: env.HOME ?? tmpdir(), ...env } });

test('setup installs the verified manifest version from --artifact-dir and is idempotent', () => {
  const d = tmp();
  const version = manifestVersion(root);
  const { hash } = artifacts(join(d, 'a'), version);
  const runtime = join(d, 'rt');
  const r = runSetup(['--artifact-dir', join(d, 'a'), '--runtime-dir', runtime]);
  assert.equal(r.status, 0, r.stderr);
  const dest = join(runtime, version, `${target.os}-${target.arch}`, 'meerkat');
  assert.match(r.stdout, /installed/);
  assert.ok(r.stdout.includes(dest) && r.stdout.includes(hash) && r.stdout.includes(SRC));
  assert.equal(statSync(dest).mode & 0o777, 0o755);
  for (const dir of [runtime, dirname(dest), dirname(dirname(dest))]) assert.equal(statSync(dir).mode & 0o777, 0o700);
  const mtime = statSync(dest).mtimeMs;
  const again = runSetup(['--artifact-dir', join(d, 'a'), '--runtime-dir', runtime]);
  assert.equal(again.status, 0, again.stderr);
  assert.match(again.stdout, /already installed/);
  assert.equal(statSync(dest).mtimeMs, mtime);
  assert.ok(!existsSync(`${dest}.previous`));
  rmSync(d, { recursive: true, force: true });
});

test('prerelease versions install and a verified prior binary is kept as .previous', async () => {
  const d = tmp();
  const version = '0.4.0-beta.1';
  const runtime = join(d, 'rt');
  artifacts(join(d, 'a1'), version, { tag: 'one' });
  const first = await install({ version, target, runtime, artifactDir: join(d, 'a1') });
  assert.equal(first.changed, true);
  const old = readFileSync(first.path, 'utf8');
  artifacts(join(d, 'a2'), version, { tag: 'two' });
  const second = await install({ version, target, runtime, artifactDir: join(d, 'a2') });
  assert.equal(second.changed, true);
  assert.match(readFileSync(second.path, 'utf8'), /two/);
  assert.equal(readFileSync(`${second.path}.previous`, 'utf8'), old);
  rmSync(d, { recursive: true, force: true });
});

test('hash mismatch and wrong reported version leave the previous binary untouched', async () => {
  const d = tmp();
  const version = '1.2.3';
  const runtime = join(d, 'rt');
  artifacts(join(d, 'good'), version);
  const { path } = await install({ version, target, runtime, artifactDir: join(d, 'good') });
  const before = readFileSync(path, 'utf8');

  const bad = join(d, 'bad');
  const { name } = artifacts(bad, version);
  writeFileSync(join(bad, name), fakeBin(version, 'tampered'));
  await assert.rejects(install({ version, target, runtime, artifactDir: bad }), /hash mismatch/);

  artifacts(join(d, 'liar'), version, { reports: '9.9.9' });
  await assert.rejects(install({ version, target, runtime, artifactDir: join(d, 'liar') }), /does not report version/);
  assert.equal(readFileSync(path, 'utf8'), before);
  assert.ok(!existsSync(`${path}.previous`));
  assert.deepEqual(readdirSync(dirname(path)), ['meerkat']);
  rmSync(d, { recursive: true, force: true });
});

test('hostile metadata is rejected before anything is installed', async () => {
  const version = '1.0.0';
  const name = artifactName(version, target);
  const good = (h) => ({ name: 'meerkat', version, sourceSha: SRC, artifacts: [{ file: name, os: target.os, arch: target.arch, sha256: h }] });
  const body = fakeBin(version);
  const h = sha(body);
  const cases = [
    [{ sums: `${h}  ../${name}\n` }, /SHA256SUMS/],
    [{ sums: `${h}  ${name}\n${h}  ${name}\n` }, /SHA256SUMS/],
    [{ sums: `${'c'.repeat(64)}  ${name}\n` }, /disagree/],
    [{ release: { ...good(h), version: '1.0.1' } }, /name\/version/],
    [{ release: { ...good(h), name: 'other' } }, /name\/version/],
    [{ release: { ...good(h), sourceSha: 'main' } }, /sourceSha/],
    [{ release: { ...good(h), artifacts: [{ file: '../meerkat', os: target.os, arch: target.arch, sha256: h }] } }, /artifact entry/],
    [{ release: { ...good(h), artifacts: [{ file: name, os: 'windows', arch: target.arch, sha256: h }] } }, /artifact entry/],
    [{ release: { ...good(h), artifacts: [] } }, /artifacts/],
  ];
  for (const [over, re] of cases) {
    const d = tmp();
    artifacts(join(d, 'a'), version, { bin: body, ...over });
    await assert.rejects(install({ version, target, runtime: join(d, 'rt'), artifactDir: join(d, 'a') }), re);
    assert.ok(!existsSync(join(d, 'rt')), 'runtime must not be created');
    rmSync(d, { recursive: true, force: true });
  }
  assert.throws(() => parseSums('not a sums file'), /malformed/);
});

test('symlinked or non-file artifacts are rejected', async () => {
  const d = tmp();
  const version = '1.0.0';
  const { name } = artifacts(join(d, 'real'), version);
  const a = join(d, 'a');
  artifacts(a, version);
  rmSync(join(a, name));
  symlinkSync(join(d, 'real', name), join(a, name));
  await assert.rejects(install({ version, target, runtime: join(d, 'rt'), artifactDir: a }), /not a regular file/);
  rmSync(join(a, name));
  mkdirSync(join(a, name));
  await assert.rejects(install({ version, target, runtime: join(d, 'rt'), artifactDir: a }), /not a regular file/);
  rmSync(join(a, 'SHA256SUMS'));
  await assert.rejects(install({ version, target, runtime: join(d, 'rt'), artifactDir: a }), /missing artifact SHA256SUMS/);
  rmSync(d, { recursive: true, force: true });
});

test('unsafe runtime trees are refused without chmod', async () => {
  const d = tmp();
  const version = '1.0.0';
  artifacts(join(d, 'a'), version);
  const open = join(d, 'open');
  mkdirSync(open, { mode: 0o755 });
  chmodSync(open, 0o755);
  await assert.rejects(install({ version, target, runtime: open, artifactDir: join(d, 'a') }), /private directory/);
  assert.equal(statSync(open).mode & 0o777, 0o755);
  const rt = join(d, 'rt');
  mkdirSync(rt, { mode: 0o700 });
  mkdirSync(join(d, 'elsewhere'), { mode: 0o700 });
  symlinkSync(join(d, 'elsewhere'), join(rt, version));
  await assert.rejects(install({ version, target, runtime: rt, artifactDir: join(d, 'a') }), /private directory/);
  const dest = join(d, 'rt2', version, `${target.os}-${target.arch}`);
  mkdirSync(dest, { recursive: true, mode: 0o700 });
  symlinkSync(join(d, 'a', artifactName(version, target)), join(dest, 'meerkat'));
  await assert.rejects(install({ version, target, runtime: join(d, 'rt2'), artifactDir: join(d, 'a') }), /not a regular file/);
  rmSync(d, { recursive: true, force: true });
});

test('downloads use the official release URL and only GitHub HTTPS hosts', async () => {
  for (const ok of ['https://github.com/hunknownz/Meerkat/releases/download/v1/x', 'https://objects.githubusercontent.com/x', 'https://release-assets.github.com/x']) {
    assert.ok(allowedUrl(ok), ok);
  }
  for (const bad of ['http://github.com/x', 'https://github.com.evil.example/x', 'https://evilgithub.com/x', 'https://u:p@github.com/x',
    'https://github.com:8443/x', 'file:///etc/passwd', 'https://example.com/x']) {
    assert.equal(allowedUrl(bad), false, bad);
  }
  const d = tmp();
  const version = '2.0.0-rc.1';
  artifacts(join(d, 'a'), version);
  const urls = [];
  const fetcher = async (url, limit) => {
    urls.push([url, limit]);
    return readFileSync(join(d, 'a', url.split('/').pop()));
  };
  await install({ version, target, runtime: join(d, 'rt'), fetcher });
  const base = `https://github.com/hunknownz/Meerkat/releases/download/v${version}/`;
  assert.deepEqual(urls.map(([u]) => u), [`${base}SHA256SUMS`, `${base}release.json`, `${base}${artifactName(version, target)}`]);
  assert.deepEqual(urls.map(([, l]) => l), [1 << 20, 1 << 20, 120 << 20]);
  rmSync(d, { recursive: true, force: true });
});

test('platform selection maps supported hosts only', () => {
  assert.deepEqual(selectTarget('darwin', 'arm64'), { os: 'darwin', arch: 'arm64' });
  assert.deepEqual(selectTarget('linux', 'x64'), { os: 'linux', arch: 'amd64' });
  assert.equal(selectTarget('win32', 'x64'), null);
  assert.equal(selectTarget('linux', 'ia32'), null);
});

test('setup rejects unknown arguments such as a remote source URL', () => {
  const r = runSetup(['--url', 'https://example.com/meerkat']);
  assert.equal(r.status, 1);
  assert.match(r.stderr, /unknown/);
});

test('redirects to non-GitHub hosts and oversized downloads are refused', async () => {
  const d = tmp();
  const real = globalThis.fetch;
  try {
    globalThis.fetch = async (url, opts) => {
      assert.equal(opts.redirect, 'manual');
      return new Response(null, { status: 302, headers: { location: 'https://attacker.example/SHA256SUMS' } });
    };
    await assert.rejects(install({ version: '1.0.0', target, runtime: join(d, 'rt') }), /non-GitHub/);
    globalThis.fetch = async () => new Response('x'.repeat(10), { status: 200, headers: { 'content-length': String(2 << 20) } });
    await assert.rejects(install({ version: '1.0.0', target, runtime: join(d, 'rt') }), /size limit/);
    assert.ok(!existsSync(join(d, 'rt')));
  } finally {
    globalThis.fetch = real;
    rmSync(d, { recursive: true, force: true });
  }
});

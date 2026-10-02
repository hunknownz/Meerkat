import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const SCRIPT = join(dirname(fileURLToPath(import.meta.url)), '..', 'scripts', 'package.mjs');

function git(cwd, ...args) {
  const r = spawnSync('git', args, { cwd, encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
  return r.stdout.trim();
}

function put(root, rel, body) {
  mkdirSync(dirname(join(root, rel)), { recursive: true });
  writeFileSync(join(root, rel), body);
}

function fixture() {
  const root = realpathSync(mkdtempSync(join(tmpdir(), 'mk-pkg-')));
  git(root, 'init', '-q');
  git(root, 'config', 'user.email', 't@example.invalid');
  git(root, 'config', 'user.name', 't');
  put(root, '.codex-plugin/plugin.json', JSON.stringify({ name: 'meerkat', version: '0.3.0' }));
  put(root, '.gitignore', 'bin/\n.dist/\n.pi-developer/\nsecret.env\n');
  put(root, 'skills/x/SKILL.md', '# x\n');
  put(root, 'frontend/node_modules/leak/index.js', 'tracked by accident\n');
  git(root, 'add', '.');
  git(root, 'add', '-f', 'frontend/node_modules/leak/index.js');
  git(root, 'commit', '-qm', 'init');
  put(root, 'secret.env', 'TOKEN=redacted\n');
  put(root, '.pi-developer/history.jsonl', '{}\n');
  put(root, 'bin/meerkat', '#!/bin/sh\necho fake\n');
  return root;
}

const pack = (root) => spawnSync(process.execPath, [SCRIPT, '--skip-build', '--root', root], { encoding: 'utf8' });

test('stages tracked HEAD files plus binary, excluding ignored and forbidden paths', () => {
  const root = fixture();
  try {
    const r = pack(root);
    assert.equal(r.status, 0, r.stderr);
    const out = join(root, '.dist', 'meerkat');
    assert.ok(existsSync(join(out, 'skills/x/SKILL.md')));
    for (const rel of ['secret.env', '.pi-developer', 'frontend/node_modules', '.git']) {
      assert.ok(!existsSync(join(out, rel)), `${rel} must not be staged`);
    }
    const info = JSON.parse(readFileSync(join(out, 'build-info.json'), 'utf8'));
    assert.equal(info.version, '0.3.0');
    assert.equal(info.sourceSha, git(root, 'rev-parse', 'HEAD'));
    const bin = readFileSync(join(out, 'bin', 'meerkat'));
    assert.equal(info.binarySha256, createHash('sha256').update(bin).digest('hex'));

    // Re-packaging keeps the prior stage as a recoverable backup.
    assert.equal(pack(root).status, 0);
    assert.ok(existsSync(join(root, '.dist', 'meerkat.previous', 'build-info.json')));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('refuses a dirty tracked tree and leaves no stage behind', () => {
  const root = fixture();
  try {
    put(root, 'skills/x/SKILL.md', '# changed\n');
    const r = pack(root);
    assert.notEqual(r.status, 0);
    assert.match(r.stderr, /not clean/);
    assert.ok(!existsSync(join(root, '.dist')));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

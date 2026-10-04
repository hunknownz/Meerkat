import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { delimiter, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  PLATFORMS, artifactName, checksumsText, parseArgs, releaseMetadata,
} from '../scripts/build-release.mjs';

const REPO = join(dirname(fileURLToPath(import.meta.url)), '..');
const SCRIPT = join(REPO, 'scripts', 'build-release.mjs');
const VERSION = '0.4.0-beta.11';
const readJSON = (rel) => JSON.parse(readFileSync(join(REPO, rel), 'utf8'));

function git(cwd, ...args) {
  const r = spawnSync('git', args, { cwd, encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
  return r.stdout.trim();
}

function put(root, rel, body) {
  mkdirSync(dirname(join(root, rel)), { recursive: true });
  writeFileSync(join(root, rel), body);
}

// Temporary clean git fixture plus a fake `go` on PATH that records its environment and arguments.
function fixture() {
  const root = realpathSync(mkdtempSync(join(tmpdir(), 'mk-rel-')));
  git(root, 'init', '-q');
  git(root, 'config', 'user.email', 't@example.invalid');
  git(root, 'config', 'user.name', 't');
  put(root, '.codex-plugin/plugin.json', JSON.stringify({ name: 'meerkat', version: VERSION }));
  put(root, '.gitignore', '.dist/\n.fakebin/\nignored.embed\n');
  put(root, 'cmd/meerkat/main.go', 'package main\nfunc main() {}\n');
  git(root, 'add', '.');
  git(root, 'commit', '-qm', 'init');
  const fakeBin = join(root, '.fakebin');
  mkdirSync(fakeBin);
  const go = join(fakeBin, 'go');
  writeFileSync(go, [
    '#!/bin/sh',
    // Source cwd must be the committed snapshot only: no .git, untracked or ignored files.
    '[ -f cmd/meerkat/main.go ] || { echo "committed source missing" >&2; exit 3; }',
    'for f in .git .fakebin .dist cmd/meerkat/untracked.go ignored.embed; do',
    '  if [ -e "$f" ]; then echo "unexpected $f in source dir" >&2; exit 4; fi',
    'done',
    'out=""; prev=""',
    'for a in "$@"; do if [ "$prev" = "-o" ]; then out="$a"; fi; prev="$a"; done',
    'printf "%s %s %s %s\\n" "$GOOS" "$GOARCH" "$CGO_ENABLED" "$*" > "$out"',
    '',
  ].join('\n'));
  chmodSync(go, 0o755);
  return { root, env: { ...process.env, PATH: `${fakeBin}${delimiter}${process.env.PATH}` } };
}

const release = (root, env, ...args) => spawnSync(process.execPath, [SCRIPT, '--root', root, ...args], { encoding: 'utf8', env });

test('parseArgs defaults to all four platforms and accepts repeated --platform', () => {
  assert.deepEqual(parseArgs([], '/r').platforms.map((p) => `${p.os}/${p.arch}`), PLATFORMS);
  const o = parseArgs(['--platform', 'linux/amd64', '--platform', 'darwin/arm64', '--platform', 'linux/amd64'], '/r');
  assert.deepEqual(o.platforms, [{ os: 'linux', arch: 'amd64' }, { os: 'darwin', arch: 'arm64' }]);
  assert.throws(() => parseArgs(['--platform', 'windows/amd64']), /unsupported platform/);
  assert.throws(() => parseArgs(['--bogus']), /unknown argument/);
});

test('metadata helpers validate and sort', () => {
  const sha = 'a'.repeat(40);
  const arts = [
    { file: artifactName(VERSION, 'linux', 'amd64'), os: 'linux', arch: 'amd64', sha256: 'b'.repeat(64) },
    { file: artifactName(VERSION, 'darwin', 'arm64'), os: 'darwin', arch: 'arm64', sha256: 'c'.repeat(64) },
  ];
  assert.equal(arts[0].file, 'meerkat_0.4.0-beta.11_linux_amd64');
  assert.equal(checksumsText(arts), `${'c'.repeat(64)}  meerkat_0.4.0-beta.11_darwin_arm64\n${'b'.repeat(64)}  meerkat_0.4.0-beta.11_linux_amd64\n`);
  const meta = releaseMetadata(VERSION, sha, arts);
  assert.equal(meta.name, 'meerkat');
  assert.deepEqual(meta.artifacts.map((a) => a.os), ['darwin', 'linux']);
  assert.throws(() => releaseMetadata(VERSION, 'abc', arts), /sourceSha/);
  assert.throws(() => releaseMetadata('v1', sha, arts), /invalid version/);
  assert.throws(() => releaseMetadata(VERSION, sha, [{ ...arts[0], sha256: 'x' }]), /invalid sha256/);
});

test('builds the four-platform matrix from clean HEAD with checksums and source metadata', () => {
  const { root, env } = fixture();
  try {
    const r = release(root, env);
    assert.equal(r.status, 0, r.stderr);
    const out = join(root, '.dist', 'releases', VERSION);
    const files = PLATFORMS.map((p) => artifactName(VERSION, ...p.split('/'))).sort();
    assert.deepEqual(readdirSync(out).sort(), [...files, 'SHA256SUMS', 'release.json'].sort());
    const meta = JSON.parse(readFileSync(join(out, 'release.json'), 'utf8'));
    assert.equal(meta.name, 'meerkat');
    assert.equal(meta.version, VERSION);
    assert.equal(meta.sourceSha, git(root, 'rev-parse', 'HEAD'));
    assert.match(meta.sourceSha, /^[0-9a-f]{40}$/);
    assert.equal(meta.artifacts.length, 4);
    const sums = readFileSync(join(out, 'SHA256SUMS'), 'utf8');
    const lines = sums.trimEnd().split('\n');
    assert.deepEqual(lines.map((l) => l.split('  ')[1]), files);
    for (const a of meta.artifacts) {
      assert.match(a.sha256, /^[0-9a-f]{64}$/);
      const body = readFileSync(join(out, a.file));
      assert.equal(createHash('sha256').update(body).digest('hex'), a.sha256);
      assert.ok(lines.includes(`${a.sha256}  ${a.file}`));
      const rec = body.toString('utf8');
      assert.ok(rec.startsWith(`${a.os} ${a.arch} 0 build -trimpath -ldflags -s -w -o `), rec);
      assert.ok(rec.trimEnd().endsWith('./cmd/meerkat'));
    }
    assert.equal(readdirSync(join(root, '.dist', 'releases')).filter((n) => n.startsWith('.stage-')).length, 0);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('repeated --platform builds only the selected subset', () => {
  const { root, env } = fixture();
  try {
    const r = release(root, env, '--platform', 'linux/arm64');
    assert.equal(r.status, 0, r.stderr);
    const meta = JSON.parse(readFileSync(join(root, '.dist', 'releases', VERSION, 'release.json'), 'utf8'));
    assert.deepEqual(meta.artifacts.map((a) => a.file), ['meerkat_0.4.0-beta.11_linux_arm64']);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('builds from a git archive of HEAD, never untracked or ignored worktree files', () => {
  const { root, env } = fixture();
  try {
    put(root, 'cmd/meerkat/untracked.go', 'package main\nfunc init() { panic("untracked") }\n');
    put(root, 'ignored.embed', 'ignored secret asset');
    const sha = git(root, 'rev-parse', 'HEAD');
    const r = release(root, env, '--platform', 'linux/amd64');
    assert.equal(r.status, 0, r.stderr);
    const meta = JSON.parse(readFileSync(join(root, '.dist', 'releases', VERSION, 'release.json'), 'utf8'));
    assert.equal(meta.sourceSha, sha);
    assert.ok(existsSync(join(root, 'cmd/meerkat/untracked.go')), 'live worktree left untouched');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('refuses a dirty tracked tree', () => {
  const { root, env } = fixture();
  try {
    put(root, 'cmd/meerkat/main.go', 'package main\n// dirty\nfunc main() {}\n');
    const r = release(root, env);
    assert.notEqual(r.status, 0);
    assert.match(r.stderr, /not clean/);
    assert.equal(existsSync(join(root, '.dist', 'releases', VERSION)), false);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('repository metadata: marketplace, MCP launcher and versions are consistent', () => {
  const manifest = readJSON('.codex-plugin/plugin.json');
  assert.equal(manifest.version, VERSION);
  assert.equal(manifest.author.name, 'hunknownz');
  assert.equal(manifest.interface.developerName, 'hunknownz');
  assert.equal(manifest.homepage, 'https://github.com/hunknownz/Meerkat');
  assert.equal(manifest.repository, 'https://github.com/hunknownz/Meerkat');
  assert.equal(manifest.license, 'MIT');
  assert.equal(manifest.mcpServers, './.mcp.json');
  assert.equal(manifest.skills, './skills/');
  assert.equal(manifest.hooks, undefined);
  assert.equal(readJSON('frontend/package.json').version, VERSION);
  const lock = readJSON('frontend/package-lock.json');
  assert.equal(lock.version, VERSION);
  assert.equal(lock.packages[''].version, VERSION);
  assert.ok(readFileSync(join(REPO, 'frontend/src/mcp-app.tsx'), 'utf8').includes(`version: '${VERSION}'`));
  assert.ok(readFileSync(join(REPO, 'internal/server/service.go'), 'utf8').includes(`Version = "${VERSION}"`));

  const market = readJSON('.agents/plugins/marketplace.json');
  const entry = market.plugins.find((p) => p.name === 'meerkat');
  assert.deepEqual(entry.source, { source: 'local', path: './' });

  assert.deepEqual(readJSON('.mcp.json'), {
    mcpServers: {
      meerkat: {
        command: '/bin/sh',
        args: ['-c', 'exec "${CODEX_MCP_NODE_PATH:-node}" ./scripts/launch.mjs mcp', '--'],
        cwd: '.',
        env_vars: ['CODEX_MCP_NODE_PATH', 'MEERKAT_BIN', 'MEERKAT_RUNTIME_DIR', 'MEERKAT_DATA_DIR'],
      },
    },
  });
  assert.ok(existsSync(join(REPO, 'scripts', 'launch.mjs')));
});

test('MCP launcher uses the installed root and host Node without plugin-root environment variables', () => {
  const temp = realpathSync(mkdtempSync(join(tmpdir(), 'mk-mcp-launch-')));
  const root = join(temp, 'installed plugin with spaces');
  const unrelated = join(temp, 'unrelated project');
  const data = join(temp, 'private data');
  const config = readJSON('.mcp.json').mcpServers.meerkat;
  try {
    mkdirSync(unrelated);
    for (const rel of ['scripts/launch.mjs', 'scripts/lib/go-cli.mjs']) {
      put(root, rel, readFileSync(join(REPO, rel)));
    }
    const binary = join(root, 'bin', 'meerkat');
    put(root, 'bin/meerkat', '#!/bin/sh\nprintf "%s\\n" "$@"\n');
    chmodSync(binary, 0o700);
    // Codex resolves a relative MCP cwd against the installed plugin root.
    // Without cwd the previous launcher fell back to this unrelated project.
    assert.notEqual(resolve(root, config.cwd), unrelated);
    const available = { CODEX_MCP_NODE_PATH: process.execPath, MEERKAT_BIN: binary, MEERKAT_DATA_DIR: data };
    const env = { PATH: '/nonexistent', ...Object.fromEntries(config.env_vars.filter((key) => available[key]).map((key) => [key, available[key]])) };
    const r = spawnSync(config.command, config.args, { cwd: resolve(root, config.cwd), env, encoding: 'utf8' });
    assert.equal(r.status, 0, r.stderr);
    assert.deepEqual(r.stdout.trimEnd().split('\n'), ['mcp', '--data-dir', data]);
  } finally {
    rmSync(temp, { recursive: true, force: true });
  }
});

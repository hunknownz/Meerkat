#!/usr/bin/env node
// Build reproducible raw release binaries for meerkat from a clean tracked HEAD.
// Output: <root>/.dist/releases/<version>/{meerkat_<version>_<os>_<arch>, SHA256SUMS, release.json}.
// Standard Node builtins only, no shell. Does not rebuild the frontend: embedded assets come from HEAD.
// Compiles from `git archive <HEAD sha>` extracted into a fresh temporary directory, never the live
// worktree, so untracked or ignored files cannot influence the published binaries.
//
// Usage: node scripts/build-release.mjs [--root <repo>] [--platform <os>/<arch>]...
//   Platforms: darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 (default: all four).
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  chmodSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const PLATFORMS = Object.freeze(['darwin/arm64', 'darwin/amd64', 'linux/arm64', 'linux/amd64']);
export const VERSION_RE = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*)?$/;
const DEFAULT_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');

export function parseArgs(argv, defaultRoot = DEFAULT_ROOT) {
  const opts = { root: resolve(defaultRoot), platforms: [] };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--root' && argv[i + 1]) opts.root = resolve(argv[++i]);
    else if (a === '--platform' && argv[i + 1]) {
      const p = argv[++i];
      if (!PLATFORMS.includes(p)) throw new Error(`unsupported platform: ${p} (use ${PLATFORMS.join(', ')})`);
      if (!opts.platforms.includes(p)) opts.platforms.push(p);
    } else throw new Error(`unknown argument: ${a}`);
  }
  if (opts.platforms.length === 0) opts.platforms = [...PLATFORMS];
  opts.platforms = opts.platforms.map((p) => {
    const [os, arch] = p.split('/');
    return { os, arch };
  });
  return opts;
}

export function artifactName(version, os, arch) {
  return `meerkat_${version}_${os}_${arch}`;
}

export function checksumsText(artifacts) {
  return [...artifacts]
    .sort((a, b) => (a.file < b.file ? -1 : a.file > b.file ? 1 : 0))
    .map((a) => `${a.sha256}  ${a.file}\n`)
    .join('');
}

export function releaseMetadata(version, sourceSha, artifacts) {
  if (!VERSION_RE.test(version)) throw new Error(`invalid version: ${version}`);
  if (!/^[0-9a-f]{40}$/.test(sourceSha)) throw new Error('sourceSha must be 40 lowercase hex characters');
  const list = [...artifacts]
    .sort((a, b) => (a.file < b.file ? -1 : a.file > b.file ? 1 : 0))
    .map(({ file, os, arch, sha256 }) => {
      if (!/^[0-9a-f]{64}$/.test(sha256)) throw new Error(`invalid sha256 for ${file}`);
      return { file, os, arch, sha256 };
    });
  return { name: 'meerkat', version, sourceSha, artifacts: list };
}

function run(cmd, args, cwd, env) {
  const r = spawnSync(cmd, args, { cwd, env, shell: false, encoding: 'utf8', maxBuffer: 64 << 20 });
  if (r.error) throw new Error(`${cmd} could not start: ${r.error.message}`);
  if (r.status !== 0) throw new Error(`${cmd} ${args[0]} failed${r.stderr ? `: ${String(r.stderr).trim()}` : ''}`);
  return r.stdout;
}

const ARCHIVE_MAX_BYTES = 512 << 20;

// Export the exact committed tree of `sha` into a new empty temporary directory (no shell).
function exportSource(root, sha) {
  const arch = spawnSync('git', ['archive', '--format=tar', sha], { cwd: root, shell: false, maxBuffer: ARCHIVE_MAX_BYTES });
  if (arch.error) throw new Error(`git archive failed: ${arch.error.message}`);
  if (arch.status !== 0) throw new Error(`git archive failed${arch.stderr?.length ? `: ${String(arch.stderr).trim()}` : ''}`);
  const src = mkdtempSync(join(tmpdir(), 'meerkat-release-src-'));
  try {
    const x = spawnSync('tar', ['-x', '-f', '-', '-C', src], { input: arch.stdout, shell: false, encoding: 'utf8', maxBuffer: 16 << 20 });
    if (x.error) throw new Error(`tar could not start: ${x.error.message}`);
    if (x.status !== 0) throw new Error(`tar extract failed${x.stderr ? `: ${x.stderr.trim()}` : ''}`);
  } catch (err) {
    rmSync(src, { recursive: true, force: true });
    throw err;
  }
  return src;
}

function assertClean(root) {
  if (run('git', ['status', '--porcelain', '--untracked-files=no'], root).trim()) {
    throw new Error('tracked working tree is not clean; commit or stash changes first');
  }
}

function headSha(root) {
  return run('git', ['rev-parse', '--verify', 'HEAD^{commit}'], root).trim();
}

function assertSafeDir(path, label) {
  if (!existsSync(path)) return false;
  const st = lstatSync(path);
  if (st.isSymbolicLink() || !st.isDirectory()) throw new Error(`${label} exists but is not a plain directory: refusing`);
  return true;
}

export function buildRelease({ root, platforms }) {
  const top = run('git', ['rev-parse', '--show-toplevel'], root).trim();
  if (resolve(top) !== resolve(root)) throw new Error(`--root must be the repository top level (${top})`);
  assertClean(root);
  const sha = headSha(root);
  const manifest = JSON.parse(run('git', ['show', `${sha}:.codex-plugin/plugin.json`], root));
  const version = manifest.version;
  if (manifest.name !== 'meerkat' || typeof version !== 'string' || !VERSION_RE.test(version)) {
    throw new Error('HEAD .codex-plugin/plugin.json must name "meerkat" with a semver version');
  }

  const dist = join(root, '.dist');
  const releases = join(dist, 'releases');
  const target = join(releases, version);
  mkdirSync(releases, { recursive: true, mode: 0o700 });
  assertSafeDir(dist, '.dist');
  assertSafeDir(releases, '.dist/releases');
  const hadTarget = assertSafeDir(target, `.dist/releases/${version}`);

  const work = mkdtempSync(join(releases, '.stage-'));
  let src;
  try {
    src = exportSource(root, sha);
    const stage = join(work, version);
    mkdirSync(stage, { mode: 0o755 });
    const artifacts = [];
    for (const { os, arch } of platforms) {
      const file = artifactName(version, os, arch);
      const out = join(stage, file);
      const env = { ...process.env, GOOS: os, GOARCH: arch, CGO_ENABLED: '0' };
      run('go', ['build', '-trimpath', '-ldflags', '-s -w', '-o', out, './cmd/meerkat'], src, env);
      if (!existsSync(out) || !lstatSync(out).isFile()) throw new Error(`go build produced no regular file for ${os}/${arch}`);
      chmodSync(out, 0o755);
      artifacts.push({ file, os, arch, sha256: createHash('sha256').update(readFileSync(out)).digest('hex') });
    }
    // The build must reflect HEAD exactly: tracked files unchanged and HEAD not moved.
    assertClean(root);
    if (headSha(root) !== sha) throw new Error('HEAD moved during build');

    const meta = releaseMetadata(version, sha, artifacts);
    writeFileSync(join(stage, 'SHA256SUMS'), checksumsText(meta.artifacts));
    writeFileSync(join(stage, 'release.json'), `${JSON.stringify(meta, null, 2)}\n`);
    if (hadTarget) rmSync(target, { recursive: true, force: true });
    renameSync(stage, target);
    return { target, meta };
  } finally {
    rmSync(work, { recursive: true, force: true });
    if (src) rmSync(src, { recursive: true, force: true });
  }
}

function main() {
  try {
    const opts = parseArgs(process.argv.slice(2));
    const { target, meta } = buildRelease(opts);
    process.stdout.write(`build-release: ${meta.artifacts.length} artifact(s) in ${target} (meerkat ${meta.version} @ ${meta.sourceSha.slice(0, 12)})\n`);
  } catch (err) {
    process.stderr.write(`build-release: ${err.message}\n`);
    process.exit(1);
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();

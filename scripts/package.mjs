#!/usr/bin/env node
// Stage a clean local plugin package at <root>/.dist/meerkat from tracked HEAD files only,
// plus the built Go binary bin/meerkat and build-info.json. No shell; nothing from the archive is executed.
//
// Usage: node scripts/package.mjs [--skip-build] [--root <repo>]
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  chmodSync, copyFileSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readdirSync,
  readFileSync, renameSync, rmSync, writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const FORBIDDEN = ['.git', 'node_modules', '.pi-developer', '.dist'];

function fail(msg) {
  process.stderr.write(`package: ${msg}\n`);
  process.exit(1);
}

function run(cmd, args, cwd, opts = {}) {
  const r = spawnSync(cmd, args, { cwd, shell: false, encoding: opts.encoding ?? 'utf8', stdio: opts.stdio, maxBuffer: 64 << 20 });
  if (r.error) fail(`${cmd} could not start: ${r.error.message}`);
  if (r.status !== 0) fail(`${cmd} ${args[0]} failed${r.stderr ? `: ${String(r.stderr).trim()}` : ''}`);
  return r.stdout;
}

function parseArgs(argv) {
  const opts = { skipBuild: false, root: resolve(dirname(fileURLToPath(import.meta.url)), '..') };
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === '--skip-build') opts.skipBuild = true;
    else if (argv[i] === '--root' && argv[i + 1]) opts.root = resolve(argv[++i]);
    else fail(`unknown argument: ${argv[i]}`);
  }
  return opts;
}

function assertCleanTracked(root) {
  const out = run('git', ['status', '--porcelain', '--untracked-files=no'], root);
  if (out.trim()) fail('tracked working tree is not clean; commit or stash changes first');
}

function forbiddenPath(rel) {
  return rel.split('/').some((part) => FORBIDDEN.includes(part));
}

function walk(dir, base = '') {
  const found = [];
  for (const name of readdirSync(dir)) {
    const rel = base ? `${base}/${name}` : name;
    found.push(rel);
    const st = lstatSync(join(dir, name));
    if (st.isDirectory()) found.push(...walk(join(dir, name), rel));
  }
  return found;
}

function sha256(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex');
}

function assertSafeDir(path, label) {
  if (!existsSync(path)) return false;
  const st = lstatSync(path);
  if (st.isSymbolicLink() || !st.isDirectory()) fail(`${label} exists but is not a plain directory: refusing`);
  return true;
}

function main() {
  const { skipBuild, root } = parseArgs(process.argv.slice(2));
  const top = run('git', ['rev-parse', '--show-toplevel'], root).trim();
  if (resolve(top) !== root) fail(`--root must be the repository top level (${top})`);

  assertCleanTracked(root);
  const sha = run('git', ['rev-parse', '--verify', 'HEAD^{commit}'], root).trim();
  const manifest = JSON.parse(run('git', ['show', `${sha}:.codex-plugin/plugin.json`], root));
  const version = manifest.version;
  if (manifest.name !== 'meerkat' || typeof version !== 'string' || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*)?$/.test(version)) {
    fail('HEAD .codex-plugin/plugin.json must name "meerkat" with a semver version');
  }

  const bin = join(root, 'bin', 'meerkat');
  if (skipBuild) {
    if (!existsSync(bin)) fail('--skip-build requires an existing bin/meerkat');
  } else {
    // Assumes frontend dependencies are already installed (npm ci is not run here).
    run(process.execPath, [join(root, 'scripts', 'build.mjs')], root, { stdio: ['ignore', 'inherit', 'pipe'] });
  }
  if (!lstatSync(bin).isFile()) fail('bin/meerkat is not a regular file');
  // The build must reproduce HEAD exactly: rebuilt tracked assets may not differ, HEAD may not move.
  assertCleanTracked(root);
  if (run('git', ['rev-parse', '--verify', 'HEAD^{commit}'], root).trim() !== sha) fail('HEAD moved during build');

  const dist = join(root, '.dist');
  const target = join(dist, 'meerkat');
  const previous = join(dist, 'meerkat.previous');
  mkdirSync(dist, { recursive: true, mode: 0o700 });
  assertSafeDir(dist, '.dist');
  const hadTarget = assertSafeDir(target, '.dist/meerkat');
  assertSafeDir(previous, '.dist/meerkat.previous');

  const work = mkdtempSync(join(dist, '.stage-'));
  chmodSync(work, 0o700);
  const stage = join(work, 'meerkat');
  const tarFile = join(work, 'head.tar');
  let backup = null;
  try {
    mkdirSync(stage, { mode: 0o700 });
    const excludes = FORBIDDEN.flatMap((d) => [`:(exclude,glob)${d}/**`, `:(exclude,glob)**/${d}/**`]);
    run('git', ['archive', '--format=tar', '-o', tarFile, sha, '--', '.', ...excludes], root);
    run('tar', ['-xf', tarFile, '-C', stage], root);
    rmSync(tarFile);

    const bad = walk(stage).filter(forbiddenPath);
    if (bad.length) throw new Error(`forbidden paths in archive: ${bad.slice(0, 5).join(', ')}`);
    const staged = JSON.parse(readFileSync(join(stage, '.codex-plugin', 'plugin.json'), 'utf8'));
    if (staged.version !== version) throw new Error('staged manifest version mismatch');

    mkdirSync(join(stage, 'bin'), { recursive: true });
    copyFileSync(bin, join(stage, 'bin', 'meerkat'));
    chmodSync(join(stage, 'bin', 'meerkat'), 0o755);
    const info = { name: 'meerkat', version, sourceSha: sha, binary: 'bin/meerkat', binarySha256: sha256(join(stage, 'bin', 'meerkat')) };
    writeFileSync(join(stage, 'build-info.json'), `${JSON.stringify(info, null, 2)}\n`);

    if (hadTarget) {
      backup = join(work, 'old');
      renameSync(target, backup);
    }
    try {
      renameSync(stage, target);
    } catch (err) {
      if (backup) renameSync(backup, target);
      backup = null;
      throw err;
    }
    if (backup) {
      rmSync(previous, { recursive: true, force: true });
      renameSync(backup, previous);
    }
    rmSync(work, { recursive: true, force: true });
    process.stdout.write(`package: staged ${target} (meerkat ${version} @ ${sha.slice(0, 12)})\n`);
  } catch (err) {
    rmSync(work, { recursive: true, force: true });
    fail(err.message);
  }
}

main();

#!/usr/bin/env node
// Install the verified precompiled Meerkat binary for this host. No Go, npm, shell or archive extraction.
//
// Usage: node scripts/setup.mjs [--artifact-dir DIR] [--runtime-dir DIR]
// Downloads meerkat_<version>_<os>_<arch>, SHA256SUMS and release.json from the official GitHub release
// (or reads them from --artifact-dir), checks hash/platform/version/source, then installs atomically to
// <runtime-dir>/<version>/<os>-<arch>/meerkat (default runtime ~/.meerkat/runtime).
import { execFileSync } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { chmodSync, closeSync, copyFileSync, existsSync, lstatSync, mkdirSync, openSync, readFileSync, realpathSync, renameSync, rmSync, writeSync } from 'node:fs';
import { basename, dirname, isAbsolute, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { ensurePrivateDir, privatePath, protectNewFile } from './lib/private.mjs';
import { SOURCE_ROOT, VERSION_RE, installedPath, manifestVersion, runtimeDir, selectTarget } from './lib/go-cli.mjs';

export const MAX_BINARY = 120 * 1024 * 1024;
export const MAX_META = 1024 * 1024;
const RELEASE_BASE = 'https://github.com/hunknownz/Meerkat/releases/download';
const SAFE_NAME = /^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$/;
const HEX64 = /^[0-9a-f]{64}$/;

export const artifactName = (version, t) => `meerkat_${version}_${t.os}_${t.arch}${t.os === 'windows' ? '.exe' : ''}`;
const sha256 = (buf) => createHash('sha256').update(buf).digest('hex');
const safeName = (n) => typeof n === 'string' && SAFE_NAME.test(n) && !n.includes('..');

// Only HTTPS to GitHub and its release storage hosts, default port, no credentials.
export function allowedUrl(raw) {
  let u;
  try { u = new URL(raw); } catch { return false; }
  const h = u.hostname.toLowerCase();
  return u.protocol === 'https:' && u.port === '' && !u.username && !u.password &&
    (h === 'github.com' || h.endsWith('.github.com') || h.endsWith('.githubusercontent.com'));
}

async function fetchBounded(url, limit, timeoutMs) {
  const signal = AbortSignal.timeout(timeoutMs);
  for (let hop = 0; hop <= 5; hop++) {
    if (!allowedUrl(url)) throw new Error('refusing download from a non-GitHub HTTPS location');
    const res = await fetch(url, { redirect: 'manual', signal });
    if (res.status >= 300 && res.status < 400) {
      const loc = res.headers.get('location');
      await res.body?.cancel();
      if (!loc) throw new Error('redirect without location');
      url = new URL(loc, url).href;
      continue;
    }
    if (res.status !== 200) throw Object.assign(new Error(`download failed: HTTP ${res.status} for ${basename(new URL(url).pathname)}`), { status: res.status });
    if (Number(res.headers.get('content-length') ?? 0) > limit) throw new Error('download exceeds size limit');
    const chunks = [];
    let size = 0;
    for await (const chunk of res.body) {
      size += chunk.length;
      if (size > limit) throw new Error('download exceeds size limit');
      chunks.push(chunk);
    }
    return Buffer.concat(chunks);
  }
  throw new Error('too many redirects');
}

function readLocal(dir, name, limit) {
  const root = resolve(dir);
  const file = resolve(root, name);
  if (!safeName(name) || dirname(file) !== root) throw new Error('artifact path escapes --artifact-dir');
  let st;
  try { st = lstatSync(file); } catch { throw new Error(`missing artifact ${name}`); }
  if (st.isSymbolicLink() || !st.isFile()) throw new Error(`artifact ${name} is not a regular file`);
  if (st.size > limit) throw new Error(`artifact ${name} exceeds size limit`);
  const buf = readFileSync(file);
  if (buf.length > limit) throw new Error(`artifact ${name} exceeds size limit`);
  return buf;
}

export function parseSums(text) {
  const sums = new Map();
  for (const line of text.split('\n')) {
    if (line.trim() === '') continue;
    const m = /^([0-9a-f]{64}) [ *]([^\s]+)$/.exec(line.replace(/\r$/, ''));
    if (!m || !safeName(m[2]) || sums.has(m[2])) throw new Error('SHA256SUMS is malformed');
    sums.set(m[2], m[1]);
  }
  return sums;
}

// Validate release.json and return the selected artifact entry.
export function checkRelease(meta, version, target) {
  const want = artifactName(version, target);
  if (!meta || typeof meta !== 'object' || meta.name !== 'meerkat' || meta.version !== version) throw new Error('release.json name/version mismatch');
  if (typeof meta.sourceSha !== 'string' || !/^[0-9a-f]{40}$/.test(meta.sourceSha)) throw new Error('release.json sourceSha is invalid');
  if (!Array.isArray(meta.artifacts) || meta.artifacts.length === 0 || meta.artifacts.length > 64) throw new Error('release.json artifacts are invalid');
  for (const a of meta.artifacts) {
    if (!a || !safeName(a.file) || typeof a.sha256 !== 'string' || !HEX64.test(a.sha256) ||
      !selectTarget(a.os, a.arch === 'amd64' ? 'x64' : a.arch) || a.file !== artifactName(version, a)) {
      throw new Error('release.json artifact entry is invalid');
    }
  }
  const hits = meta.artifacts.filter((a) => a.file === want && a.os === target.os && a.arch === target.arch);
  if (hits.length !== 1) throw new Error(`release.json has no unique entry for ${want}`);
  return hits[0];
}

function hostVersion(file) {
  try {
    return execFileSync(file, ['version'], { encoding: 'utf8', timeout: 15000, stdio: ['ignore', 'pipe', 'ignore'], shell: false }).trim();
  } catch {
    return null;
  }
}

// Each existing runtime component must be a plain private directory owned by the current user.
export async function install({ version, target, runtime, artifactDir, fetcher = fetchBounded }) {
  if (!VERSION_RE.test(version)) throw new Error('invalid version');
  if (!target) throw new Error(`unsupported platform ${process.platform}/${process.arch}`);
  const name = artifactName(version, target);
  const base = `${RELEASE_BASE}/v${version}`;
  const get = (file, limit, ms) => (artifactDir ? readLocal(artifactDir, file, limit) : fetcher(`${base}/${file}`, limit, ms));
  const sums = parseSums((await get('SHA256SUMS', MAX_META, 30000)).toString('utf8'));
  let meta;
  try { meta = JSON.parse((await get('release.json', MAX_META, 30000)).toString('utf8')); } catch (err) {
    throw err instanceof SyntaxError ? new Error('release.json is not valid JSON') : err;
  }
  const entry = checkRelease(meta, version, target);
  if (sums.get(name) !== entry.sha256) throw new Error('SHA256SUMS and release.json disagree');
  const bin = await get(name, MAX_BINARY, 600000);
  const hash = sha256(bin);
  if (hash !== entry.sha256) throw new Error('binary hash mismatch; nothing installed');

  const dest = installedPath(runtime, version, target);
  for (const dir of [runtime, join(runtime, version), dirname(dest)]) ensurePrivateDir(dir);
  let existing = null;
  try { existing = lstatSync(dest); } catch {}
  if (existing && (existing.isSymbolicLink() || !existing.isFile())) throw new Error(`${dest} is not a regular file; refusing`);
  if (existing) privatePath(dest, { strict: false });
  const result = { path: dest, sha256: hash, sourceSha: meta.sourceSha, source: artifactDir ? resolve(artifactDir) : base };
  if (existing && sha256(readFileSync(dest)) === hash && hostVersion(dest) === version) return { ...result, changed: false };

  const tmp = join(dirname(dest), `.meerkat.tmp-${randomBytes(6).toString('hex')}${target.os === 'windows' ? '.exe' : ''}`);
  try {
    const fd = openSync(tmp, 'wx', 0o700);
    try { writeSync(fd, bin); } finally { closeSync(fd); }
    protectNewFile(tmp, 0o755);
    if (sha256(readFileSync(tmp)) !== hash) throw new Error('written binary hash mismatch');
    if (hostVersion(tmp) !== version) throw new Error(`binary does not report version ${version}; nothing replaced`);
    if (existing && hostVersion(dest) === version) {
      const prev = `${tmp}.previous`;
      copyFileSync(dest, prev);
      protectNewFile(prev, 0o755);
      renameSync(prev, `${dest}.previous`);
    }
    renameSync(tmp, dest);
  } finally {
    rmSync(tmp, { force: true });
    rmSync(`${tmp}.previous`, { force: true });
  }
  return { ...result, changed: true };
}

function parseArgs(argv) {
  const opts = {};
  for (let i = 0; i < argv.length; i++) {
    const key = { '--artifact-dir': 'artifactDir', '--runtime-dir': 'runtime' }[argv[i]];
    if (!key || !argv[i + 1] || opts[key]) throw new Error(`unknown or incomplete argument: ${argv[i]}`);
    opts[key] = argv[++i];
  }
  if (opts.runtime && !isAbsolute(opts.runtime)) opts.runtime = resolve(opts.runtime);
  return opts;
}

async function main() {
  try {
    const opts = parseArgs(process.argv.slice(2));
    const r = await install({
      version: manifestVersion(SOURCE_ROOT), target: selectTarget(),
      runtime: opts.runtime ?? runtimeDir(), artifactDir: opts.artifactDir,
    });
    process.stdout.write(`meerkat setup: ${r.changed ? 'installed' : 'already installed'} ${r.path}\n` +
      `sha256 ${r.sha256}\nsource ${r.source} (commit ${r.sourceSha})\n`);
  } catch (err) {
    process.stderr.write(`meerkat setup: ${err.message}\n`);
    process.exitCode = 1;
  }
}

if (process.argv[1] && existsSync(process.argv[1]) && realpathSync(process.argv[1]) === realpathSync(fileURLToPath(import.meta.url))) await main();

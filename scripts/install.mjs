#!/usr/bin/env node
// User-authorized bootstrap, never invoked as an installation hook. No task is
// prepared or executed here; scheduling and state remain in Go/SQLite.
import { spawn, spawnSync, execFileSync } from 'node:child_process';
import { existsSync, readFileSync, realpathSync, writeFileSync, openSync, closeSync, mkdirSync, rmSync } from 'node:fs';
import { homedir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { install } from './setup.mjs';
import { buildRelease } from './build-release.mjs';
import { SOURCE_ROOT, installedPath, manifestVersion, runtimeDir, selectTarget } from './lib/go-cli.mjs';
import { ensurePrivateDir, protectNewFile } from './lib/private.mjs';
import { installExecutor } from './lib/executor-install.mjs';

const run = (cmd, args, cwd = SOURCE_ROOT) => execFileSync(cmd, args, { cwd, shell: false, stdio: ['ignore','pipe','pipe'], encoding: 'utf8', timeout: 600000, maxBuffer: 32 << 20 });
export function codexCommand(explicit) {
  if (explicit) return explicit;
  if (process.env.MEERKAT_CODEX_COMMAND) return process.env.MEERKAT_CODEX_COMMAND;
  const candidates = ['codex'];
  if (process.platform === 'darwin') {
    for (const parent of ['/Applications', join(homedir(), 'Applications')]) {
      for (const app of ['Codex.app', 'ChatGPT.app']) candidates.push(join(parent, app, 'Contents', 'Resources', 'codex-cli', 'bin', 'codex'));
    }
  }
  for (const candidate of candidates) {
    const probe = spawnSync(candidate, ['plugin', '--help'], { shell: false, encoding: 'utf8', timeout: 15000 });
    if (!probe.error && probe.status === 0) return candidate;
  }
  return 'codex'; // Report the registration command if no compatible CLI was found.
}
export function options(argv) {
  const o = { root: SOURCE_ROOT, runtime: runtimeDir(), dataDir: process.env.MEERKAT_DATA_DIR ?? join(homedir(), '.meerkat'), host: true, executor: true, start: true, source: false };
  for (let i = 0; i < argv.length; i++) {
    const flag = { '--no-host': 'host', '--no-executor': 'executor', '--no-start': 'start', '--source': 'source' }[argv[i]];
    if (flag) { o[flag] = flag === 'source'; continue; }
    const key = { '--root': 'root', '--runtime-dir': 'runtime', '--data-dir': 'dataDir', '--artifact-dir': 'artifactDir', '--codex-command': 'codex' }[argv[i]];
    if (!key || !argv[i + 1]) throw new Error(`unknown or incomplete option: ${argv[i]}`);
    o[key] = argv[++i];
  }
  for (const k of ['root', 'runtime', 'dataDir']) o[k] = resolve(o[k]);
  o.root=realpathSync(o.root);
  return o;
}
export async function ensureRuntime(o) {
  const version = manifestVersion(o.root), target = selectTarget();
  if (!target) throw new Error('supported hosts: macOS/Linux/Windows, amd64 or arm64');
  let artifactDir = o.artifactDir;
  if (!o.source && !artifactDir) {
    try { return await install({ version, target, runtime: o.runtime }); }
    catch (e) { if (e.status !== 404) throw e; }
  }
  if (!artifactDir) {
    try { run('go', ['version'], o.root); } catch { throw new Error('Go 1.26+ is required while public binaries are unavailable. Follow INSTALL.md to install it, then rerun; existing history is preserved.'); }
    const release = buildRelease({ root: o.root, platforms: [target] });
    artifactDir = release.target;
  }
  return install({ version, target, runtime: o.runtime, artifactDir });
}
export async function ensureService(binary, dataDir, onSpawn) {
  ensurePrivateDir(dataDir);
  const snapshot = () => { try { return JSON.parse(run(binary, ['snapshot', '--data-dir', dataDir])); } catch { return null; } };
  let diagnostic; try { diagnostic=JSON.parse(run(binary,['doctor','--data-dir',dataDir])); } catch(e) {try{diagnostic=JSON.parse(e.stdout.toString());}catch{}}
  const current = snapshot();
  if (current?.ok) {
    // Do not stop, upgrade, or steal an existing controller, even when idle.
    if(diagnostic?.data?.serviceVersion!==manifestVersion(SOURCE_ROOT)) throw new Error('existing service version differs; perform the explicit backed-up update in docs/install.md first');
    return { state: 'reused', snapshot: true, version: diagnostic.data.serviceVersion };
  }
  const stamp = `${Date.now()}-${process.pid}`;
  const logPath = join(dataDir, `service-${stamp}.log`);
  const fd = openSync(logPath, 'wx', 0o600); protectNewFile(logPath);
  const child = spawn(binary, ['serve', '--data-dir', dataDir, '--port', '0'], { detached: true, windowsHide: true, stdio: ['ignore', fd, fd], shell: false });
  onSpawn?.(child);
  closeSync(fd);
  let spawnError = null; child.on('error', (e) => { spawnError = e; });
  for (let i = 0; i < 100; i++) {
    if (spawnError || child.exitCode !== null) throw new Error('service could not start; use doctor and inspect the private startup log');
    let ready;
    try { ready = JSON.parse(readFileSync(logPath, 'utf8')); } catch {}
    if (ready?.ok && snapshot()?.ok) {
      const record = { ...ready, pid: child.pid, startedAt: new Date().toISOString(), logPath };
      const recordPath = join(dataDir, 'installation-service.json');
      if (existsSync(recordPath)) { /* Replace installer metadata only, not SQLite or task records. */ rmSync(recordPath); }
      writeFileSync(recordPath, JSON.stringify(record, null, 2) + '\n', { mode: 0o600, flag: 'wx' }); protectNewFile(recordPath);
      child.unref(); return { state: 'started', version: ready.version, url: ready.url, snapshot: true };
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  child.unref();
  throw new Error('service startup unconfirmed; inspect the private startup log before retrying');
}
export function hostPackage(o, binary) {
  const sha = run('git', ['rev-parse', 'HEAD'], o.root).trim();
  const version = manifestVersion(o.root);
  const parent = join(o.dataDir, 'marketplaces'); ensurePrivateDir(parent);
  const target = join(parent, `${version}-${sha.slice(0, 12)}`);
  if (!existsSync(target)) {
    ensurePrivateDir(target);
    // Copy committed, public package files only. No worktree, ignored files or
    // credential sources enter the host package.
    const archive = join(target, 'source.tar');
    run('git', ['archive', '--format=tar', '-o', archive, sha], o.root);
    run('tar', ['-xf', archive, '-C', target], o.root); rmSync(archive);
  }
  const mcp = JSON.parse(readFileSync(join(target, '.mcp.json'), 'utf8'));
  const entry = mcp.mcpServers.meerkat;
  entry.command = process.execPath; entry.args = ['./scripts/launch.mjs', 'mcp'];
  entry.env = { MEERKAT_BIN: binary, MEERKAT_DATA_DIR: o.dataDir };
  writeFileSync(join(target, '.mcp.json'), JSON.stringify(mcp, null, 2) + '\n');
  return { path: target, sourceSha: sha, version };
}
export async function installAll(o) {
  const [major, minor] = process.versions.node.split('.').map(Number);
  if (major < 22 || major === 22 && minor < 19) throw new Error('Node 22.19+ required');
  const binary = await ensureRuntime(o);
  const result = { runtime: { state: 'installed', ...binary }, executor: { state: 'not_installed' }, service: { state: 'not_started' }, host: { state: 'not_registered' }, configuration: 'needs_provider_model_and_auth_environment', realTask: 'not_verified', nativePanel: 'not_verified' };
  if (o.executor) result.executor = { state: 'installed', ...installExecutor() };
  if (o.start) result.service = await ensureService(binary.path, o.dataDir, o.onServiceSpawn);
  if (o.host) {
    const pack = hostPackage(o, binary.path);
    const codex = codexCommand(o.codex);
    try {
      // A different existing marketplace is never removed automatically.
      const lists = JSON.parse(run(codex, ['plugin', 'marketplace', 'list', '--json']));
      const existing = (Array.isArray(lists) ? lists : lists.marketplaces ?? []).find((x) => x.name === 'meerkat');
      if (existing && JSON.stringify(existing).includes(pack.path) === false) throw new Error('Meerkat marketplace already uses a different source; follow the explicit update steps in docs/install.md');
      if (!existing) run(codex, ['plugin', 'marketplace', 'add', pack.path]);
      run(codex, ['plugin', 'add', 'meerkat@meerkat']);
      result.host = { state: 'registered', ...pack, reloadRequired: true };
    } catch (e) { result.host = { state: 'needs_registration', ...pack, command: [codex, 'plugin', 'marketplace', 'add', pack.path], error: e.message.includes('different source') ? e.message : 'Codex CLI unavailable or plugin registration failed; follow docs/install.md' }; }
  }
  return result;
}
if (process.argv[1] && existsSync(process.argv[1]) && realpathSync(process.argv[1]) === realpathSync(fileURLToPath(import.meta.url))) {
  try { process.stdout.write(JSON.stringify(await installAll(options(process.argv.slice(2))), null, 2) + '\n'); }
  catch (e) { process.stderr.write(`meerkat install: ${e.message}\n`); process.exitCode = 1; }
}

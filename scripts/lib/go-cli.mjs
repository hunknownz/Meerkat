// Thin launcher for the Go `meerkat` CLI. No scheduling, storage, or Pi logic lives in Node.
import { spawn } from 'node:child_process';
import { accessSync, constants, lstatSync, readFileSync, statSync } from 'node:fs';
import { homedir } from 'node:os';
import { dirname, isAbsolute, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const SOURCE_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
export const VERSION_RE = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$/;

const OS = { darwin: 'darwin', linux: 'linux' };
const ARCH = { arm64: 'arm64', x64: 'amd64' };

export function buildCommand(root = SOURCE_ROOT) {
  return `cd ${JSON.stringify(root)} && go build -o bin/meerkat ./cmd/meerkat`;
}

export function setupCommand(root = SOURCE_ROOT) {
  return `node ${JSON.stringify(join(root, 'scripts', 'setup.mjs'))}`;
}

// Map a Node platform/arch to the release target, or null when unsupported.
export function selectTarget(platform = process.platform, arch = process.arch) {
  const os = OS[platform];
  const a = ARCH[arch];
  return os && a ? { os, arch: a } : null;
}

// Read the plugin version from the manifest at runtime (never hardcoded).
export function manifestVersion(root = SOURCE_ROOT) {
  const m = JSON.parse(readFileSync(join(root, '.codex-plugin', 'plugin.json'), 'utf8'));
  if (m?.name !== 'meerkat' || typeof m.version !== 'string' || !VERSION_RE.test(m.version)) {
    throw new Error('.codex-plugin/plugin.json must name "meerkat" with a semver version');
  }
  return m.version;
}

export function runtimeDir(env = process.env) {
  const dir = env.MEERKAT_RUNTIME_DIR;
  if (dir !== undefined && dir !== '') {
    if (!isAbsolute(dir)) throw new Error('MEERKAT_RUNTIME_DIR must be an absolute path');
    return resolve(dir);
  }
  return join(homedir(), '.meerkat', 'runtime');
}

export function installedPath(runtime, version, target) {
  return join(runtime, version, `${target.os}-${target.arch}`, 'meerkat');
}

// Resolve the binary: absolute MEERKAT_BIN, then <root>/bin/meerkat, then the installed release binary.
export function resolveBinary(env = process.env, root = SOURCE_ROOT, target = selectTarget()) {
  const fromEnv = env.MEERKAT_BIN;
  if (fromEnv !== undefined && fromEnv !== '') {
    if (!isAbsolute(fromEnv)) return { error: 'MEERKAT_BIN must be an absolute path' };
    return checkExecutable(fromEnv, 'MEERKAT_BIN');
  }
  const local = join(root, 'bin', 'meerkat');
  if (exists(local)) return checkExecutable(local, 'bin/meerkat');
  const hint = `install the release binary with:\n  ${setupCommand(root)}\n(or, in a source checkout: ${buildCommand(root)})`;
  if (!target) return { error: `unsupported platform ${process.platform}/${process.arch}; ${hint}` };
  let installed;
  try {
    installed = installedPath(runtimeDir(env), manifestVersion(root), target);
  } catch (err) {
    return { error: `${err.message}; ${hint}` };
  }
  if (!exists(installed)) return { error: `meerkat binary not installed; ${hint}` };
  const st = lstatSync(installed);
  if (!st.isFile() || st.uid !== process.getuid() || (st.mode & 0o022) !== 0) {
    return { error: `installed binary ${installed} is not a private regular file; ${hint}` };
  }
  return checkExecutable(installed, 'installed binary', hint);
}

function exists(path) {
  try { lstatSync(path); return true; } catch { return false; }
}

function checkExecutable(path, label, hint) {
  try {
    if (!statSync(path).isFile()) return { error: `${label} is not a regular file` };
    accessSync(path, constants.X_OK);
    return { path };
  } catch {
    return { error: `${label} not found or not executable${hint ? `; ${hint}` : ''}` };
  }
}

// Insert --data-dir from MEERKAT_DATA_DIR after the command unless the args already supply it.
export function withDataDir(args, env = process.env) {
  const dir = env.MEERKAT_DATA_DIR;
  if (!dir || args.length === 0 || ['help', '-h', '--help', 'version', '--version', '-v'].includes(args[0])) return args;
  if (args.some((a) => a === '--data-dir' || a.startsWith('--data-dir='))) return args;
  if (!isAbsolute(dir)) throw new Error('MEERKAT_DATA_DIR must be an absolute path');
  const head = args[0] === 'issue' && args.length > 1 ? 2 : 1;
  return [...args.slice(0, head), '--data-dir', dir, ...args.slice(head)];
}

// Spawn the Go CLI with args (no shell), inherit stdio, forward SIGINT/SIGTERM, and
// mirror the child's exit status. Resolves with the exit code (never calls process.exit).
export function runGo(args, { env = process.env, root = SOURCE_ROOT, stderr = process.stderr, signals = process } = {}) {
  const bin = resolveBinary(env, root);
  if (bin.error) {
    stderr.write(`meerkat: ${bin.error}\n`);
    return Promise.resolve(2);
  }
  return new Promise((done) => {
    const child = spawn(bin.path, args, { stdio: 'inherit', shell: false, env });
    const forward = (sig) => { if (child.exitCode === null && child.signalCode === null) child.kill(sig); };
    const onInt = () => forward('SIGINT');
    const onTerm = () => forward('SIGTERM');
    signals.on('SIGINT', onInt);
    signals.on('SIGTERM', onTerm);
    const cleanup = () => { signals.off('SIGINT', onInt); signals.off('SIGTERM', onTerm); };
    child.on('error', (err) => {
      cleanup();
      stderr.write(`meerkat: failed to start ${bin.path}: ${err.code || 'error'}\n`);
      done(2);
    });
    child.on('exit', (code, signal) => {
      cleanup();
      if (signal) done(128 + ({ SIGINT: 2, SIGTERM: 15, SIGKILL: 9, SIGHUP: 1 }[signal] ?? 1));
      else done(code ?? 1);
    });
  });
}

export async function main(args) {
  process.exitCode = await runGo(args);
}

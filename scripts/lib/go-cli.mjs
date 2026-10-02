// Thin launcher for the Go `meerkat` CLI. No scheduling, storage, or Pi logic lives in Node.
import { spawn } from 'node:child_process';
import { accessSync, constants, statSync } from 'node:fs';
import { dirname, isAbsolute, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const SOURCE_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../..');

export function buildCommand(root = SOURCE_ROOT) {
  return `cd ${JSON.stringify(root)} && go build -o bin/meerkat ./cmd/meerkat`;
}

// Resolve the private binary: MEERKAT_BIN (absolute, executable file) or <root>/bin/meerkat.
export function resolveBinary(env = process.env, root = SOURCE_ROOT) {
  const fromEnv = env.MEERKAT_BIN;
  if (fromEnv !== undefined && fromEnv !== '') {
    if (!isAbsolute(fromEnv)) return { error: 'MEERKAT_BIN must be an absolute path' };
    return checkExecutable(fromEnv, 'MEERKAT_BIN');
  }
  return checkExecutable(join(root, 'bin', 'meerkat'), 'bin/meerkat', root);
}

function checkExecutable(path, label, root) {
  try {
    if (!statSync(path).isFile()) return { error: `${label} is not a regular file` };
    accessSync(path, constants.X_OK);
    return { path };
  } catch {
    const hint = root ? `; build it with:\n  ${buildCommand(root)}` : '';
    return { error: `${label} not found or not executable${hint}` };
  }
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

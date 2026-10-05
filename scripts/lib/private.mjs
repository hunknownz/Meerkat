// Bootstrap permissions only; the Go runtime independently checks every private path.
import { execFileSync } from 'node:child_process';
import { chmodSync, lstatSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
const aclScript = join(dirname(fileURLToPath(import.meta.url)), 'private.ps1');
export function windowsACL(path, action = 'check') {
  execFileSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', aclScript, '-Path', path, '-Action', action], { stdio: ['ignore', 'pipe', 'pipe'], shell: false, timeout: 30000 });
}
export function privatePath(path, { directory = false, strict = true } = {}) {
  const st = lstatSync(path);
  if (st.isSymbolicLink() || (directory ? !st.isDirectory() : !st.isFile())) throw new Error('private directory or file must be regular (no symlink)');
  if (process.platform === 'win32') windowsACL(path);
  else if (st.uid !== process.getuid() || (st.mode & (strict ? 0o077 : 0o022)) !== 0) throw new Error('private directory or file must be owned by the current user with private permissions');
  return st;
}
export function ensurePrivateDir(path) {
  try { privatePath(path, { directory: true }); }
  catch (e) {
    if (e.code !== 'ENOENT') throw e;
    // Create one component at a time; never harden a pre-existing shared path.
    const parent = dirname(path);
    try { lstatSync(parent); } catch (err) { if (err.code !== 'ENOENT') throw err; ensurePrivateDir(parent); }
    mkdirSync(path, { mode: 0o700 });
    if (process.platform === 'win32') windowsACL(path, 'protect'); else chmodSync(path, 0o700);
    privatePath(path, { directory: true });
  }
}
export function protectNewFile(path, mode = 0o600) {
  if (process.platform === 'win32') windowsACL(path, 'protect'); else chmodSync(path, mode);
}

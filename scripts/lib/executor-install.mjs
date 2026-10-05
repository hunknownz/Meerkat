import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { ensurePrivateDir } from './private.mjs';

export const PI_VERSION = '0.99.1';
export const executorRoot = (home = homedir()) => join(home, '.meerkat', 'executors', 'pi', PI_VERSION);
export const piCLI = (root = executorRoot()) => join(root, 'node_modules', '@earendil-works', 'pi-coding-agent', 'dist', 'bundle', 'cli.js');
export function npmCLI() {
  const base = dirname(process.execPath);
  const options = [join(base, 'node_modules/npm/bin/npm-cli.js'), resolve(base, '../lib/node_modules/npm/bin/npm-cli.js')];
  const found = options.find(existsSync);
  if (!found) throw new Error('npm CLI missing beside Node; use an official Node 22 installation');
  return found;
}
export function managedPiCommand() {
  if (!existsSync(piCLI())) return ['pi'];
  return [process.execPath, piCLI()];
}
export function installExecutor(root = executorRoot()) {
  ensurePrivateDir(root);
  const meta = join(root, 'node_modules', '@earendil-works', 'pi-coding-agent', 'package.json');
  let present = false;
  try { present = JSON.parse(readFileSync(meta, 'utf8')).version === PI_VERSION && existsSync(piCLI(root)); } catch {}
  if (!present) {
    try {
      execFileSync(process.execPath, [npmCLI(), 'install', '--prefix', root, '--no-audit', '--no-fund', '--save-exact', `@earendil-works/pi-coding-agent@${PI_VERSION}`], { shell: false, stdio: ['ignore', 'pipe', 'pipe'], timeout: 600000, maxBuffer: 8 << 20 });
    } catch { throw new Error('private Pi installation failed; check network/npm configuration (no model was called)'); }
  }
  const version = execFileSync(process.execPath, [piCLI(root), '--version'], { encoding: 'utf8', timeout: 15000, env: { ...process.env, PI_CODING_AGENT_DIR: join(root, 'probe') } }).trim();
  if (version !== PI_VERSION) throw new Error('installed Pi version does not match the pinned adapter');
  return { executor: 'pi', version, command: [process.execPath, piCLI(root)], changed: !present };
}

#!/usr/bin/env node
// Build the embedded React assets, then the private Go binary at bin/meerkat (no shell).
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';
import { SOURCE_ROOT } from './lib/go-cli.mjs';

const steps = [
  ['npm', ['--prefix', join(SOURCE_ROOT, 'frontend'), 'run', 'build']],
  ['go', ['build', '-o', join(SOURCE_ROOT, 'bin', 'meerkat'), './cmd/meerkat']],
];
const skipFrontend = process.argv.includes('--go-only');
for (const [cmd, args] of skipFrontend ? steps.slice(1) : steps) {
  const r = spawnSync(cmd, args, { cwd: SOURCE_ROOT, stdio: 'inherit', shell: false });
  if (r.status !== 0) {
    process.stderr.write(`build: ${cmd} failed\n`);
    process.exit(r.status ?? 1);
  }
}

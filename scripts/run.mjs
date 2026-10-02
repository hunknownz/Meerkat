#!/usr/bin/env node
// Forwards to `meerkat run <args>`; input is strict JSON via --input FILE|-.
import { main } from './lib/go-cli.mjs';

const args = process.argv.slice(2);
const legacy = args.find((a) => /^--(config|worktree|role|task|prompt)(=|$)/.test(a));
if (legacy) {
  process.stderr.write(
    `meerkat run: legacy flag ${legacy.split('=')[0]} is no longer supported.\n` +
    'The Node runner was replaced by the Go CLI. Use:\n' +
    '  node scripts/run.mjs --input FILE|- [--acknowledge]\n' +
    'where the input is strict JSON (see contracts/). Run `node scripts/flow.mjs help` for all commands.\n',
  );
  process.exitCode = 2;
} else {
  await main(['run', ...args]);
}

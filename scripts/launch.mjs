#!/usr/bin/env node
// Launch the Go CLI: `node scripts/launch.mjs <command> [args]` (e.g. `mcp`). Never downloads or builds;
// a missing binary yields the setup command on stderr. MEERKAT_DATA_DIR adds --data-dir when absent.
import { runGo, withDataDir } from './lib/go-cli.mjs';

let args;
try {
  args = withDataDir(process.argv.slice(2));
} catch (err) {
  process.stderr.write(`meerkat: ${err.message}\n`);
  process.exit(2);
}
process.exitCode = await runGo(args);

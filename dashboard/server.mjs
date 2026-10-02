#!/usr/bin/env node
// Forwards to `meerkat serve <args>`; the Go daemon serves the embedded React UI.
import { main } from '../scripts/lib/go-cli.mjs';

await main(['serve', ...process.argv.slice(2)]);

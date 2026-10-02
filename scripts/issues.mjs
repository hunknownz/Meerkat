#!/usr/bin/env node
// Forwards to `meerkat issue <args>` (read --url URL --output FILE | update --task ID [--apply]).
import { main } from './lib/go-cli.mjs';

await main(['issue', ...process.argv.slice(2)]);

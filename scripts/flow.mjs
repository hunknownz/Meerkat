#!/usr/bin/env node
// Forwards all arguments to the Go CLI: `meerkat <args>`.
import { main } from './lib/go-cli.mjs';

await main(process.argv.slice(2));

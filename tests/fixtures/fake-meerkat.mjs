#!/usr/bin/env node
// Fake Go CLI: records argv, optionally waits for a signal, exits with FAKE_EXIT.
import { writeFileSync } from 'node:fs';

const out = process.env.FAKE_RECORD;
const record = (extra) => writeFileSync(out, JSON.stringify({ argv: process.argv.slice(2), ...extra }));
if (process.env.FAKE_WAIT) {
  for (const sig of ['SIGINT', 'SIGTERM']) process.on(sig, () => { record({ signal: sig }); process.exit(Number(process.env.FAKE_EXIT ?? 0)); });
  record({ ready: true });
  setInterval(() => {}, 1000);
} else {
  record({});
  process.exit(Number(process.env.FAKE_EXIT ?? 0));
}

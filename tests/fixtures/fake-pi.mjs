// Fake Pi CLI for tests. Scenario comes from FAKE_PI_SCENARIO: ok | dirty | fail | overtokens | hold.
// FAKE_PI_ARGS_FILE records argv; `hold` waits for FAKE_PI_RELEASE to exist, then behaves like ok.
import { execFileSync } from 'node:child_process';
import { existsSync, writeFileSync } from 'node:fs';

if (process.argv.includes('--version')) { console.log('fake-pi 0.0.0'); process.exit(0); }
if (process.env.FAKE_PI_ARGS_FILE) writeFileSync(process.env.FAKE_PI_ARGS_FILE, JSON.stringify(process.argv.slice(2)));
const scenario = process.env.FAKE_PI_SCENARIO || 'ok';
if (scenario === 'hold') {
  await new Promise((done) => { const t = setInterval(() => { if (existsSync(process.env.FAKE_PI_RELEASE)) { clearInterval(t); done(); } }, 25); });
}
const emit = (ev) => process.stdout.write(JSON.stringify(ev) + '\n');
const usage = (i, o, cr, cw, cost) => ({ input: i, output: o, cacheRead: cr, cacheWrite: cw, cost: { total: cost } });

emit({ type: 'agent_start' });
emit({ type: 'message_end', message: { role: 'user', usage: usage(999, 999, 0, 0, 1) } });
emit({ type: 'message_update', message: { role: 'assistant', usage: usage(100, 5, 0, 0, 0) } });
emit({ type: 'message_end', message: { role: 'assistant', usage: usage(100, 10, 50, 5, 0.001) } });
if (scenario === 'overtokens') {
  emit({ type: 'message_update', message: { role: 'assistant', usage: usage(10_000_000, 0, 0, 0, 0) } });
  setInterval(() => {}, 1000); // hang until killed
} else {
  // Split one event across writes to exercise LF buffering.
  const line = JSON.stringify({ type: 'message_end', message: { role: 'assistant', usage: usage(200, 20, 0, 0, 0.002) } });
  process.stdout.write(line.slice(0, 10));
  setTimeout(() => {
    process.stdout.write(line.slice(10) + '\n');
    if (scenario !== 'fail') {
      writeFileSync('feature.txt', 'done\n');
      if (scenario === 'ok' || scenario === 'hold') {
        execFileSync('git', ['add', 'feature.txt']);
        execFileSync('git', ['-c', 'user.name=t', '-c', 'user.email=t@t', 'commit', '-qm', 'feat: fake']);
      }
    }
    emit({ type: 'agent_settled' });
    process.exit(scenario === 'fail' ? 1 : 0);
  }, 20);
}

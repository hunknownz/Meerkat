// Fake Pi CLI for tests. Scenario comes from FAKE_PI_SCENARIO: ok | dirty | modify | fail | overtokens | hold | hang.
// FAKE_PI_ARGS_FILE records argv; `hold` waits for FAKE_PI_RELEASE to exist, then behaves like ok.
// `hang` spawns a grandchild, writes both pids to FAKE_PI_PID_FILE and waits to be killed.
// Role report behaviour (FAKE_PI_REPORT): none | pass | changes | changed | no_change | stale | invalid | symlink.
// FAKE_PI_USAGE: ok | missing (assistant message lacks some counters) | none (no usage) | error (provider error).
import { execFileSync, spawn } from 'node:child_process';
import { existsSync, symlinkSync, writeFileSync } from 'node:fs';

if (process.argv.includes('--version')) { console.log('fake-pi 0.0.0'); process.exit(0); }
if (process.env.FAKE_PI_ARGS_FILE) writeFileSync(process.env.FAKE_PI_ARGS_FILE, JSON.stringify(process.argv.slice(2)));
const scenario = process.env.FAKE_PI_SCENARIO || 'ok';
if (scenario === 'hold') {
  await new Promise((done) => { const t = setInterval(() => { if (existsSync(process.env.FAKE_PI_RELEASE)) { clearInterval(t); done(); } }, 25); });
}
const emit = (ev) => process.stdout.write(JSON.stringify(ev) + '\n');
const usage = (i, o, cr, cw, cost) => ({ input: i, output: o, cacheRead: cr, cacheWrite: cw, cost: { total: cost } });
const prompt = process.argv[process.argv.length - 1];
const reportFile = /^Report file: (.+)$/m.exec(prompt)?.[1];
const ctxRaw = /^Context digest: (.+)$/m.exec(prompt)?.[1];
const ctx = process.env.FAKE_PI_CTX ?? (ctxRaw && !ctxRaw.startsWith('(') ? ctxRaw : null);
const reportMode = process.env.FAKE_PI_REPORT || 'none';
const usageMode = process.env.FAKE_PI_USAGE || 'ok';
const commit = (file, text) => {
  writeFileSync(file, text);
  execFileSync('git', ['add', file]);
  execFileSync('git', ['-c', 'user.name=t', '-c', 'user.email=t@t', 'commit', '-qm', `fake: ${file}`]);
};
const writeReport = () => {
  if (!reportFile || reportMode === 'none') return;
  const head = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
  const base = { candidateSha: head, contextDigest: ctx, summary: 'fake report', checks: [{ command: 'node --test', result: 'pass' }], knownGaps: [] };
  const body = {
    pass: { ...base, verdict: 'pass', findings: [] },
    changes: { ...base, verdict: 'changes_requested', findings: [{ id: 'F1', summary: 'fix it', path: 'AGENTS.md', line: 1 }] },
    changed: { ...base, decision: 'changed' },
    no_change: { ...base, decision: 'no_change' },
    stale: { ...base, candidateSha: '0'.repeat(40), verdict: 'pass', findings: [], decision: 'changed' },
    invalid: { ...base, verdict: 'maybe', decision: 'perhaps' },
  }[reportMode];
  if (reportMode === 'symlink') {
    writeFileSync(reportFile + '.real', JSON.stringify({ ...base, verdict: 'pass', findings: [] }));
    symlinkSync(reportFile + '.real', reportFile);
  } else writeFileSync(reportFile, JSON.stringify(body));
};

if (scenario === 'hang') {
  const kid = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore' });
  writeFileSync(process.env.FAKE_PI_PID_FILE, JSON.stringify({ pid: process.pid, grandchild: kid.pid }));
  emit({ type: 'agent_start' });
  emit({ type: 'tool_execution_start', toolName: 'bash', args: { command: 'echo SECRET-ARG' } });
  setInterval(() => {}, 1000);
} else if (['review', 'review-mutate', 'polish-nochange', 'polish-changed', 'dev-report'].includes(scenario)) {
  emit({ type: 'agent_start' });
  emit({ type: 'tool_execution_start', toolName: 'read', args: { path: '/etc/secret' } });
  if (scenario === 'review-mutate') commit('evil.txt', 'x\n');
  if (scenario === 'polish-changed' || scenario === 'dev-report') commit('feature.txt', 'polished\n');
  writeReport();
  const u = { ok: usage(100, 10, 0, 0, 0.001), missing: { input: 100, output: 'lots', cost: { total: 0.001 } }, none: undefined, error: usage(0, 0, 0, 0, 0) }[usageMode];
  const msg = { role: 'assistant', usage: u, ...(usageMode === 'error' ? { stopReason: 'error', errorMessage: 'HTTP 401 invalid key sk-secret-provider-xyz' } : { stopReason: 'stop' }) };
  emit({ type: 'message_end', message: msg });
  emit({ type: 'agent_settled' });
  process.exitCode = 0;
} else {
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
      if (scenario === 'modify') writeFileSync('AGENTS.md', 'changed\n');
      if (scenario === 'ok' || scenario === 'hold') {
        execFileSync('git', ['add', 'feature.txt']);
        execFileSync('git', ['-c', 'user.name=t', '-c', 'user.email=t@t', 'commit', '-qm', 'feat: fake']);
      }
    }
    emit({ type: 'agent_settled' });
    process.exit(scenario === 'fail' ? 1 : 0);
  }, 20);
}
}

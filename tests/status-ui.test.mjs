import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parseWorkflow, parseLegacy, startDashboard, POLL_MS, TIMEOUT_MS } from '../dashboard/public/app.js';
import {
  createMeerkatUI, esc, elapsedSeconds, formatDuration, formatLocalTime, safeHref,
  modelLabel, runUsage, runCost, summarizeUsage, taskCategory, liveRunSummary,
  phaseIndex, eventText, eventTime, pendingResume, itemText, agentLabel,
} from '../dashboard/public/ui.js';

const read = (p) => readFileSync(new URL(`../dashboard/public/${p}`, import.meta.url), 'utf8');
const UI = read('ui.js');
const APP = read('app.js');
const HTML = read('index.html');

test('parseWorkflow accepts the API shape and rejects malformed payloads', () => {
  const ok = parseWorkflow({ ok: true, data: { schemaVersion: 1, tasks: [], runs: [] }, legacyActive: [{ id: 'a', task: 't', model: 'm', worktree: '/w', startedAt: 's', x: 1 }], sessionToken: 'tok' });
  assert.equal(ok.sessionToken, 'tok');
  assert.deepEqual(ok.legacyActive[0], { id: 'a', task: 't', model: 'm', worktree: '/w', startedAt: 's' });
  for (const bad of [null, {}, { ok: false }, { ok: true, data: {}, sessionToken: 't' }, { ok: true, data: { schemaVersion: 2 }, sessionToken: 't' },
    { ok: true, data: { schemaVersion: 1, runs: {} }, sessionToken: 't' }, { ok: true, data: { schemaVersion: 1 } }, { ok: true, data: { schemaVersion: 1 }, sessionToken: 't', legacyActive: [7] }]) {
    assert.throws(() => parseWorkflow(bad), /malformed/);
  }
  assert.throws(() => parseLegacy({}), /malformed/);
  // optional link metadata survives, bounded; blanks and non-strings are dropped
  const [linked, bare] = parseLegacy([
    { id: 'b', task: 't', runId: ` ${'r'.repeat(100)} `, taskId: 'task-1', role: 'developer', extra: 'x' },
    { id: 'c', runId: '  ', taskId: 7, role: null },
  ]);
  assert.equal(linked.runId, 'r'.repeat(64));
  assert.equal(linked.taskId, 'task-1');
  assert.equal(linked.role, 'developer');
  assert.equal('extra' in linked, false);
  assert.deepEqual(Object.keys(bare).sort(), ['id', 'model', 'startedAt', 'task', 'worktree']);
  assert.equal(parseLegacy(Array.from({ length: 80 }, () => ({}))).length, 50);
  assert.equal(POLL_MS, 4000);
  assert.ok(TIMEOUT_MS < POLL_MS);
});

test('elapsed time uses real ISO timestamps and works across dates', () => {
  assert.equal(elapsedSeconds('2026-10-01T23:50:00Z', '2026-10-02T00:20:05Z'), 1805);
  assert.equal(elapsedSeconds('2026-10-01T23:50:00+08:00', null, Date.parse('2026-10-02T00:00:00+08:00')), 600);
  assert.equal(elapsedSeconds('bad', null), null);
  assert.equal(elapsedSeconds(undefined, null), null);
  assert.equal(formatDuration(5), '5 秒');
  assert.equal(formatDuration(185), '3 分 05 秒');
  assert.equal(formatDuration(3720), '1 小时 02 分');
  assert.equal(formatDuration(90000), '1 天 1 小时');
  assert.equal(formatDuration(null), '—');
  const now = Date.parse('2026-10-02T12:00:00');
  assert.match(formatLocalTime('2026-10-02T09:05:00', now), /^09:05$/);
  assert.match(formatLocalTime('2026-10-01T09:05:00', now), /^10-01 09:05$/);
  assert.match(formatLocalTime('2025-10-01T09:05:00', now), /^2025-10-01 09:05$/);
  assert.equal(formatLocalTime('nope', now), '—');
});

test('links and text are sanitized', () => {
  assert.equal(safeHref('https://github.com/o/r/issues/1'), 'https://github.com/o/r/issues/1');
  for (const bad of ['javascript:alert(1)', 'http://x.test', 'data:text/html,x', '/rel', 'https://u:p@x.test/', 7, null]) assert.equal(safeHref(bad), null);
  assert.equal(esc('<a href="x">\'&'), '&lt;a href=&quot;x&quot;&gt;&#39;&amp;');
  assert.equal(modelLabel({ provider: 'deepseek', model: 'v4' }), 'deepseek/v4');
  assert.equal(modelLabel('claude-opus'), 'claude-opus');
  assert.equal(modelLabel(null), '');
});

test('usage: unknown is never zero, fees only trusted USD, wall vs agent sum', () => {
  const runs = [
    { startedAt: '2026-10-01T23:00:00Z', endedAt: '2026-10-01T23:30:00Z', usage: { input: 100, output: 50, cacheRead: 10, cacheWrite: null, cost: { total: 0.5, currency: 'USD', source: 'provider' } } },
    { startedAt: '2026-10-01T23:10:00Z', endedAt: '2026-10-02T00:10:00Z', summary: { usage: { input: 20, output: null } }, cost: { total: 9, currency: 'USD', source: 'estimated' } },
    { startedAt: '2026-10-01T23:20:00Z', endedAt: '2026-10-01T23:25:00Z', usage: { cost: { total: 3, currency: 'CNY', source: 'provider' } } },
  ];
  assert.equal(runUsage(runs[2]), null);
  assert.deepEqual(runCost(runs[0]), { kind: 'reported', usd: 0.5 });
  assert.deepEqual(runCost(runs[1]), { kind: 'estimated', usd: 9 });
  assert.deepEqual(runCost(runs[2]), { kind: 'unknown', usd: null });
  assert.deepEqual(runCost({ usage: { cost: { total: 1, currency: 'USD' } } }), { kind: 'unknown', usd: null });
  const s = summarizeUsage(runs);
  assert.equal(s.reported, 2);
  assert.equal(s.full, 0);
  assert.equal(s.known.input, 120);
  assert.equal(s.missing.output, 2);
  assert.equal(s.missing.cacheWrite, 3);
  assert.equal(s.usd, 0.5);
  assert.equal(s.usdRuns, 1);
  assert.equal(s.estRuns, 1);
  assert.equal(s.unknownFee, 1);
  assert.equal(s.agentSeconds, (30 + 60 + 5) * 60);
  assert.equal(s.wallSeconds, 70 * 60);
});

test('usage: core nested run.usage shape is read; partial is never complete; null fee stays unknown', () => {
  const tokens = { input: 1200, output: 300, cacheRead: 50, cacheWrite: 0, total: 1550 };
  const complete = { startedAt: '2026-10-01T23:00:00Z', endedAt: '2026-10-01T23:10:00Z', usage: { tokens, usageCompleteness: 'complete', estimatedCostUsd: null } };
  const partial = { startedAt: '2026-10-01T23:00:00Z', endedAt: '2026-10-01T23:10:00Z', usage: { tokens: { ...tokens }, usageCompleteness: 'partial', estimatedCostUsd: null } };
  const unknown = { startedAt: '2026-10-01T23:00:00Z', usage: { tokens: { input: null, output: null, cacheRead: null, cacheWrite: null, total: null }, usageCompleteness: 'unknown', estimatedCostUsd: null } };
  assert.deepEqual(runUsage(complete), { input: 1200, output: 300, cacheRead: 50, cacheWrite: 0, completeness: 'complete' });
  assert.deepEqual(runUsage(partial), { input: 1200, output: 300, cacheRead: 50, cacheWrite: 0, completeness: 'partial' });
  assert.equal(runUsage(unknown), null);
  assert.equal(runUsage({ usage: { tokens: { input: 5, output: null }, usageCompleteness: 'complete' } }).completeness, 'partial');
  assert.equal(runUsage({ usage: { input: 1, output: 2, cacheRead: 3, cacheWrite: 4 } }).completeness, 'complete');
  assert.deepEqual(runCost(complete), { kind: 'unknown', usd: null });
  const s = summarizeUsage([complete, partial, unknown], Date.parse('2026-10-01T23:20:00Z'));
  assert.equal(s.reported, 2);
  assert.equal(s.full, 1);
  assert.equal(s.known.input, 2400);
  assert.equal(s.missing.input, 1);
  assert.equal(s.unknownFee, 3);
});

test('task phase follows the workflow states; resume is pending only after failure', () => {
  const want = { implementing: 1, first_delivery: 2, checking: 3, fixing: 3, final_candidate: 4, polishing: 5, rechecking: 5, delivered: 5 };
  for (const [state, p] of Object.entries(want)) assert.equal(phaseIndex({ state }, []), p, state);
  assert.equal(phaseIndex({ state: 'queued' }, []), 0);
  assert.equal(pendingResume({ state: 'implementing', resumeRole: 'developer' }), null);
  assert.equal(pendingResume({ state: 'checking', resumeRole: 'reviewer' }), null);
  for (const state of ['failed', 'stopped', 'unknown']) assert.equal(pendingResume({ state, resumeRole: 'developer' }), 'developer');
  assert.equal(pendingResume({ state: 'failed' }), null);
  assert.match(UI, /pendingResume\(t\)/);
  assert.doesNotMatch(UI, /t\.resumeRole \?/);
});

test('events: core {type, summary, observedAt} rendered bounded; legacy fields still work', () => {
  const e = { type: 'tool', summary: 'bash', observedAt: '2026-10-01T23:00:00Z', args: { command: 'secret-cmd' }, transcript: 'raw' };
  assert.equal(eventText(e), 'tool · bash');
  assert.equal(eventTime(e), '2026-10-01T23:00:00Z');
  assert.equal(eventText({ type: 'lifecycle', summary: 'x'.repeat(500) }).length, 'lifecycle · '.length + 200);
  assert.equal(eventText({ kind: 'state', message: 'running' }), 'state · running');
  assert.equal(eventTime({ at: 'a' }), 'a');
  assert.equal(eventTime({ time: 't' }), 't');
  assert.match(UI, /timeEl\(eventTime\(e\)\)/);
  assert.match(UI, /e\.summary \|\| e\.message/);
  // header concurrency limit applies to managed workflow runs only
  assert.match(UI, /工作流并发上限/);
  assert.doesNotMatch(UI, /· 并发上限/);
});

test('task categories: delivered means final code only', () => {
  assert.equal(taskCategory({ state: 'delivered' }), 'delivered');
  assert.equal(taskCategory({ state: 'final_candidate' }), 'active');
  assert.equal(taskCategory({ state: 'blocked' }), 'attention');
  assert.equal(taskCategory({ state: 'unknown' }), 'attention');
  assert.equal(taskCategory({}), 'active');
});

test('ui.js is a host-neutral factory scoped to its root', () => {
  assert.equal(typeof createMeerkatUI, 'function');
  assert.throws(() => createMeerkatUI(null), /root element required/);
  assert.match(UI, /export function createMeerkatUI\(root, options = \{\}\)/);
  for (const k of ['update(snapshot, legacyActive', 'setDisconnected(message)', 'destroy()']) assert.ok(UI.includes(k), k);
  // no global document/window queries or listeners, no network, no inline styles
  assert.doesNotMatch(UI, /document\.(querySelector|getElementById|addEventListener|documentElement|body)|window\.|\bfetch\(|XMLHttpRequest|style="/);
  assert.match(UI, /root\.addEventListener/);
  assert.match(UI, /removeEventListener/);
  assert.match(UI, /clearInterval\(ticker\)/);
  assert.match(UI, /root\.id = 'meerkat-ui'/);
  assert.match(UI, /root\.dataset\.theme = theme/);
});

test('ui.js carries no sample data, demo toggles or config/key inputs', () => {
  // no sample/demo constants or fixed clocks (explanatory copy may mention any word)
  assert.doesNotMatch(UI, /\b(?:const|let|var)\s+(?:SAMPLE|DEMO|MOCK|FAKE|FIXTURE|EXAMPLE)\w*\s*=/i);
  assert.doesNotMatch(UI, /\b(?:const|let|var)\s+NOW\s*=/);
  assert.doesNotMatch(UI, /data-(?:demo|sim)\b|sim-stop|示例数据/);
  // no hard-coded provider/model names or currency amounts inside string literals
  for (const m of UI.matchAll(/(['"`])((?:(?!\1)[^\\\n]|\\.)*)\1/g)) assert.doesNotMatch(m[2], /DeepSeek|Opus|¥\s*\d/i, m[0]);
  // form controls: only search + bounded selects; no credential/config/path inputs
  const controls = [...UI.matchAll(/<(input|textarea|select)\b[^>]*>/g)].map((m) => m[0]);
  assert.ok(controls.length >= 3);
  for (const c of controls) {
    assert.doesNotMatch(c, /^<textarea/, c);
    if (c.startsWith('<input')) assert.match(c, /\btype="search"/, c);
    const names = [...c.matchAll(/\b(?:id|name|data-setting|data-profile|data-ref)="([^"]*)"/g)].map((m) => m[1]).join(' ');
    assert.doesNotMatch(names, /key|token|secret|passw|credential|config|path|file/i, c);
  }
  const settings = [...UI.matchAll(/data-setting="([^"]*)"/g)].map((m) => m[1]).sort();
  assert.deepEqual(settings, ['maxConcurrency', 'maxFixRounds']);
  // stop acknowledgement must not claim the run stopped
  assert.match(UI, /不代表进程已停止/);
  assert.match(UI, /requestId: prev\?\.requestId \|\| uuid\(\)/);
  // stale snapshot is labelled and the run count becomes unknown
  assert.match(UI, /运行数未知/);
  assert.match(UI, /data-act="reconnect"/);
  // workflow runs and not-yet-linked independent Pi runs are labelled per category
  assert.match(UI, /<b>工作流运行<\/b>/);
  assert.match(UI, /<b>独立运行，尚未关联任务<\/b>/);
  assert.doesNotMatch(UI, /兼容|run\.mjs|单次 Pi/);
  // escape + validated hrefs for every link
  for (const m of UI.matchAll(/href="\$\{([^}]+)\}"/g)) assert.match(m[1], /^esc\(href\)$/);
  // accessibility: dialog semantics, Escape, tabs, focus return
  for (const k of ['role="dialog"', 'aria-modal="true"', "e.key === 'Escape'", 'role="tablist"', 'aria-selected', 'returnFocus', 'aria-expanded']) assert.ok(UI.includes(k), k);
});

test('standalone bootstrap only reads/writes the workflow API with the session token', () => {
  assert.match(APP, /import \{ createMeerkatUI \} from '\.\/ui\.js'/);
  assert.doesNotMatch(APP, /innerHTML|insertAdjacentHTML|DELETE|PATCH|setInterval/);
  const urls = [...APP.matchAll(/[`'](\/api\/[^`']*)[`']/g)].map((m) => m[1]);
  assert.deepEqual([...new Set(urls)].sort(), ['/api/workflow', '/api/workflow/runs/${encodeURIComponent(action.runId)}/stop', '/api/workflow/settings'].sort());
  assert.match(APP, /'X-Meerkat-Token': token/);
  assert.match(APP, /AbortController/);
  assert.match(APP, /if \(inFlight\) return inFlight/);
});

test('index.html mounts #meerkat-ui with a module script and no inline code', () => {
  assert.match(HTML, /<div id="meerkat-ui">/);
  assert.match(HTML, /<script type="module" src="\/app\.js"><\/script>/);
  assert.doesNotMatch(HTML, /<script>|<script(?![^>]*src=)[^>]*>|https?:\/\//);
  // inline styles / event handlers only count as real attributes inside a tag
  for (const tag of HTML.match(/<[a-z][^>]*>/gi) || []) {
    assert.doesNotMatch(tag, /\sstyle\s*=/i, tag);
    assert.doesNotMatch(tag, /\son[a-z]+\s*=/i, tag);
  }
  assert.match(HTML, /content=/); // attributes like content= must not trip the event check
  assert.match(HTML, /role="status"/);
});

test('app.css stays scoped to #meerkat-ui and keeps the MIT notice', () => {
  const css = read('app.css');
  const notice = read('THIRD_PARTY_NOTICES.md');
  assert.match(notice, /MIT License/);
  assert.match(notice, /Permission is hereby granted/);
  assert.match(css, /#meerkat-ui\[data-theme="dark"\]/);
  assert.match(css, /prefers-reduced-motion/);
  assert.match(css, /max-width: 420px/);
  assert.doesNotMatch(css, /@import|url\(\s*['"]?https?:/);
});

test('live counts: managed runs are never counted twice and unknown is never zero', () => {
  const snap = {
    runs: [{ id: 'AAA', state: 'starting' }, { id: 'bbb', state: 'stopping' }, { id: 'ccc', state: 'unknown' }, { id: 'ddd', state: 'succeeded' }],
    counts: { running: 1, queued: 0, unknown: 1 },
  };
  const legacy = [
    { id: '1', runId: 'aaa' }, { id: '2', runId: 'bbb' }, { id: '3', runId: 'ccc' }, { id: '4', runId: 'ddd' },
    { id: '5' }, { id: '6', runId: 'zzz' }, null,
  ];
  const s = liveRunSummary(snap, legacy);
  assert.deepEqual(s.list.map((a) => a.id), ['5', '6']);
  assert.deepEqual({ managed: s.managed, independent: s.independent, total: s.total, queued: s.queued, unknown: s.unknown },
    { managed: 1, independent: 2, total: 3, queued: 0, unknown: 1 });
  // one independent Pi with no managed runs is "1 running", not 0
  const solo = liveRunSummary({ runs: [], counts: { running: 0, queued: 0 } }, [{ id: 'x' }]);
  assert.equal(solo.total, 1);
  assert.equal(solo.unknown, 0);
  // malformed counts stay unknown (null), never a fake 0
  for (const counts of [undefined, null, [], { running: -1 }, { running: '2' }, { running: 1.5 }]) {
    const m = liveRunSummary({ runs: [], counts }, [{ id: 'x' }]);
    assert.equal(m.managed, null);
    assert.equal(m.total, null);
    assert.equal(m.independent, 1);
  }
  assert.equal(liveRunSummary({ counts: { running: 0, queued: 'x', unknown: -2 } }).queued, null);
  assert.equal(liveRunSummary({ counts: { running: 0, unknown: -2 } }).unknown, null);
  assert.equal(liveRunSummary(null).total, null);
  assert.equal(liveRunSummary({ runs: [], counts: { running: 0 } }, Array.from({ length: 80 }, (_, i) => ({ id: String(i) }))).independent, 50);
});

function fakeDashboard() {
  const calls = { update: 0, disconnected: 0, destroy: 0 };
  let onAction = null;
  const createUI = (_root, opts) => {
    onAction = opts.onAction;
    return { update() { calls.update += 1; }, setDisconnected() { calls.disconnected += 1; }, destroy() { calls.destroy += 1; } };
  };
  return { calls, createUI, act: (a) => onAction(a) };
}

function deferredFetch() {
  const pending = [];
  const fetch = (url, init) => new Promise((resolve, reject) => {
    pending.push({ url, resolve, reject });
    init?.signal?.addEventListener('abort', () => reject(Object.assign(new Error('aborted'), { name: 'AbortError' })));
  });
  return { pending, fetch };
}

const okBody = { ok: true, data: { schemaVersion: 1, runs: [] }, legacyActive: [], sessionToken: 't' };
const okResponse = { ok: true, status: 200, json: async () => okBody };
const tick = () => new Promise((r) => setImmediate(r));

test('startDashboard: destroy stops in-flight polls from updating or retrying', async (t) => {
  const real = globalThis.fetch;
  t.after(() => { globalThis.fetch = real; });
  const f = deferredFetch();
  globalThis.fetch = f.fetch;
  const d = fakeDashboard();
  const stop = startDashboard({}, { createUI: d.createUI });
  assert.equal(f.pending.length, 1);
  const first = f.pending[0];
  stop();
  stop(); // idempotent
  first.resolve(okResponse); // late response after teardown (or abort already rejected it)
  await tick(); await tick();
  assert.equal(d.calls.update, 0);
  assert.equal(d.calls.disconnected, 0);
  assert.equal(d.calls.destroy, 1);
  assert.equal(f.pending.length, 1, 'no retry after teardown');
  await assert.rejects(d.act({ type: 'reconnect' }), /已关闭/);
  assert.equal(f.pending.length, 1);
});

test('startDashboard: repeated manual reconnects share one request', async (t) => {
  const real = globalThis.fetch;
  t.after(() => { globalThis.fetch = real; });
  const f = deferredFetch();
  globalThis.fetch = f.fetch;
  const d = fakeDashboard();
  const stop = startDashboard({}, { createUI: d.createUI });
  f.pending[0].resolve({ ok: false, status: 500, json: async () => ({ ok: false, error: 'down' }) });
  await tick(); await tick();
  assert.equal(d.calls.disconnected, 1);
  const a = d.act({ type: 'reconnect' });
  const b = d.act({ type: 'reconnect' });
  assert.equal(f.pending.length, 2, 'second reconnect reuses the in-flight poll');
  f.pending[1].resolve(okResponse);
  await Promise.all([a, b]);
  assert.equal(d.calls.update, 1);
  stop();
  assert.equal(f.pending.length, 2, 'scheduled retry cleared on teardown');
});

test('itemText shows agent-reported command + result, distinct from controller checks', () => {
  const reported = itemText({ status: 'reported', command: 'npm test -- <x>', result: '12 passed' });
  assert.match(reported, /^Agent 上报（非调度器验证） · 命令：npm test -- <x> · 结果：12 passed$/);
  assert.doesNotMatch(reported, /reported/);
  assert.equal(esc(reported).includes('&lt;x&gt;'), true, 'escaped by caller');
  // controller check stays name · status with no agent label
  assert.equal(itemText({ name: 'tests', status: 'passed' }), 'tests · passed');
  // review check with command + result is readable; status kept when it differs
  assert.equal(itemText({ status: 'failed', command: 'node --test', result: '1 failing' }), '命令：node --test · failed · 结果：1 failing');
  assert.equal(itemText({ severity: 'high', message: 'bug' }), 'high · bug');
  // bounded
  const long = itemText({ status: 'reported', command: 'c'.repeat(1000), result: 'r'.repeat(1000) });
  assert.ok(long.length < 700);
  assert.equal(itemText(null), '');
});

test('agentLabel: historical Pi-NN slots render as Agent-NN keeping the original; custom IDs stay intact', () => {
  assert.deepEqual(agentLabel('Pi-01'), { label: 'Agent-01', legacy: 'Pi-01' });
  assert.deepEqual(agentLabel('Pi-12'), { label: 'Agent-12', legacy: 'Pi-12' });
  // new slot IDs and arbitrary/custom IDs are shown as stored
  for (const id of ['Agent-02', 'reviewer-alpha', 'pi-01', 'Pi-1', 'Pi-01x', 'XPi-01', 'Pi-']) assert.deepEqual(agentLabel(id), { label: id, legacy: null }, id);
  assert.deepEqual(agentLabel(undefined), { label: '', legacy: null });
  assert.equal(agentLabel('x'.repeat(100)).label.length, 40);
});

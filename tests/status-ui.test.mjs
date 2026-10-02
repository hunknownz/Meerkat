import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parseWorkflow, parseLegacy, POLL_MS, TIMEOUT_MS } from '../dashboard/public/app.js';
import {
  createMeerkatUI, esc, elapsedSeconds, formatDuration, formatLocalTime, safeHref,
  modelLabel, runUsage, runCost, summarizeUsage, taskCategory,
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
  assert.doesNotMatch(UI, /示例|data-demo|data-sim|sim-stop|DeepSeek|Opus|¥|NOW = '|apiKey|api_key|configPath|password/i);
  assert.doesNotMatch(UI, /type="(password|file|text)"/);
  // stop acknowledgement must not claim the run stopped
  assert.match(UI, /不代表进程已停止/);
  assert.match(UI, /requestId: prev\?\.requestId \|\| uuid\(\)/);
  // stale snapshot is labelled and the run count becomes unknown
  assert.match(UI, /运行数未知/);
  assert.match(UI, /data-act="reconnect"/);
  // legacy single-run executions live in their own compatibility section
  assert.match(UI, /兼容 · 单次 Pi 执行/);
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
  assert.doesNotMatch(HTML, /<script>|<script(?![^>]*src=)[^>]*>|style=|on[a-z]+="|https?:\/\//);
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

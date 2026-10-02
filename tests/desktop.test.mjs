import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createServer } from 'node:http';
import vm from 'node:vm';
import {
  parsePort, parseLoopbackUrl, parseCli, selectTarget, validateWsUrl, toShellState, toWorkflowState, shellExpression,
  mountSource, fetchState, createSession, ASSET_VERSION, MOUNT_CSS, RECONNECT_BINDING, UsageError,
} from '../desktop/injector.mjs';
import { createDom } from './fixtures/mini-dom.mjs';

const SHELL_SRC = readFileSync(new URL('../desktop/shell.js', import.meta.url), 'utf8');
const MOUNT_SRC = readFileSync(new URL('../internal/web/assets/mount/meerkat-ui.js', import.meta.url), 'utf8');
const RUN_ID = '3f2b8c1e-6a4d-4e2f-9b7a-1c2d3e4f5a6b';

// Minimal snapshot valid against contracts/workflow.schema.json (the React mount's contract).
const SNAPSHOT = {
  snapshotVersion: 1,
  observedAt: '2026-10-02T09:00:00Z',
  projects: [{ id: 'meerkat', name: 'Meerkat' }],
  tasks: [{ id: 'task-1', projectId: 'meerkat', title: 'Wire desktop overlay', state: 'developing' }],
  runs: [{ id: RUN_ID, taskId: 'task-1', agentId: 'Pi-01', role: 'developer', state: 'running', startedAt: '2026-10-02T08:55:00Z', events: [] }],
  deliveries: [], reviews: [], contexts: [], profiles: [],
  counts: { running: 1, queued: 0, unknown: 0 },
  controller: { state: 'running' },
};
const workflowState = (data = SNAPSHOT) => ({ kind: 'workflow', data, legacyActive: [], at: 1 });
// Values returned from node:vm carry the context's Object/Array prototypes; copy into this realm before deep comparisons.
const hostValue = (v) => JSON.parse(JSON.stringify(v));

test('parsePort accepts only 1-65535 digit strings', () => {
  assert.equal(parsePort('9222'), 9222);
  for (const bad of ['0', '65536', '-1', '92a', '', ' 9222', '1e3', '0x10', undefined]) {
    assert.throws(() => parsePort(bad), UsageError, String(bad));
  }
});

test('parseLoopbackUrl rejects remote, credentialed, portless, and wrong-protocol URLs', () => {
  assert.equal(parseLoopbackUrl('http://127.0.0.1:47824/', ['http:']).port, '47824');
  assert.equal(parseLoopbackUrl('http://[::1]:1/', ['http:']).hostname, '[::1]');
  for (const bad of [
    'http://example.com:80/', 'http://localhost:47824/', 'http://127.0.0.1.nip.io:80/', 'http://10.0.0.1:80/',
    'http://user:pw@127.0.0.1:80/', 'http://127.0.0.1/', 'https://127.0.0.1:443/', 'file:///etc/passwd', 'nope',
    'http://0.0.0.0:80/',
  ]) assert.throws(() => parseLoopbackUrl(bad, ['http:']), UsageError, bad);
});

test('parseCli requires --cdp-port, defaults status URL, and normalizes workflow/active URLs', () => {
  assert.throws(() => parseCli([]), /--cdp-port is required/);
  assert.throws(() => parseCli(['--cdp-port', '9222', '--bogus']), UsageError);
  const o = parseCli(['--cdp-port', '9222']);
  assert.equal(o.cdpPort, 9222);
  assert.equal(o.workflowUrl.href, 'http://127.0.0.1:47824/api/workflow');
  assert.equal(o.activeUrl.href, 'http://127.0.0.1:47824/api/active');
  const p = parseCli(['--cdp-port', '9222', '--status-url', 'http://127.0.0.1:5000/pi?x=1#y']);
  assert.equal(p.workflowUrl.href, 'http://127.0.0.1:5000/pi/api/workflow');
  assert.equal(p.activeUrl.href, 'http://127.0.0.1:5000/pi/api/active');
  assert.throws(() => parseCli(['--cdp-port', '9222', '--status-url', 'http://evil.test:80/']), UsageError);
});

test('selectTarget prefers exact main renderer, falls back to title Codex, excludes detached/dictation/overlay', () => {
  const ws = 'ws://127.0.0.1:9222/devtools/page/X';
  const list = [
    { type: 'page', url: 'app://-/detached-window.html?initialRoute=%2Fthread', title: 'Codex', webSocketDebuggerUrl: ws },
    { type: 'page', url: 'app://-/index.html?initialRoute=%2Favatar-overlay', title: 'Codex', webSocketDebuggerUrl: ws },
    { type: 'page', url: 'app://-/dictation.html', title: 'Codex', webSocketDebuggerUrl: ws },
    { type: 'page', url: 'app://-/overlay', title: 'Overlay', webSocketDebuggerUrl: ws },
    { type: 'service_worker', url: 'app://-/sw.js', webSocketDebuggerUrl: ws },
    { type: 'page', url: 'https://x.test/', title: 'Codex', webSocketDebuggerUrl: ws, id: 'title' },
    { type: 'page', url: 'app://-/index.html', title: 'ChatGPT', webSocketDebuggerUrl: ws, id: 'app' },
  ];
  assert.equal(selectTarget(list).id, 'app');
  assert.equal(selectTarget(list.slice(0, 6)).id, 'title');
  assert.equal(selectTarget(list.slice(0, 5)), null);
  assert.equal(selectTarget({}), null);
});

test('validateWsUrl requires loopback ws on the same port', () => {
  assert.equal(validateWsUrl('ws://127.0.0.1:9222/devtools/page/A', 9222).pathname, '/devtools/page/A');
  assert.throws(() => validateWsUrl('ws://127.0.0.1:9333/devtools/page/A', 9222), /does not match/);
  assert.throws(() => validateWsUrl('ws://evil.test:9222/x', 9222), UsageError);
  assert.throws(() => validateWsUrl('wss://127.0.0.1:9222/x', 9222), UsageError);
});

test('legacy toShellState validates /api/active into factory legacyActive and never turns failures into zero', () => {
  const s = toShellState({ ok: true, count: 1, agents: [{ id: 'a', task: 't', model: 'm', worktree: '/w', startedAt: 'x', extra: 1 }] }, 5);
  assert.deepEqual(s, { kind: 'legacy', count: 1, legacyActive: [{ id: 'a', task: 't', model: 'm', worktree: '/w', startedAt: 'x' }], at: 5 });
  assert.equal(toShellState({ ok: true, count: 0, agents: [] }).count, 0);
  for (const bad of [null, [], { ok: false, count: 0, agents: [] }, { ok: true, agents: [] }, { ok: true, count: -1, agents: [] },
    { ok: true, count: 0 }, { ok: true, count: 1, agents: [null] }]) {
    assert.throws(() => toShellState(bad), /malformed/);
  }
});

test('toWorkflowState validates the React contract, keeps the public snapshot exactly, and strips the session token', () => {
  const s = toWorkflowState({ ok: true, data: SNAPSHOT, legacyActive: [{ runId: 'p', task: 't' }], sessionToken: 'secret-token-value' }, 7);
  assert.deepEqual(Object.keys(s).sort(), ['at', 'data', 'kind', 'legacyActive']);
  assert.equal(s.kind, 'workflow');
  assert.deepEqual(s.data, SNAPSHOT);
  assert.notEqual(s.data, SNAPSHOT, 'copied, not aliased');
  assert.equal(s.legacyActive[0].runId, 'p');
  assert.doesNotMatch(JSON.stringify(s), /sessionToken|secret-token-value/);
  assert.doesNotMatch(shellExpression(s), /secret-token-value/);
  for (const bad of [
    { ok: true, data: { schemaVersion: 1 }, sessionToken: 't' },
    { ok: true, data: SNAPSHOT },
    { ok: false, data: SNAPSHOT, sessionToken: 't' },
    { ok: true, data: { ...SNAPSHOT, runs: {} }, sessionToken: 't' },
  ]) assert.throws(() => toWorkflowState(bad), /malformed/);
});

test('mountSource wraps the trusted React IIFE bundle into a read-only adapter loader', () => {
  const src = mountSource();
  assert.match(src, /^\(function meerkatMountLoader\(\) \{\n'use strict';\nvar MeerkatUI=\(function\(/);
  assert.ok(src.includes(MOUNT_SRC.trim()), 'bundle embedded verbatim');
  assert.match(src, /mount\(container, \{ snapshot: null \}, \{\s*readonly: true,/);
  assert.equal(typeof vm.runInNewContext(src), 'function');
  const fake = 'var MeerkatUI=(function(e){return e.mount=function(){},e})({});';
  assert.ok(mountSource(fake).includes(fake));
  assert.throws(() => mountSource('window.MeerkatUI={}'), /expected MeerkatUI IIFE/);
  assert.throws(() => mountSource(`import x from 'y';\n${fake}`), /expected MeerkatUI IIFE/);
  for (const evil of ['fetch("http://127.0.0.1:1/x")', 'import("./x.js")', 'eval("1")', 'new Function("x")', 'new WebSocket(u)', 'new EventSource(u)']) {
    assert.throws(() => mountSource(`var MeerkatUI=(function(e){${evil};return e})({});`), /module loading, eval, or network/, evil);
  }
});

test('shellExpression sends the mount loader only on demand and keeps the version in both forms', () => {
  const small = shellExpression({ kind: 'error', message: 'x' });
  const full = shellExpression(null, { assets: true });
  assert.ok(small.includes(JSON.stringify(ASSET_VERSION)) && full.includes(JSON.stringify(ASSET_VERSION)));
  assert.ok(!small.includes('meerkatMountLoader') && full.includes('meerkatMountLoader'));
  assert.ok(small.length < 20000, 'small form never carries the bundle');
  assert.ok(!full.includes('dashboard/public/ui.js') && !full.includes('createMeerkatUI'));
  assert.match(ASSET_VERSION, /^[0-9a-f]{16}$/);
  assert.ok(MOUNT_CSS.includes('#meerkat-ui'));
  assert.ok(SHELL_SRC.includes('VERSION-SENSITIVE'));
});

// ---- status loading (mocked fetch and a real loopback server) ----------------

function mockFetch(routes) {
  const calls = [];
  const fn = async (url, init) => {
    calls.push(String(url));
    assert.equal(init.redirect, 'error');
    const r = routes[new URL(url).pathname];
    if (!r) throw new TypeError('fetch failed');
    if (r instanceof Error) throw r;
    return new Response(typeof r.body === 'string' ? r.body : JSON.stringify(r.body), { status: r.status });
  };
  return { fn, calls };
}
const URLS = { workflowUrl: new URL('http://127.0.0.1:47824/api/workflow'), activeUrl: new URL('http://127.0.0.1:47824/api/active') };

test('fetchState: workflow snapshot, explicit 404 legacy fallback, and failures stay unknown (never empty)', async () => {
  const ok = mockFetch({ '/api/workflow': { status: 200, body: { ok: true, data: SNAPSHOT, legacyActive: [], sessionToken: 'tok-123456789' } } });
  const s = await fetchState(URLS, ok.fn, () => 9);
  assert.deepEqual(s, { kind: 'workflow', data: SNAPSHOT, legacyActive: [], at: 9 });
  assert.deepEqual(ok.calls, [URLS.workflowUrl.href]);

  const old = mockFetch({
    '/api/workflow': { status: 404, body: { ok: false, error: 'not found' } },
    '/api/active': { status: 200, body: { ok: true, count: 1, agents: [{ id: 'a', task: 'legacy task' }] } },
  });
  const l = await fetchState(URLS, old.fn, () => 9);
  assert.equal(l.kind, 'legacy');
  assert.equal(l.legacyActive[0].task, 'legacy task');

  for (const [name, routes, re] of [
    ['503', { '/api/workflow': { status: 503, body: { ok: false, error: 'workflow core not installed' } }, '/api/active': { status: 200, body: { ok: true, count: 0, agents: [] } } }, /HTTP 503：workflow core not installed/],
    ['500 non-JSON', { '/api/workflow': { status: 500, body: 'boom' } }, /HTTP 500/],
    ['malformed', { '/api/workflow': { status: 200, body: { ok: true, data: { snapshotVersion: 1, runs: {} }, sessionToken: 't' } } }, /无效数据/],
    ['old dashboard contract', { '/api/workflow': { status: 200, body: { ok: true, data: { schemaVersion: 1 }, sessionToken: 't' } } }, /无效数据/],
    ['not JSON', { '/api/workflow': { status: 200, body: '<html>' } }, /无效数据/],
    ['unreachable', {}, /无法连接/],
    ['old service, active broken', { '/api/workflow': { status: 404, body: {} }, '/api/active': { status: 200, body: { ok: true, count: 0 } } }, /无效数据/],
    ['old service, active 500', { '/api/workflow': { status: 404, body: {} }, '/api/active': { status: 500, body: {} } }, /HTTP 500/],
  ]) {
    const m = mockFetch(routes);
    const e = await fetchState(URLS, m.fn);
    assert.equal(e.kind, 'error', name);
    assert.match(e.message, re, name);
    assert.ok(!('data' in e) && !('legacyActive' in e), name);
    if (name === '503') assert.deepEqual(m.calls, [URLS.workflowUrl.href], '503 must not fall back to /api/active');
  }
});

test('fetchState against a real loopback server: exact snapshot without token; redirects are refused', async () => {
  let mode = 'ok';
  const server = createServer((req, res) => {
    if (mode === 'redirect') { res.writeHead(302, { location: 'http://127.0.0.1:1/api/workflow' }); res.end(); return; }
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ ok: true, data: SNAPSHOT, legacyActive: [], sessionToken: 'desktop-test-token-0123456789' }));
  });
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  try {
    const base = new URL(`http://127.0.0.1:${server.address().port}/`);
    const urls = { workflowUrl: new URL('api/workflow', base), activeUrl: new URL('api/active', base) };
    const s = await fetchState(urls);
    assert.equal(s.kind, 'workflow');
    assert.deepEqual(s.data, SNAPSHOT);
    assert.doesNotMatch(JSON.stringify(s), /desktop-test-token/);
    mode = 'redirect';
    const e = await fetchState(urls);
    assert.equal(e.kind, 'error');
    assert.match(e.message, /无法连接/);
  } finally {
    server.closeAllConnections();
    await new Promise((r) => server.close(r));
  }
});

test('createSession resends assets on demand, holds stale state after errors, and refetches only on reconnect', async () => {
  const sent = [];
  let reply = () => ({ ok: true });
  const states = [{ kind: 'error', message: 'down' }, workflowState()];
  let loads = 0;
  const session = createSession({
    evaluate: async (expr) => { sent.push(expr); return reply(expr); },
    loadState: async () => states[Math.min(loads++, states.length - 1)],
  });
  await session.tick();
  assert.ok(sent[0].includes('meerkatMountLoader'), 'first push carries assets');
  assert.equal(loads, 1);
  assert.equal(session.stale, true);
  await session.tick();
  assert.equal(loads, 1, 'no automatic refetch while stale');
  assert.ok(!sent[1].includes('meerkatMountLoader'));
  assert.match(sent[1], /\)\(null,/, 'stale tick only re-attaches');
  session.requestReconnect();
  await session.tick();
  assert.equal(loads, 2);
  assert.equal(session.stale, false);
  // Renderer reloaded: monitor gone → shell asks for assets → resent in the same tick.
  reply = (expr) => (expr.includes('meerkatMountLoader') ? { ok: true } : { ok: false, needAssets: true });
  const before = sent.length;
  const r = await session.tick();
  assert.equal(r.ok, true);
  assert.equal(sent.length - before, 2);
  assert.ok(sent.at(-1).includes('meerkatMountLoader'));
});

// ---- shell in an owned local DOM with a fake mount (no browser, no Codex) ----
// The real React bundle is exercised in tests/desktop-mount.test.mjs (jsdom).

const CODEX_DOM = `<nav id="app-shell-sidebar"><div data-app-action-sidebar-scroll>
  <button data-sidebar-destination="builtin:chats" aria-current="page"><svg></svg><span>Chats</span></button>
  <a href="/plugins" id="plugins" data-state="active" aria-selected="true"><span class="ic"><svg><path d="M0 0"/></svg></span><span>Plugins</span></a>
</div></nav><main data-app-shell-main-content-layout><p>native</p></main>`;
const ICON = 'data:image/svg+xml;base64,PHN2Zy8+';

// Fake adapter with the same contract as the injector's mount loader; renders plain text into its own ShadowRoot.
function fakeMount(d, log) {
  return () => (container, opts) => {
    const sr = container.attachShadow({ mode: 'open' });
    const ui = { container, opts, view: { snapshot: null, legacy: [], stale: null }, destroyed: false };
    const render = () => {
      const v = ui.view;
      const runs = v.snapshot ? v.snapshot.runs.length : '未知';
      sr.innerHTML = `<div id="meerkat-ui" data-theme="${opts.theme}"><p data-ref="sum">运行数 ${v.stale ? '未知' : runs}</p>`
        + `${v.stale ? `<p data-ref="stale">已断连 ${v.stale}</p>` : ''}`
        + `${(v.snapshot?.tasks || []).map((t) => `<p>${t.title}</p>`).join('')}<p>${opts.readonlyNote}</p></div>`;
    };
    log.push(ui);
    render();
    return {
      update(snapshot, legacyActive) { ui.view = { snapshot, legacy: legacyActive, stale: null }; render(); },
      setDisconnected(message) { ui.view = { ...ui.view, stale: message }; render(); },
      destroy() { ui.destroyed = true; sr.innerHTML = ''; },
    };
  };
}

// Every mounted DOM registers teardown first, so a failed assertion never leaves the shell's
// observer debounce timer or listeners alive (tests must exit without --test-force-exit).
function teardown(t, d) {
  t.after(() => {
    try { d.run(shellExpression('remove')); } catch { /* best effort */ }
    try { d.window.__meerkat?.remove?.(); } catch { /* best effort */ }
    for (const o of d.observers) o.disconnect();
  });
}

function withShell(d) {
  const shell = d.run(`(${SHELL_SRC.trim()})`);
  const mounts = [];
  const load = fakeMount(d, mounts);
  const eval_ = (state, { assets = false } = {}) => shell(state, ICON, assets ? { version: ASSET_VERSION, load } : { version: ASSET_VERSION });
  return { mounts, eval: eval_ };
}

function codex(t) {
  const d = createDom();
  teardown(t, d);
  d.document.body.innerHTML = CODEX_DOM;
  const docListeners = () => d.document.listeners.length;
  const activeObservers = () => d.observers.filter((o) => o.active).length;
  const $ = (s) => d.document.querySelector(s);
  const view = () => $('[data-meerkat-view]');
  const mountEl = () => view()?.shadowRoot?.querySelector('[data-meerkat-mount]');
  const ui = () => mountEl()?.shadowRoot?.querySelector('#meerkat-ui');
  return { ...d, ...withShell(d), $, view, mountEl, ui, docListeners, activeObservers };
}

test('real shell expression is a single function call; missing selectors reported; idempotent per version', (t) => {
  const removeExpr = shellExpression('remove');
  assert.ok(removeExpr.startsWith(`(${SHELL_SRC.trim()})("remove",`));
  assert.equal(typeof vm.runInNewContext(`(${SHELL_SRC.trim()})`), 'function', 'shell.js is a single function expression');
  const d = createDom();
  teardown(t, d);
  const s = withShell(d);
  const r = s.eval({ kind: 'error', message: 'x' }, { assets: true });
  assert.equal(r.ok, false);
  assert.deepEqual([...r.missing], ['[data-app-action-sidebar-scroll]', '[data-app-shell-main-content-layout]']);
  const monitor = d.window.__meerkat;
  assert.equal(monitor.version, ASSET_VERSION);
  s.eval(null);
  assert.equal(d.window.__meerkat, monitor, 'same version reuses the monitor without assets');
  assert.equal(d.document.listeners.length, 1);
  assert.equal(d.run(shellExpression('remove')).ok, true, 'injector remove expression tears down');
  assert.equal(d.window.__meerkat, undefined);
  assert.deepEqual(hostValue(d.run(shellExpression(null))), { ok: false, needAssets: true, missing: ['Meerkat UI assets'] });
});

test('shell mounts the adapter read-only inside a nested ShadowRoot of the overlay', async (t) => {
  const c = codex(t);
  const r = c.eval(workflowState(), { assets: true });
  assert.equal(r.ok, true);
  assert.equal(r.version, ASSET_VERSION);
  for (let i = 0; i < 3; i++) c.eval(workflowState());
  const entries = c.document.querySelectorAll('[data-meerkat-entry]');
  assert.equal(entries.length, 1);
  const [entry] = entries;
  assert.equal(entry.previousElementSibling, c.$('#plugins'));
  for (const a of ['href', 'id', 'data-state', 'aria-selected', 'aria-current']) assert.equal(entry.hasAttribute(a), false, a);
  assert.equal(entry.textContent.trim(), 'Meerkat');
  assert.equal(entry.querySelectorAll('svg').length, 0);
  const icon = entry.querySelector('[data-meerkat-icon]');
  assert.equal(icon.parentElement.getAttribute('class'), 'ic', 'logo replaces the original icon in place');
  assert.equal(icon.style.background, 'currentColor');
  assert.equal(icon.style.maskImage, `url("${ICON}")`);
  assert.equal(c.docListeners(), 1);
  assert.equal(c.activeObservers(), 1);
  assert.equal(c.mounts.length, 0, 'nothing mounted until opened');

  c.click(entry);
  assert.equal(entry.getAttribute('aria-current'), 'page');
  const view = c.view();
  assert.equal(view.parentElement, c.$('[data-app-shell-main-content-layout]'));
  assert.equal(c.$('[data-app-shell-main-content-layout]').style.position, 'relative');
  assert.equal(c.document.querySelectorAll('iframe').length, 0);
  assert.ok(c.mountEl().shadowRoot, 'mount creates its own ShadowRoot inside the outer one');
  assert.equal(c.mounts.length, 1);
  const { opts } = c.mounts[0];
  assert.equal(opts.readonly, true);
  assert.equal(opts.theme, 'light');
  assert.match(opts.readonlyNote, /只读（非官方实验适配器）.*coordinator CLI/);
  assert.deepEqual(Object.keys(opts).sort(), ['onAction', 'readonly', 'readonlyNote', 'theme']);
  const ui = c.ui();
  assert.match(ui.textContent, /Wire desktop overlay/);
  assert.match(ui.querySelector('[data-ref="sum"]').textContent, /1/);
  assert.equal(c.$('#meerkat-ui'), null, 'UI lives only inside the ShadowRoots');
  await c.tick();
});

test('shell theme follows the Codex <html> class, else prefers-color-scheme', (t) => {
  const c = codex(t);
  c.eval(workflowState(), { assets: true });
  c.document.documentElement.classList.add('dark');
  c.click(c.$('[data-meerkat-entry]'));
  assert.equal(c.mounts.at(-1).opts.theme, 'dark');
  c.click(c.$('[data-sidebar-destination="builtin:chats"]'));
  c.document.documentElement.classList.remove('dark');
  c.context.matchMedia = () => ({ matches: true });
  c.click(c.$('[data-meerkat-entry]'));
  assert.equal(c.mounts.at(-1).opts.theme, 'dark');
});

test('shell keeps the last snapshot visibly stale on errors; 503 before any snapshot shows unknown, not empty', async (t) => {
  const c = codex(t);
  c.eval({ kind: 'error', message: '工作流服务不可用（HTTP 503：workflow core not installed）' }, { assets: true });
  c.click(c.$('[data-meerkat-entry]'));
  let ui = c.ui();
  assert.match(ui.textContent, /已断连.*HTTP 503/s);
  assert.equal(ui.querySelector('[data-ref="sum"]').textContent.trim(), '运行数 未知');

  c.eval(workflowState());
  ui = c.ui();
  assert.doesNotMatch(ui.textContent, /已断连/);
  c.eval({ kind: 'error', message: '无法连接本地状态服务 http://127.0.0.1:47824' });
  ui = c.ui();
  assert.match(ui.textContent, /已断连.*无法连接/s);
  assert.match(ui.textContent, /Wire desktop overlay/, 'known history kept');

  // Reopening rebuilds the UI from the last snapshot and still marks it stale.
  c.click(c.$('[data-sidebar-destination="builtin:chats"]'));
  c.click(c.$('[data-meerkat-entry]'));
  ui = c.ui();
  assert.match(ui.textContent, /Wire desktop overlay/);
  assert.match(ui.textContent, /已断连/);

  // Legacy service: no snapshot is passed (the adapter shows it as unknown).
  c.eval({ kind: 'legacy', count: 1, legacyActive: [{ id: 'x', task: 'old pi task' }], at: 1 });
  assert.equal(c.mounts.at(-1).view.snapshot, null);
  assert.equal(c.mounts.at(-1).view.legacy[0].task, 'old pi task');
});

test('shell opened before any state shows waiting, never an empty snapshot', (t) => {
  const c = codex(t);
  c.eval(null, { assets: true });
  c.click(c.$('[data-meerkat-entry]'));
  assert.match(c.ui().textContent, /已断连 正在等待本地状态服务/);
  assert.equal(c.mounts[0].view.snapshot, null);
});

test('shell reconnect only calls the injector binding and settles on the next state', async (t) => {
  const c = codex(t);
  const calls = [];
  c.window[RECONNECT_BINDING] = (payload) => calls.push(payload);
  c.eval({ kind: 'error', message: 'down' }, { assets: true });
  c.click(c.$('[data-meerkat-entry]'));
  const { onAction } = c.mounts[0].opts;
  const p = onAction({ type: 'reconnect' });
  assert.deepEqual(calls, ['reconnect']);
  c.eval(workflowState());
  await p;
  assert.match(c.ui().textContent, /Wire desktop overlay/);
  const q = onAction({ type: 'reconnect' });
  c.eval({ kind: 'error', message: 'still down' });
  await assert.rejects(q, /still down/);
});

test('shell onAction rejects stop/settings/unknown actions (read-only)', async (t) => {
  const c = codex(t);
  c.eval(workflowState(), { assets: true });
  c.click(c.$('[data-meerkat-entry]'));
  const { onAction } = c.mounts[0].opts;
  await assert.rejects(onAction({ type: 'stop', runId: RUN_ID, requestId: RUN_ID }), /只读.*coordinator CLI/);
  await assert.rejects(onAction({ type: 'settings', input: { maxConcurrency: 2 } }), /只读/);
  await assert.rejects(onAction({ type: 'exec', command: 'rm -rf /' }), /只读/);
  await assert.rejects(onAction({ type: 'reconnect' }), /未连接/, 'no binding → helpful error');
});

test('native navigation closes the overlay; sidebar rerenders re-attach; remove tears everything down', async (t) => {
  const c = codex(t);
  c.eval(workflowState(), { assets: true });
  c.click(c.$('[data-meerkat-entry]'));
  c.click(c.$('[data-sidebar-destination="builtin:chats"]').querySelector('span'));
  assert.equal(c.view(), null);
  assert.equal(c.mounts[0].destroyed, true, 'adapter destroyed');
  assert.equal(c.$('[data-app-shell-main-content-layout]').style.position, '');
  assert.equal(c.$('[data-meerkat-entry]').hasAttribute('aria-current'), false);
  assert.equal(c.window.__meerkat.ui, null);

  // Host rerender drops the entry and overlay; the observer re-attaches both after the debounce.
  c.click(c.$('[data-meerkat-entry]'));
  c.$('[data-meerkat-entry]').remove();
  c.view().remove();
  await new Promise((r) => setTimeout(r, 150));
  assert.equal(c.document.querySelectorAll('[data-meerkat-entry]').length, 1);
  assert.ok(c.view(), 'open overlay recreated after rerender');
  assert.equal(c.mounts[1].destroyed, true, 'detached adapter destroyed before remount');
  assert.match(c.ui().textContent, /Wire desktop overlay/);

  assert.equal(c.eval('remove').ok, true);
  assert.ok(c.mounts.every((m) => m.destroyed));
  assert.equal(c.window.__meerkat, undefined);
  assert.equal(c.docListeners(), 0);
  assert.equal(c.activeObservers(), 0);
  assert.equal(c.document.querySelectorAll('[data-meerkat-entry], [data-meerkat-view]').length, 0);
});

test('shell closes the overlay and rethrows if the mount adapter throws', (t) => {
  const c = codex(t);
  c.eval(workflowState(), { assets: true });
  c.window.__meerkat.createUI = () => { throw new Error('mount failed'); };
  assert.throws(() => c.click(c.$('[data-meerkat-entry]')), /mount failed/);
  assert.equal(c.view(), null);
  assert.equal(c.window.__meerkat.open, false);
  assert.equal(c.$('[data-app-shell-main-content-layout]').style.position, '');
});

test('shell migrates from a pre-versioned or outdated monitor instead of reusing stale handlers', (t) => {
  const c = codex(t);
  // Simulate the previous single-file shell: no version, its own entry, listener, and observer.
  const oldEntry = c.document.createElement('button');
  oldEntry.setAttribute('data-meerkat-entry', '');
  c.$('#plugins').after(oldEntry);
  const oldView = c.document.createElement('section');
  oldView.setAttribute('data-meerkat-view', '');
  c.$('[data-app-shell-main-content-layout]').append(oldView);
  const onClick = () => {};
  c.document.addEventListener('click', onClick, true);
  const observer = new c.context.MutationObserver(() => {});
  observer.observe(c.document.body);
  let removed = 0;
  let staleRender = 0;
  c.window.__meerkat = {
    state: null, open: true, observer, onClick, timer: 0,
    render: () => { staleRender++; },
    remove: () => { removed++; observer.disconnect(); c.document.removeEventListener('click', onClick, true); oldEntry.remove(); delete c.window.__meerkat; },
  };

  assert.equal(c.eval(workflowState()).needAssets, true, 'outdated monitor is not driven without new assets');
  assert.equal(removed, 0);
  const r = c.eval(workflowState(), { assets: true });
  assert.equal(r.ok, true);
  assert.equal(removed, 1);
  assert.equal(staleRender, 0);
  assert.equal(c.window.__meerkat.version, ASSET_VERSION);
  assert.equal(c.docListeners(), 1);
  assert.equal(c.activeObservers(), 1);
  assert.equal(c.document.querySelectorAll('[data-meerkat-entry]').length, 1);
  assert.equal(c.view(), null, 'old overlay removed');

  // A different (older) version with UI open is destroyed before reinstall.
  c.click(c.$('[data-meerkat-entry]'));
  const oldUi = c.mounts.at(-1);
  c.window.__meerkat.version = 'old-version';
  c.eval(workflowState(), { assets: true });
  assert.equal(oldUi.destroyed, true);
  assert.equal(c.window.__meerkat.version, ASSET_VERSION);
  assert.equal(c.docListeners(), 1);
  assert.equal(c.activeObservers(), 1);
  c.eval('remove');
  assert.equal(c.docListeners(), 0);
});

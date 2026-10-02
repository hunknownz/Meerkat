import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import vm from 'node:vm';
import {
  parsePort, parseLoopbackUrl, parseCli, selectTarget, validateWsUrl, toShellState, toWorkflowState, shellExpression,
  factorySource, fetchState, createSession, ASSET_VERSION, UI_CSS, RECONNECT_BINDING, UsageError,
} from '../desktop/injector.mjs';
import { createMeerkatUI } from '../dashboard/public/ui.js';
import { start, urlOf } from '../dashboard/server.mjs';
import { createDom } from './fixtures/mini-dom.mjs';

const SHELL_SRC = readFileSync(new URL('../desktop/shell.js', import.meta.url), 'utf8');
const UI_SRC = readFileSync(new URL('../dashboard/public/ui.js', import.meta.url), 'utf8');
const RUN_ID = '3f2b8c1e-6a4d-4e2f-9b7a-1c2d3e4f5a6b';

const SNAPSHOT = {
  schemaVersion: 1,
  observedAt: '2026-10-02T09:00:00Z',
  projects: [{ id: 'meerkat', name: 'Meerkat' }],
  tasks: [{ id: 'task-1', projectId: 'meerkat', title: 'Wire desktop overlay', state: 'developing' }],
  runs: [{ id: RUN_ID, taskId: 'task-1', role: 'developer', state: 'running', startedAt: '2026-10-02T08:55:00Z' }],
  deliveries: [], reviews: [], contexts: [], profiles: [],
  counts: { running: 1, queued: 0 },
  controller: { state: 'running' },
};
const workflowState = (data = SNAPSHOT) => ({ kind: 'workflow', data, legacyActive: [], at: 1 });

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

test('toWorkflowState keeps the public snapshot exactly and strips the session token', () => {
  const s = toWorkflowState({ ok: true, data: SNAPSHOT, legacyActive: [{ id: 'p', task: 't' }], sessionToken: 'secret-token-value' }, 7);
  assert.equal(s.kind, 'workflow');
  assert.deepEqual(s.data, SNAPSHOT);
  assert.equal(s.legacyActive[0].id, 'p');
  assert.doesNotMatch(JSON.stringify(s), /sessionToken|secret-token-value/);
  assert.throws(() => toWorkflowState({ ok: true, data: { schemaVersion: 2 }, sessionToken: 't' }), /malformed/);
});

test('factorySource adapts the trusted ui.js module into a closure returning the real createMeerkatUI', () => {
  const src = factorySource();
  assert.match(src, /^\(function meerkatUIFactory\(\) \{/);
  assert.doesNotMatch(src, /^\s*(export|import)\b/m);
  const factory = vm.runInNewContext(src)();
  assert.equal(typeof factory, 'function');
  assert.equal(factory.name, 'createMeerkatUI');
  assert.equal(factory.toString(), createMeerkatUI.toString());
  assert.throws(() => factory(null), /root element required/);
  // Never the standalone bootstrap, and nothing that fetches or carries tokens.
  assert.doesNotMatch(src, /startDashboard|getElementById\('meerkat-ui'\)|\bfetch\(|X-Meerkat-Token/);
  assert.throws(() => factorySource(`import x from './y.js';\n${UI_SRC}`), /must not import/);
  assert.throws(() => factorySource(`${UI_SRC}\nexport { esc as escape };`), /unsupported export/);
  assert.throws(() => factorySource(`${UI_SRC}\nexport default 1;`), /unsupported export/);
  assert.throws(() => factorySource('export const x = 1;'), /does not define createMeerkatUI/);
});

test('shellExpression sends assets only on demand and keeps the version in both forms', () => {
  const small = shellExpression({ kind: 'error', message: 'x' });
  const full = shellExpression(null, { assets: true });
  assert.ok(small.includes(JSON.stringify(ASSET_VERSION)) && full.includes(JSON.stringify(ASSET_VERSION)));
  assert.ok(!small.includes('meerkatUIFactory') && full.includes('meerkatUIFactory'));
  assert.ok(full.includes(JSON.stringify(UI_CSS)));
  assert.match(ASSET_VERSION, /^[0-9a-f]{16}$/);
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
    ['malformed', { '/api/workflow': { status: 200, body: { ok: true, data: { schemaVersion: 1, runs: {} }, sessionToken: 't' } } }, /无效数据/],
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

test('fetchState against a real loopback dashboard: exact snapshot without token; missing core is 503 unknown', async () => {
  const dataDir = mkdtempSync(join(tmpdir(), 'meerkat-desktop-'));
  const workflow = { readWorkflow: async () => structuredClone(SNAPSHOT), requestStop: async () => ({}), updateSettings: async () => ({}) };
  let server = await start({ port: 0, dataDir, workflow, sessionToken: 'desktop-test-token-0123456789' });
  try {
    const base = new URL(urlOf(server));
    assert.equal(base.hostname, '127.0.0.1');
    const urls = { workflowUrl: new URL('api/workflow', base), activeUrl: new URL('api/active', base) };
    const s = await fetchState(urls);
    assert.equal(s.kind, 'workflow');
    assert.deepEqual(s.data, SNAPSHOT);
    assert.doesNotMatch(JSON.stringify(s), /desktop-test-token/);
    await new Promise((r) => server.close(r));
    // Default lazy core (not installed in this checkout or failing) → 503, shown as unknown.
    server = await start({ port: 0, dataDir, workflow: { readWorkflow: async () => { throw new Error('corrupt'); } } });
    const base2 = new URL(urlOf(server));
    const e = await fetchState({ workflowUrl: new URL('api/workflow', base2), activeUrl: new URL('api/active', base2) });
    assert.equal(e.kind, 'error');
    assert.match(e.message, /HTTP 503/);
  } finally {
    await new Promise((r) => server.close(r));
    rmSync(dataDir, { recursive: true, force: true });
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
  assert.ok(sent[0].includes('meerkatUIFactory'), 'first push carries assets');
  assert.equal(loads, 1);
  assert.equal(session.stale, true);
  await session.tick();
  assert.equal(loads, 1, 'no automatic refetch while stale');
  assert.ok(!sent[1].includes('meerkatUIFactory'));
  assert.match(sent[1], /\)\(null,/, 'stale tick only re-attaches');
  session.requestReconnect();
  await session.tick();
  assert.equal(loads, 2);
  assert.equal(session.stale, false);
  // Renderer reloaded: monitor gone → shell asks for assets → resent in the same tick.
  reply = (expr) => (expr.includes('meerkatUIFactory') ? { ok: true } : { ok: false, needAssets: true });
  const before = sent.length;
  const r = await session.tick();
  assert.equal(r.ok, true);
  assert.equal(sent.length - before, 2);
  assert.ok(sent.at(-1).includes('meerkatUIFactory'));
});

// ---- shell in an owned local DOM (no browser, no Codex) ----------------------

const CODEX_DOM = `<nav id="app-shell-sidebar"><div data-app-action-sidebar-scroll>
  <button data-sidebar-destination="builtin:chats" aria-current="page"><svg></svg><span>Chats</span></button>
  <a href="/plugins" id="plugins" data-state="active" aria-selected="true"><span class="ic"><svg><path d="M0 0"/></svg></span><span>Plugins</span></a>
</div></nav><main data-app-shell-main-content-layout><p>native</p></main>`;

function codex() {
  const d = createDom();
  d.document.body.innerHTML = CODEX_DOM;
  const docListeners = () => d.document.listeners.length;
  const activeObservers = () => d.observers.filter((o) => o.active).length;
  const $ = (s) => d.document.querySelector(s);
  const view = () => $('[data-meerkat-view]');
  const shadow = () => view()?.shadowRoot;
  const ui = () => shadow()?.querySelector('#meerkat-ui');
  const eval_ = (state, opts) => d.run(shellExpression(state, opts));
  return { ...d, $, view, shadow, ui, docListeners, activeObservers, eval: eval_ };
}

test('shell reports missing selectors and is idempotent per version', () => {
  const d = createDom();
  const r = d.run(shellExpression({ kind: 'error', message: 'x' }, { assets: true }));
  assert.equal(r.ok, false);
  assert.deepEqual([...r.missing], ['[data-app-action-sidebar-scroll]', '[data-app-shell-main-content-layout]']);
  const monitor = d.window.__meerkat;
  assert.equal(monitor.version, ASSET_VERSION);
  d.run(shellExpression(null));
  assert.equal(d.window.__meerkat, monitor, 'same version reuses the monitor without assets');
  assert.equal(d.document.listeners.length, 1);
  assert.equal(d.run(shellExpression('remove')).ok, true);
  assert.equal(d.window.__meerkat, undefined);
  assert.deepEqual({ ...d.run(shellExpression(null)) }, { ok: false, needAssets: true, missing: ['Meerkat UI assets'] });
});

test('shell mounts the shared factory with app.css in a ShadowRoot of the overlay (read-only)', async (t) => {
  const c = codex();
  t.after(() => c.eval('remove'));
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
  assert.equal(icon.parentElement.getAttribute('class'), 'ic', 'icon replaces the original icon in place');
  assert.equal(icon.style.background, 'currentColor');
  assert.match(icon.style.maskImage, /^url\("data:image\/svg\+xml;base64,/);
  assert.equal(c.docListeners(), 1);
  assert.equal(c.activeObservers(), 1);

  c.click(entry);
  assert.equal(entry.getAttribute('aria-current'), 'page');
  const view = c.view();
  assert.equal(view.parentElement, c.$('[data-app-shell-main-content-layout]'));
  assert.equal(c.$('[data-app-shell-main-content-layout]').style.position, 'relative');
  assert.equal(c.document.querySelectorAll('iframe').length + view.shadowRoot.querySelectorAll('iframe').length, 0);
  assert.equal(view.shadowRoot.adoptedStyleSheets.length, 1);
  assert.equal(view.shadowRoot.adoptedStyleSheets[0].text, UI_CSS, 'exact shared app.css');
  const ui = c.ui();
  assert.ok(ui.querySelector('header.top'), 'factory chrome rendered');
  assert.equal(ui.dataset.theme, 'light');
  assert.match(ui.textContent, /Wire desktop overlay/);
  assert.match(ui.querySelector('[data-ref="sum"]').textContent, /1/);
  assert.equal(c.$('#meerkat-ui'), null, 'UI lives only inside the ShadowRoot');

  // Read-only controls: expanding the running run shows the CLI note instead of a stop button.
  c.click(ui.querySelector(`[data-agent="${RUN_ID}"]`));
  assert.equal(ui.querySelectorAll('[data-stop]').length, 0);
  assert.match(ui.textContent, /只读（非官方适配器）.*coordinator CLI/);
  c.click(ui.querySelector('[data-act="settings"]'));
  const sheet = ui.querySelector('[data-ref="settings"]');
  assert.match(sheet.textContent, /coordinator CLI/);
  assert.ok(sheet.querySelector('[data-act="settings-save"]')?.disabled ?? true);
  for (const sel of sheet.querySelectorAll('select')) assert.equal(sel.disabled, true);
  await c.tick();
});

test('shell keeps the last snapshot visibly stale on errors; 503 before any snapshot shows unknown, not empty', async (t) => {
  const c = codex();
  t.after(() => c.eval('remove'));
  c.eval({ kind: 'error', message: '工作流服务不可用（HTTP 503：workflow core not installed）' }, { assets: true });
  c.click(c.$('[data-meerkat-entry]'));
  let ui = c.ui();
  assert.match(ui.textContent, /已断连.*HTTP 503.*运行数未知.*尚未取得任何快照/s);
  assert.doesNotMatch(ui.querySelector('[data-ref="sum"]').textContent, /\b0\b/);
  assert.doesNotMatch(ui.textContent, /当前没有工作流运行/);

  c.eval(workflowState());
  assert.doesNotMatch(ui.textContent, /已断连/);
  c.eval({ kind: 'error', message: '无法连接本地状态服务 http://127.0.0.1:47824' });
  assert.match(ui.textContent, /已断连.*运行数未知.*最近快照，已过期/s);
  assert.match(ui.textContent, /Wire desktop overlay/, 'known history kept');
  assert.equal(ui.querySelector('[data-ref="sum"]').textContent.trim(), '运行数 未知');

  // Reopening rebuilds the UI from the last snapshot and still marks it stale.
  c.click(c.$('[data-sidebar-destination="builtin:chats"]'));
  c.click(c.$('[data-meerkat-entry]'));
  ui = c.ui();
  assert.match(ui.textContent, /Wire desktop overlay/);
  assert.match(ui.textContent, /已断连/);

  // Legacy service: independent runs listed, workflow count stays unknown.
  c.eval({ kind: 'legacy', count: 1, legacyActive: [{ id: 'x', task: 'old pi task', model: 'm', worktree: '/w', startedAt: '' }], at: 1 });
  assert.match(ui.textContent, /old pi task/);
  assert.match(ui.querySelector('[data-ref="sum"]').textContent, /未知/);
});

test('shell reconnect only calls the injector binding and settles on the next state', async (t) => {
  const c = codex();
  t.after(() => c.eval('remove'));
  const calls = [];
  c.context[RECONNECT_BINDING] = (payload) => calls.push(payload);
  c.window[RECONNECT_BINDING] = c.context[RECONNECT_BINDING];
  c.eval({ kind: 'error', message: 'down' }, { assets: true });
  c.click(c.$('[data-meerkat-entry]'));
  const ui = c.ui();
  c.click(ui.querySelector('[data-act="reconnect"]'));
  assert.deepEqual(calls, ['reconnect']);
  assert.match(ui.querySelector('[data-act="reconnect"]').textContent, /正在重连/);
  c.eval(workflowState());
  await c.tick();
  assert.equal(ui.querySelector('[data-act="reconnect"]'), null);
  assert.match(ui.textContent, /Wire desktop overlay/);
});

test('shell factory options are read-only and reject stop/settings actions', async (t) => {
  const c = codex();
  t.after(() => c.eval('remove'));
  c.eval(workflowState(), { assets: true });
  const monitor = c.window.__meerkat;
  const real = monitor.createUI;
  let seen;
  monitor.createUI = (root, opts) => { seen = opts; return real(root, opts); };
  c.click(c.$('[data-meerkat-entry]'));
  assert.equal(seen.readonly, true);
  assert.equal(seen.themeKey, null);
  await assert.rejects(seen.onAction({ type: 'stop', runId: RUN_ID, requestId: RUN_ID }), /只读.*coordinator CLI/);
  await assert.rejects(seen.onAction({ type: 'settings', input: { maxConcurrency: 2 } }), /只读/);
  await assert.rejects(seen.onAction({ type: 'exec', command: 'rm -rf /' }), /只读/);
  await assert.rejects(seen.onAction({ type: 'reconnect' }), /未连接/, 'no binding → helpful error');
});

test('native navigation closes the overlay; sidebar rerenders re-attach; remove tears everything down', async () => {
  const c = codex();
  c.eval(workflowState(), { assets: true });
  c.click(c.$('[data-meerkat-entry]'));
  const ui = c.ui();
  c.click(c.$('[data-sidebar-destination="builtin:chats"] span'));
  assert.equal(c.view(), null);
  assert.equal(ui.innerHTML, '', 'factory destroyed');
  assert.equal(c.$('[data-app-shell-main-content-layout]').style.position, '');
  assert.equal(c.$('[data-meerkat-entry]').hasAttribute('aria-current'), false);
  assert.equal(c.window.__meerkat.ui, null);

  // Host rerender drops the entry; the observer re-attaches it after the debounce.
  c.click(c.$('[data-meerkat-entry]'));
  c.$('[data-meerkat-entry]').remove();
  c.view().remove();
  await new Promise((r) => setTimeout(r, 150));
  assert.equal(c.document.querySelectorAll('[data-meerkat-entry]').length, 1);
  assert.ok(c.view(), 'open overlay recreated after rerender');
  assert.match(c.ui().textContent, /Wire desktop overlay/);

  const shadowUi = c.ui();
  assert.equal(c.eval('remove').ok, true);
  assert.equal(shadowUi.innerHTML, '');
  assert.equal(c.window.__meerkat, undefined);
  assert.equal(c.docListeners(), 0);
  assert.equal(c.activeObservers(), 0);
  assert.equal(c.document.querySelectorAll('[data-meerkat-entry], [data-meerkat-view]').length, 0);
});

test('shell migrates from a pre-versioned or outdated monitor instead of reusing stale handlers', () => {
  const c = codex();
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
  const oldUi = c.ui();
  c.window.__meerkat.version = 'old-version';
  c.eval(workflowState(), { assets: true });
  assert.equal(oldUi.innerHTML, '');
  assert.equal(c.window.__meerkat.version, ASSET_VERSION);
  assert.equal(c.docListeners(), 1);
  assert.equal(c.activeObservers(), 1);
  c.eval('remove');
  assert.equal(c.docListeners(), 0);
});

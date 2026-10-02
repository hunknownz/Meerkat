// Integration: desktop/shell.js + the real React mount bundle (internal/web/assets/mount/meerkat-ui.js)
// in jsdom from frontend/node_modules (test-only; run `npm ci` in frontend/ first). The bundle is local,
// trusted repository code evaluated the same way the injector's CDP Runtime.evaluate would; nothing is
// fetched and no Codex instance is contacted.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { shellExpression, toWorkflowState, ASSET_VERSION, RECONNECT_BINDING } from '../desktop/injector.mjs';

const require = createRequire(new URL('../frontend/package.json', import.meta.url));
const { JSDOM } = require('jsdom');

const RUN_ID = '3f2b8c1e-6a4d-4e2f-9b7a-1c2d3e4f5a6b';
const SNAPSHOT = {
  snapshotVersion: 1,
  observedAt: '2026-10-02T09:00:00Z',
  projects: [{ id: 'meerkat', name: 'Meerkat' }],
  tasks: [{ id: 'task-1', projectId: 'meerkat', title: 'Wire desktop overlay', state: 'developing' }],
  runs: [{ id: RUN_ID, taskId: 'task-1', agentId: 'Pi-01', role: 'developer', state: 'running', startedAt: '2026-10-02T08:55:00Z', events: [] }],
  deliveries: [], reviews: [], contexts: [], profiles: [],
  counts: { running: 1, queued: 0, unknown: 0 },
  controller: { state: 'running' },
  settings: { maxConcurrency: 2, maxFixRounds: 2, defaultProfiles: {} },
};
const TOKEN = 'tok-secret-desktop-0123456789';
const CODEX_DOM = `<!doctype html><html><body><nav id="app-shell-sidebar"><div data-app-action-sidebar-scroll>
  <button data-sidebar-destination="builtin:chats"><span>Chats</span></button>
  <a href="/plugins" id="plugins"><span class="ic"><svg></svg></span><span>Plugins</span></a>
</div></nav><main data-app-shell-main-content-layout><p>native</p></main></body></html>`;

const settle = () => new Promise((r) => setTimeout(r, 30));

function codex(t) {
  // outside-only: only window.eval of our own expressions runs; page <script>s never execute.
  const dom = new JSDOM(CODEX_DOM, { runScripts: 'outside-only', pretendToBeVisual: true });
  const w = dom.window;
  t.after(() => {
    try { w.eval(shellExpression('remove')); } catch { /* best effort */ }
    w.close();
  });
  const $ = (s) => w.document.querySelector(s);
  const inner = () => $('[data-meerkat-view]')?.shadowRoot?.querySelector('[data-meerkat-mount]')?.shadowRoot ?? null;
  const run = (state, opts) => JSON.parse(JSON.stringify(w.eval(shellExpression(state, opts))));
  return { w, $, inner, run };
}

const bridge = () => toWorkflowState({ ok: true, data: SNAPSHOT, legacyActive: [], sessionToken: TOKEN }, 1);
const button = (root, text) => [...root.querySelectorAll('button')].find((b) => b.textContent.trim() === text);

test('real React mount renders the bridged snapshot read-only inside the overlay ShadowRoot', async (t) => {
  const c = codex(t);
  const state = bridge();
  const expr = shellExpression(state, { assets: true });
  assert.ok(!expr.includes(TOKEN), 'session token never reaches the renderer');
  assert.deepEqual(c.run(state, { assets: true }), { ok: true, version: ASSET_VERSION });
  assert.equal(typeof c.w.MeerkatUI, 'undefined', 'bundle global stays inside the loader closure');
  c.$('[data-meerkat-entry]').click();
  await settle();
  const root = c.inner();
  assert.ok(root, 'React mount attached its own ShadowRoot in the outer one');
  assert.match(root.querySelector('style').textContent, /#meerkat-ui/);
  assert.match(root.textContent, /Wire desktop overlay/);
  assert.equal(c.$('#meerkat-ui'), null, 'nothing leaks into the host document');

  // Read-only: stop disabled with the adapter's note; settings controls disabled.
  root.querySelector('.row-btn').click();
  await settle();
  assert.equal(button(root, '停止运行').disabled, true);
  assert.match(root.textContent, /只读（非官方实验适配器）.*coordinator CLI/);
  root.querySelector('button[aria-label="设置"]').click();
  await settle();
  const sheet = root.querySelector('[aria-labelledby="mk-settings-title"]');
  assert.ok(sheet, 'settings sheet opened');
  assert.match(sheet.textContent, /coordinator CLI/);
  const controls = sheet.querySelectorAll('select, input');
  assert.ok(controls.length >= 2);
  for (const el of controls) assert.equal(el.disabled, true);
});

test('disconnect keeps the last snapshot stale; reconnect goes only through the CDP binding', async (t) => {
  const c = codex(t);
  const calls = [];
  c.w[RECONNECT_BINDING] = (p) => calls.push(p);
  c.run(bridge(), { assets: true });
  c.$('[data-meerkat-entry]').click();
  c.run({ kind: 'error', message: '无法连接本地状态服务 http://127.0.0.1:47824', at: 2 });
  await settle();
  let root = c.inner();
  assert.match(root.textContent, /已断连.*无法连接本地状态服务.*运行数未知/s);
  assert.match(root.textContent, /Wire desktop overlay/, 'last snapshot kept, marked stale');
  button(root, '重新连接').click();
  assert.deepEqual(calls, ['reconnect']);
  c.run(bridge());
  await settle();
  root = c.inner();
  assert.doesNotMatch(root.textContent, /已断连/);

  // Legacy-only service: no snapshot; shown as unknown instead of an empty workflow.
  c.run({ kind: 'legacy', count: 1, legacyActive: [{ id: 'x', task: 'old' }], at: 3 });
  await settle();
  assert.match(c.inner().textContent, /旧版状态服务仅提供 \/api\/active（1 个独立运行）/);
});

test('same asset version reuses the mount; remove and reinject clean up React roots', async (t) => {
  const c = codex(t);
  c.run(bridge(), { assets: true });
  const monitor = c.w.__meerkat;
  assert.equal(monitor.version, ASSET_VERSION);
  assert.deepEqual(c.run(bridge()), { ok: true, version: ASSET_VERSION }, 'version-only expression suffices');
  assert.equal(c.w.__meerkat, monitor);
  c.$('[data-meerkat-entry]').click();
  await settle();
  const first = c.inner();
  assert.ok(first.childNodes.length > 0);

  // Version mismatch: needs assets, then the old React root is destroyed before reinstall.
  monitor.version = 'outdated';
  assert.equal(c.run(bridge()).needAssets, true);
  c.run(bridge(), { assets: true });
  assert.equal(first.childNodes.length, 0, 'old React root unmounted');
  assert.notEqual(c.w.__meerkat, monitor);
  c.$('[data-meerkat-entry]').click();
  await settle();
  assert.match(c.inner().textContent, /Wire desktop overlay/);

  assert.deepEqual(c.run('remove'), { ok: true });
  assert.equal(c.w.__meerkat, undefined);
  assert.equal(c.w.document.querySelectorAll('[data-meerkat-entry], [data-meerkat-view]').length, 0);
  assert.equal(c.$('[data-app-shell-main-content-layout]').style.position, '');
  assert.equal(c.run(null).needAssets, true);
  assert.equal(c.run(bridge(), { assets: true }).ok, true, 'reinject after remove works');
  c.$('[data-meerkat-entry]').click();
  await settle();
  assert.match(c.inner().textContent, /Wire desktop overlay/);
});

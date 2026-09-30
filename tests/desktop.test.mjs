import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import {
  parsePort, parseLoopbackUrl, parseCli, selectTarget, validateWsUrl, toShellState, shellExpression, UsageError,
} from '../desktop/injector.mjs';

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

test('parseCli requires --cdp-port, defaults status URL, and normalizes active URL', () => {
  assert.throws(() => parseCli([]), /--cdp-port is required/);
  assert.throws(() => parseCli(['--cdp-port', '9222', '--bogus']), UsageError);
  const o = parseCli(['--cdp-port', '9222']);
  assert.equal(o.cdpPort, 9222);
  assert.equal(o.activeUrl.href, 'http://127.0.0.1:47824/api/active');
  const p = parseCli(['--cdp-port', '9222', '--status-url', 'http://127.0.0.1:5000/pi?x=1#y']);
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

test('toShellState validates payload and never turns failures into zero', () => {
  const s = toShellState({ ok: true, count: 1, agents: [{ id: 'a', task: 't', model: 'm', worktree: '/w', startedAt: 'x', extra: 1 }] }, 5);
  assert.deepEqual(s, { state: 'ok', count: 1, agents: [{ task: 't', model: 'm', worktree: '/w', startedAt: 'x' }], at: 5 });
  assert.equal(toShellState({ ok: true, count: 0, agents: [] }).count, 0);
  for (const bad of [null, [], { ok: false, count: 0, agents: [] }, { ok: true, agents: [] }, { ok: true, count: -1, agents: [] },
    { ok: true, count: 0 }, { ok: true, count: 1, agents: [null] }]) {
    assert.throws(() => toShellState(bad), /malformed/);
  }
});

test('shell script is a single function expression and reports missing selectors', () => {
  const src = readFileSync(new URL('../desktop/shell.js', import.meta.url), 'utf8');
  const listeners = [];
  const window = {};
  const context = vm.createContext({
    window,
    document: {
      body: {},
      querySelector: () => null,
      querySelectorAll: () => [],
      addEventListener: (...a) => listeners.push(a),
      removeEventListener: () => {},
    },
    MutationObserver: class { observe() {} disconnect() {} },
    setTimeout, clearTimeout,
  });
  const result = vm.runInContext(shellExpression({ state: 'error', message: 'x' }), context);
  assert.equal(result.ok, false);
  assert.deepEqual([...result.missing], ['[data-app-action-sidebar-scroll]', '[data-app-shell-main-content-layout]']);
  // Idempotent: a second call reuses the installed instance and adds no listeners.
  const monitor = window.__meerkat;
  vm.runInContext(shellExpression(null), context);
  assert.equal(window.__meerkat, monitor);
  assert.equal(listeners.length, 1);
  assert.equal(vm.runInContext(shellExpression('remove'), context).ok, true);
  assert.equal(window.__meerkat, undefined);
  assert.ok(src.includes('VERSION-SENSITIVE'));
});

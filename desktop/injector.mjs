#!/usr/bin/env node
// Optional, NON-OFFICIAL Codex desktop adapter.
//
// Connects over CDP to a Codex instance the user already launched with
//   --remote-debugging-address=127.0.0.1 --remote-debugging-port=<port>
// polls the local Meerkat workflow API itself (so the renderer CSP is untouched), strips
// the write session token, and injects desktop/shell.js to show a read-only "Meerkat"
// sidebar entry. The overlay renders the shared dashboard/public/ui.js factory and
// app.css inside a ShadowRoot; both are read from this local checkout (never fetched
// from the status service) and passed to the renderer as a closure, not via app.js.
//
// GET /api/workflow is the primary source. Only an explicit HTTP 404 (an older
// Meerkat service without the workflow API) falls back to GET /api/active; any other
// failure (503, malformed, unreachable) is shown as disconnected with the run count
// unknown and the last known snapshot marked stale.
//
// It never modifies app.asar or Codex user data, never starts Pi, and only talks to
// literal loopback addresses. Renderer selectors are version-sensitive (see shell.js).
//
// Usage: node desktop/injector.mjs --cdp-port 9222 [--status-url http://127.0.0.1:47824/]

import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';
import { parseWorkflow, parseLegacy } from '../dashboard/public/app.js';

export const DEFAULT_STATUS_URL = 'http://127.0.0.1:47824/';
export const POLL_MS = 4000;
const LOOPBACK_HOSTS = new Set(['127.0.0.1', '[::1]']);
const SHELL_SOURCE = readFileSync(new URL('./shell.js', import.meta.url), 'utf8');
const ICON_DATA = `data:image/svg+xml;base64,${readFileSync(new URL('../assets/meerkat-sidebar.svg', import.meta.url)).toString('base64')}`;
const UI_SOURCE = readFileSync(new URL('../dashboard/public/ui.js', import.meta.url), 'utf8');
export const UI_CSS = readFileSync(new URL('../dashboard/public/app.css', import.meta.url), 'utf8');
export const RECONNECT_BINDING = '__meerkatReconnect';
const MAX_RESPONSE_CHARS = 4 * 1024 * 1024;
// Only these declaration forms are exported by the shared factory module.
const KNOWN_EXPORT = /^export (?=(?:async )?function\b|const\b|let\b|class\b)/gm;

/**
 * Turns the trusted local ES module source of dashboard/public/ui.js into a function
 * expression that returns createMeerkatUI. Only the known `export` keywords are removed;
 * any import, re-export, or dynamic import makes the source unsupported.
 */
export function factorySource(source = UI_SOURCE) {
  if (/^\s*import\b/m.test(source) || /\bimport\s*\(/.test(source)) throw new Error('shared UI factory must not import modules');
  const body = source.replace(KNOWN_EXPORT, '');
  if (/^\s*export\b/m.test(body)) throw new Error('shared UI factory has an unsupported export form');
  if (!/^function createMeerkatUI\(/m.test(body)) throw new Error('shared UI factory does not define createMeerkatUI');
  return `(function meerkatUIFactory() {\n'use strict';\n${body}\nreturn createMeerkatUI;\n})`;
}

const FACTORY_SOURCE = factorySource();
/** Changes whenever the shell, factory, or styles change; the renderer migrates on mismatch. */
export const ASSET_VERSION = createHash('sha256').update(`${SHELL_SOURCE}\0${FACTORY_SOURCE}\0${UI_CSS}`).digest('hex').slice(0, 16);

export class UsageError extends Error {}

/** Parses a TCP port string strictly (digits only, 1..65535). */
export function parsePort(value, name = 'port') {
  if (typeof value !== 'string' || !/^\d{1,5}$/.test(value)) throw new UsageError(`${name} must be an integer 1-65535`);
  const port = Number(value);
  if (port < 1 || port > 65535) throw new UsageError(`${name} must be an integer 1-65535`);
  return port;
}

/** Parses a URL and requires a literal loopback host, allowed protocol, and no credentials. */
export function parseLoopbackUrl(value, protocols, name = 'url') {
  let url;
  try {
    url = new URL(value);
  } catch {
    throw new UsageError(`${name} is not a valid URL`);
  }
  if (!protocols.includes(url.protocol)) throw new UsageError(`${name} must use ${protocols.join(' or ')}`);
  if (!LOOPBACK_HOSTS.has(url.hostname)) throw new UsageError(`${name} host must be 127.0.0.1 or [::1]`);
  if (url.username || url.password) throw new UsageError(`${name} must not contain credentials`);
  if (!url.port) throw new UsageError(`${name} must include an explicit port`);
  parsePort(url.port, `${name} port`);
  return url;
}

export function parseCli(argv) {
  let values;
  try {
    ({ values } = parseArgs({
      args: argv,
      options: {
        'cdp-port': { type: 'string' },
        'status-url': { type: 'string', default: DEFAULT_STATUS_URL },
        help: { type: 'boolean', short: 'h' },
      },
      strict: true,
    }));
  } catch (err) {
    throw new UsageError(err.message);
  }
  if (values.help) return { help: true };
  if (values['cdp-port'] === undefined) throw new UsageError('--cdp-port is required');
  const cdpPort = parsePort(values['cdp-port'], '--cdp-port');
  const statusUrl = parseLoopbackUrl(values['status-url'], ['http:'], '--status-url');
  statusUrl.search = '';
  statusUrl.hash = '';
  if (!statusUrl.pathname.endsWith('/')) statusUrl.pathname += '/';
  return { cdpPort, statusUrl, workflowUrl: new URL('api/workflow', statusUrl), activeUrl: new URL('api/active', statusUrl) };
}

/** Picks the main Codex renderer page from /json/list; excludes dictation/overlay windows. */
export function selectTarget(list) {
  if (!Array.isArray(list)) return null;
  // Only the exact main renderer; detached windows, overlays, dictation, and any ?initialRoute= are never selected.
  const MAIN = 'app://-/index.html';
  const pages = list.filter((t) => t && t.type === 'page' && typeof t.webSocketDebuggerUrl === 'string'
    && !/dictation|overlay|detached|initialRoute/i.test(`${t.url || ''} ${t.title || ''}`));
  return pages.find((t) => t.url === MAIN)
    || pages.find((t) => String(t.title || '').trim() === 'Codex' && !String(t.url || '').includes('?'))
    || null;
}

/** Validates the target's websocket URL points back at the same loopback CDP port. */
export function validateWsUrl(value, cdpPort) {
  const url = parseLoopbackUrl(value, ['ws:'], 'webSocketDebuggerUrl');
  if (Number(url.port) !== cdpPort) throw new UsageError('webSocketDebuggerUrl port does not match --cdp-port');
  return url;
}

const clip = (v, n = 200) => String(v ?? '').replace(/[\u0000-\u001f\u007f]+/g, ' ').trim().slice(0, n);

/** Validates legacy GET /api/active (older service) into the factory's legacyActive shape. */
export function toShellState(payload, now = Date.now()) {
  if (!payload || typeof payload !== 'object' || payload.ok !== true
    || !Number.isInteger(payload.count) || payload.count < 0 || !Array.isArray(payload.agents)) {
    throw new Error('malformed status payload');
  }
  let legacyActive;
  try {
    legacyActive = parseLegacy(payload.agents);
  } catch {
    throw new Error('malformed status payload');
  }
  return { kind: 'legacy', count: payload.count, legacyActive, at: now };
}

/** Validates GET /api/workflow (same contract as the dashboard) and drops the session token. */
export function toWorkflowState(payload, now = Date.now()) {
  const { data, legacyActive } = parseWorkflow(payload);
  return { kind: 'workflow', data, legacyActive, at: now };
}

async function readJson(res) {
  const text = await res.text();
  if (text.length > MAX_RESPONSE_CHARS) throw new Error('response too large');
  return JSON.parse(text);
}

/**
 * Loads the state shown by the overlay. Never manufactures an empty snapshot:
 * every failure becomes {kind:'error'} (run count unknown in the UI).
 */
export async function fetchState(urls, fetchImpl = fetch, now = Date.now) {
  const get = (url) => fetchImpl(url, { redirect: 'error', signal: AbortSignal.timeout(3000), headers: { accept: 'application/json' } });
  const error = (message) => ({ kind: 'error', message, at: now() });
  let res;
  try {
    res = await get(urls.workflowUrl);
  } catch {
    return error(`无法连接本地状态服务 ${urls.workflowUrl.origin}`);
  }
  if (res.status === 404) {
    // Explicit "not found": an older Meerkat service without the workflow API.
    let legacy;
    try {
      legacy = await get(urls.activeUrl);
    } catch {
      return error(`无法连接本地状态服务 ${urls.activeUrl.origin}`);
    }
    if (!legacy.ok) return error(`状态服务不可用（HTTP ${legacy.status}）`);
    try {
      return toShellState(await readJson(legacy), now());
    } catch {
      return error('状态服务返回了无效数据');
    }
  }
  let body = null;
  try { body = await readJson(res); } catch { /* handled below */ }
  if (!res.ok) {
    const why = body && typeof body.error === 'string' ? `：${clip(body.error, 120)}` : '';
    return error(`工作流服务不可用（HTTP ${res.status}${why}）`);
  }
  try {
    return toWorkflowState(body, now());
  } catch {
    return error('工作流服务返回了无效数据');
  }
}

/**
 * Renderer expression for shell.js. With { assets: true } it also carries the shared UI
 * factory closure and app.css; otherwise only the version, so an outdated or missing
 * monitor answers {needAssets:true} and the caller resends with assets.
 */
export function shellExpression(state, { assets = false } = {}) {
  const pack = assets
    ? `{version:${JSON.stringify(ASSET_VERSION)},css:${JSON.stringify(UI_CSS)},load:${FACTORY_SOURCE}}`
    : `{version:${JSON.stringify(ASSET_VERSION)}}`;
  return `(${SHELL_SOURCE.trim()})(${JSON.stringify(state ?? null)},${JSON.stringify(ICON_DATA)},${pack})`;
}

/**
 * Polling session (no timers; the caller schedules tick()). While the service is
 * healthy every tick fetches; after a failure the stale state stays on screen and
 * fetching resumes only after requestReconnect() (the overlay's "reconnect" button).
 */
export function createSession({ evaluate, loadState }) {
  let installed = false;
  let stale = false;
  let reconnect = false;
  let chain = Promise.resolve();
  const push = async (state) => {
    let r = await evaluate(shellExpression(state, { assets: !installed }));
    if (r?.needAssets) r = await evaluate(shellExpression(state, { assets: true }));
    installed = !!r && !r.needAssets;
    return r;
  };
  const run = async () => {
    let state = null;
    if (!stale || reconnect) {
      reconnect = false;
      state = await loadState();
      stale = state?.kind === 'error';
    }
    return push(state);
  };
  return {
    requestReconnect() { reconnect = true; },
    get stale() { return stale; },
    tick() {
      const next = chain.then(run, run);
      chain = next.catch(() => {});
      return next;
    },
  };
}

class Cdp {
  constructor(ws) {
    this.ws = ws;
    this.nextId = 1;
    this.pending = new Map();
    this.handlers = new Map();
    ws.addEventListener('message', (ev) => {
      let msg;
      try { msg = JSON.parse(ev.data); } catch { return; }
      if (!msg.id && typeof msg.method === 'string') { this.handlers.get(msg.method)?.(msg.params); return; }
      const p = msg.id && this.pending.get(msg.id);
      if (!p) return;
      this.pending.delete(msg.id);
      if (msg.error) p.reject(new Error(msg.error.message));
      else p.resolve(msg.result);
    });
    ws.addEventListener('close', () => {
      for (const p of this.pending.values()) p.reject(new Error('CDP connection closed'));
      this.pending.clear();
    });
  }

  static connect(url) {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(url);
      ws.addEventListener('open', () => resolve(new Cdp(ws)), { once: true });
      ws.addEventListener('error', () => reject(new Error(`cannot open CDP websocket ${url.origin}`)), { once: true });
    });
  }

  on(method, fn) { this.handlers.set(method, fn); }

  send(method, params) {
    const id = this.nextId++;
    this.ws.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => this.pending.set(id, { resolve, reject }));
  }

  async evaluate(expression) {
    const r = await this.send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: false });
    if (r.exceptionDetails) throw new Error(`renderer exception: ${r.exceptionDetails.exception?.description || r.exceptionDetails.text}`);
    return r.result?.value;
  }
}

async function findTarget(cdpPort) {
  const listUrl = new URL(`http://127.0.0.1:${cdpPort}/json/list`);
  let list;
  try {
    const res = await fetch(listUrl, { redirect: 'error', signal: AbortSignal.timeout(3000) });
    list = await res.json();
  } catch {
    throw new UsageError(`No CDP endpoint at ${listUrl.origin}. Launch Codex with `
      + `--remote-debugging-address=127.0.0.1 --remote-debugging-port=${cdpPort}`);
  }
  const target = selectTarget(list);
  if (!target) throw new UsageError('No Codex renderer page found in /json/list (expected app://-/index.html or a main page titled "Codex")');
  return validateWsUrl(target.webSocketDebuggerUrl, cdpPort);
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function main(argv) {
  const opts = parseCli(argv);
  if (opts.help) {
    console.log('Usage: node desktop/injector.mjs --cdp-port <port> [--status-url http://127.0.0.1:47824/]');
    return 0;
  }
  const cdp = await Cdp.connect(await findTarget(opts.cdpPort));
  const session = createSession({
    evaluate: (expr) => cdp.evaluate(expr),
    loadState: () => fetchState(opts),
  });
  let wake = null;
  const nap = (ms) => new Promise((r) => { const t = setTimeout(r, ms); wake = () => { clearTimeout(t); r(); }; });
  // The overlay's "reconnect" button calls this binding; its payload is ignored and it can
  // only trigger one more status poll (no commands are accepted from the renderer).
  cdp.on('Runtime.bindingCalled', (p) => {
    if (p?.name !== RECONNECT_BINDING) return;
    session.requestReconnect();
    wake?.();
  });
  await cdp.send('Runtime.enable', {}).catch(() => {});
  await cdp.send('Runtime.addBinding', { name: RECONNECT_BINDING }).catch(() => {});

  // Wait briefly for the renderer to mount; fail loudly if the selectors never appear.
  let result;
  for (let i = 0; i < 10; i++) {
    session.requestReconnect();
    result = await session.tick();
    if (result?.ok) break;
    await sleep(1000);
  }
  if (!result?.ok) {
    await cdp.evaluate(shellExpression('remove')).catch(() => {});
    console.error(`Codex UI not supported: missing ${(result?.missing || ['unknown']).join(', ')}.`
      + '\nThis adapter relies on version-sensitive Codex renderer selectors; update desktop/shell.js for this Codex version.');
    return 2;
  }
  console.log(`Meerkat entry injected into Codex (NON-OFFICIAL, version-sensitive); polling ${opts.workflowUrl.href} `
    + `every ${POLL_MS / 1000}s. Ctrl+C to remove.`);

  let closed = false;
  cdp.ws.addEventListener('close', () => { closed = true; wake?.(); });
  let stopping = false;
  const stop = async () => {
    if (stopping) return;
    stopping = true;
    if (!closed) {
      await cdp.evaluate(shellExpression('remove')).catch(() => {});
      await cdp.send('Runtime.removeBinding', { name: RECONNECT_BINDING }).catch(() => {});
    }
    cdp.ws.close();
    process.exit(0);
  };
  process.on('SIGINT', stop);
  process.on('SIGTERM', stop);

  let warned = false;
  while (!closed) {
    await nap(POLL_MS);
    if (closed) break;
    const r = await session.tick().catch((e) => ({ ok: false, missing: [e.message] }));
    if (!r?.ok && !warned) {
      console.error(`warning: Meerkat entry could not be attached (${(r?.missing || []).join(', ')})`);
      warned = true;
    } else if (r?.ok) warned = false;
  }
  console.error('Codex renderer disconnected; re-run the injector after Codex restarts.');
  return 1;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  main(process.argv.slice(2)).then((code) => process.exit(code), (err) => {
    console.error(err instanceof UsageError ? `error: ${err.message}` : err);
    process.exit(err instanceof UsageError ? 64 : 1);
  });
}

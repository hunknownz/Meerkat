#!/usr/bin/env node
// Optional, NON-OFFICIAL Codex desktop adapter.
//
// Connects over CDP to a Codex instance the user already launched with
//   --remote-debugging-address=127.0.0.1 --remote-debugging-port=<port>
// polls the local Meerkat status API itself (so the renderer CSP is untouched), and
// injects desktop/shell.js to show a read-only "Meerkat" sidebar entry.
//
// It never modifies app.asar or Codex user data, never starts Pi, and only talks to
// literal loopback addresses. Renderer selectors are version-sensitive (see shell.js).
//
// Usage: node desktop/injector.mjs --cdp-port 9222 [--status-url http://127.0.0.1:47824/]

import { readFileSync } from 'node:fs';
import { parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';

export const DEFAULT_STATUS_URL = 'http://127.0.0.1:47824/';
export const POLL_MS = 4000;
const LOOPBACK_HOSTS = new Set(['127.0.0.1', '[::1]']);
const SHELL_SOURCE = readFileSync(new URL('./shell.js', import.meta.url), 'utf8');
const ICON_DATA = `data:image/png;base64,${readFileSync(new URL('../assets/meerkat.png', import.meta.url)).toString('base64')}`;

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
  return { cdpPort, statusUrl, activeUrl: new URL('api/active', statusUrl) };
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

/** Validates GET /api/active and reduces it to the state rendered by shell.js. */
export function toShellState(payload, now = Date.now()) {
  if (!payload || typeof payload !== 'object' || payload.ok !== true
    || !Number.isInteger(payload.count) || payload.count < 0 || !Array.isArray(payload.agents)) {
    throw new Error('malformed status payload');
  }
  const str = (v) => (typeof v === 'string' ? v.slice(0, 500) : '');
  const agents = payload.agents.map((a) => {
    if (!a || typeof a !== 'object') throw new Error('malformed status payload');
    return { task: str(a.task), model: str(a.model), worktree: str(a.worktree), startedAt: str(a.startedAt) };
  });
  return { state: 'ok', count: payload.count, agents, at: now };
}

export async function fetchStatus(activeUrl) {
  let res;
  try {
    res = await fetch(activeUrl, { redirect: 'error', signal: AbortSignal.timeout(3000), headers: { accept: 'application/json' } });
  } catch {
    return { state: 'error', message: `Status service unreachable at ${activeUrl.origin}` };
  }
  if (!res.ok) return { state: 'error', message: `Status service unavailable (HTTP ${res.status})` };
  try {
    return toShellState(await res.json());
  } catch {
    return { state: 'error', message: 'Status service returned malformed data' };
  }
}

export function shellExpression(state) {
  return `(${SHELL_SOURCE.trim()})(${JSON.stringify(state)},${JSON.stringify(ICON_DATA)})`;
}

class Cdp {
  constructor(ws) {
    this.ws = ws;
    this.nextId = 1;
    this.pending = new Map();
    ws.addEventListener('message', (ev) => {
      let msg;
      try { msg = JSON.parse(ev.data); } catch { return; }
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

  // Wait briefly for the renderer to mount; fail loudly if the selectors never appear.
  let result;
  for (let i = 0; i < 10; i++) {
    result = await cdp.evaluate(shellExpression(await fetchStatus(opts.activeUrl)));
    if (result?.ok) break;
    await sleep(1000);
  }
  if (!result?.ok) {
    await cdp.evaluate(shellExpression('remove')).catch(() => {});
    console.error(`Codex UI not supported: missing ${(result?.missing || ['unknown']).join(', ')}.`
      + '\nThis adapter relies on version-sensitive Codex renderer selectors; update desktop/shell.js for this Codex version.');
    return 2;
  }
  console.log(`Meerkat entry injected into Codex; polling ${opts.activeUrl.href} every ${POLL_MS / 1000}s. Ctrl+C to remove.`);

  let closed = false;
  cdp.ws.addEventListener('close', () => { closed = true; });
  let stopping = false;
  const stop = async () => {
    if (stopping) return;
    stopping = true;
    if (!closed) await cdp.evaluate(shellExpression('remove')).catch(() => {});
    cdp.ws.close();
    process.exit(0);
  };
  process.on('SIGINT', stop);
  process.on('SIGTERM', stop);

  let warned = false;
  while (!closed) {
    await sleep(POLL_MS);
    if (closed) break;
    const r = await cdp.evaluate(shellExpression(await fetchStatus(opts.activeUrl))).catch((e) => ({ ok: false, missing: [e.message] }));
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

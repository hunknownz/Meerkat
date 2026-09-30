#!/usr/bin/env node
// Meerkat dashboard: a loopback-only local task/agent board over a small
// private JSON store. Node 22 built-in HTTP only: no dependencies, no external
// assets, no shell execution. API writes are JSON-only, body-size-bounded, and
// origin-guarded; paths entered in the UI are stored as data, never executed.
import { createServer } from 'node:http';
import { readFileSync, realpathSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { Store, STAGES, defaultDataDir, isValidId, MAX_BODY_BYTES } from './store.mjs';
import { collectRuns } from './runs.mjs';
import { listActive } from './active.mjs';

export const HOST = '127.0.0.1';

const here = dirname(fileURLToPath(import.meta.url));
const PUBLIC = join(here, 'public');
const ASSETS = {
  '/': { file: 'index.html', type: 'text/html' },
  '/app.css': { file: 'app.css', type: 'text/css' },
  '/app.js': { file: 'app.js', type: 'text/javascript' },
  '/meerkat.png': { file: '../../assets/meerkat.png', type: 'image/png' },
};
const COLLECTIONS = ['workspaces', 'repositories', 'agents', 'tasks'];

function send(res, status, body, type, extra = {}) {
  res.writeHead(status, { 'Content-Type': `${type}; charset=utf-8`, 'Cache-Control': 'no-store', ...extra });
  res.end(body);
}

function sendJson(res, status, obj, extra = {}) {
  send(res, status, JSON.stringify(obj), 'application/json', extra);
}

function httpError(status, message) {
  const e = new Error(message);
  e.status = status;
  return e;
}

function methodNotAllowed(res, allow) {
  sendJson(res, 405, { ok: false, error: `method not allowed; use ${allow}` }, { Allow: allow });
}

/** Reads the request body, bounded to MAX_BODY_BYTES. */
function readBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    let tooLarge = false;
    req.on('data', (chunk) => {
      size += chunk.length;
      if (size > MAX_BODY_BYTES) {
        tooLarge = true;
        return;
      }
      chunks.push(chunk);
    });
    req.on('end', () => {
      if (tooLarge) {
        reject(httpError(413, 'request body too large'));
        return;
      }
      resolve(Buffer.concat(chunks).toString('utf8'));
    });
    req.on('error', reject);
  });
}

async function readJsonBody(req) {
  const type = String(req.headers['content-type'] || '').toLowerCase();
  if (!type.startsWith('application/json')) throw httpError(415, 'Content-Type must be application/json');
  const text = await readBody(req);
  if (text.trim() === '') throw httpError(400, 'request body must be JSON');
  let parsed;
  try {
    parsed = JSON.parse(text);
  } catch {
    throw httpError(400, 'invalid JSON body');
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw httpError(400, 'request body must be a JSON object');
  }
  return parsed;
}

/** Rejects writes that carry a cross-origin indicator. Read-only requests pass. */
function guardOrigin(req, server) {
  const origin = req.headers.origin;
  if (origin) {
    const addr = server.address();
    const port = addr && typeof addr === 'object' ? addr.port : null;
    const own = `http://${HOST}:${port}`;
    if (port !== null && origin !== own) throw httpError(403, 'cross-origin write rejected');
  }
  if (req.headers['sec-fetch-site'] === 'cross-site') throw httpError(403, 'cross-origin write rejected');
}

async function createResource(store, coll, body) {
  switch (coll) {
    case 'workspaces': return store.createWorkspace(body);
    case 'repositories': return store.createRepository(body);
    case 'agents': return store.createAgent(body);
    case 'tasks': return store.createTask(body);
    default: throw httpError(404, 'not found');
  }
}

async function updateResource(store, coll, id, body) {
  switch (coll) {
    case 'workspaces': return store.updateWorkspace(id, body);
    case 'repositories': return store.updateRepository(id, body);
    case 'agents': return store.updateAgent(id, body);
    case 'tasks': return store.updateTask(id, body);
    default: throw httpError(404, 'not found');
  }
}

async function handleApi(req, res, method, pathname, store, server) {
  const parts = pathname.slice('/api/'.length).split('/').filter(Boolean);

  if (parts.length === 1 && parts[0] === 'state') {
    if (method !== 'GET') return methodNotAllowed(res, 'GET');
    return sendJson(res, 200, { ok: true, stages: STAGES, data: store.state() });
  }

  if (parts.length === 1 && parts[0] === 'runs') {
    if (method !== 'GET') return methodNotAllowed(res, 'GET');
    return sendJson(res, 200, { ok: true, runs: collectRuns(store.list('tasks')) });
  }

  if (parts.length === 1 && parts[0] === 'active') {
    if (method !== 'GET') return methodNotAllowed(res, 'GET');
    const agents = await listActive(store.dir);
    return sendJson(res, 200, { ok: true, count: agents.length, agents });
  }

  if (parts.length < 1 || !COLLECTIONS.includes(parts[0])) {
    return sendJson(res, 404, { ok: false, error: 'not found' });
  }

  if (parts.length === 1) {
    const coll = parts[0];
    if (method === 'GET') return sendJson(res, 200, { ok: true, [coll]: store.list(coll) });
    if (method === 'POST') {
      guardOrigin(req, server);
      const body = await readJsonBody(req);
      const created = await createResource(store, coll, body);
      return sendJson(res, 201, { ok: true, data: created });
    }
    return methodNotAllowed(res, 'GET, POST');
  }

  if (parts.length === 2) {
    const [coll, id] = parts;
    if (!isValidId(id)) return sendJson(res, 404, { ok: false, error: 'not found' });
    const existing = store.get(coll, id);
    if (method === 'GET') {
      if (!existing) return sendJson(res, 404, { ok: false, error: 'not found' });
      return sendJson(res, 200, { ok: true, data: existing });
    }
    if (method === 'PUT') {
      guardOrigin(req, server);
      if (!existing) return sendJson(res, 404, { ok: false, error: 'not found' });
      const body = await readJsonBody(req);
      const updated = await updateResource(store, coll, id, body);
      return sendJson(res, 200, { ok: true, data: updated });
    }
    if (method === 'DELETE' && coll === 'tasks') {
      guardOrigin(req, server);
      store.deleteTask(id); // throws 404 when missing
      return sendJson(res, 200, { ok: true });
    }
    return methodNotAllowed(res, coll === 'tasks' ? 'GET, PUT, DELETE' : 'GET, PUT');
  }

  return sendJson(res, 404, { ok: false, error: 'not found' });
}

async function handle(req, res, store, server) {
  let url;
  try {
    url = new URL(req.url || '/', `http://${HOST}`);
  } catch {
    return send(res, 400, '400 Bad Request\n', 'text/plain');
  }
  const { pathname } = url;
  const method = req.method || 'GET';

  if (pathname.startsWith('/api/')) {
    return handleApi(req, res, method, pathname, store, server);
  }

  const asset = ASSETS[pathname];
  if (asset) {
    if (method !== 'GET') return methodNotAllowed(res, 'GET');
    try {
      return send(res, 200, readFileSync(join(PUBLIC, asset.file)), asset.type);
    } catch {
      return send(res, 404, '404 Not Found\n', 'text/plain');
    }
  }

  if (method !== 'GET') return methodNotAllowed(res, 'GET');
  return send(res, 404, '404 Not Found\n', 'text/plain');
}

/**
 * Creates the dashboard HTTP server. options.dataDir selects the private store
 * directory; when omitted, a private user data directory under the home dir is
 * used (never the repository). options.store may inject a prebuilt Store.
 */
export function createDashboard(options = {}) {
  const store = options.store instanceof Store ? options.store : new Store(options.dataDir || defaultDataDir());
  const server = createServer((req, res) => {
    handle(req, res, store, server).catch((err) => {
      const status = err.status || 500;
      if (status >= 500) console.error('dashboard error:', err);
      sendJson(res, status, { ok: false, error: status >= 500 ? 'internal error' : err.message });
    });
  });
  return server;
}

/** Starts the dashboard on the given port and host; resolves with the listening server. */
export async function start({ port = 0, host = HOST, dataDir } = {}) {
  const server = createDashboard({ dataDir });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, host, resolve);
  });
  return server;
}

/** Loopback URL of the bound server, e.g. http://127.0.0.1:PORT/ */
export function urlOf(server) {
  const { address, port } = server.address();
  return `http://${address}:${port}/`;
}

export async function main(argv = process.argv.slice(2)) {
  let port = 0;
  let dataDir;
  try {
    const { values } = parseArgs({
      args: argv,
      options: {
        port: { type: 'string', default: '0' },
        'data-dir': { type: 'string' },
        help: { type: 'boolean', short: 'h' },
      },
    });
    if (values.help) {
      console.log('usage: node dashboard/server.mjs [--port <0-65535>] [--data-dir <dir>]');
      return 0;
    }
    port = Number(values.port);
    if (!Number.isInteger(port) || port < 0 || port > 65535) throw new Error(`invalid --port: ${values.port}`);
    if (values['data-dir'] !== undefined) dataDir = values['data-dir'];
  } catch (e) {
    console.error(`dashboard: ${e.message}`);
    return 1;
  }
  let server;
  try {
    server = await start({ port, dataDir });
  } catch (e) {
    console.error(`dashboard: ${e.message}`);
    return 1;
  }
  console.log(`Meerkat dashboard: ${urlOf(server)}`);
  console.log(`Data directory: ${dataDir || defaultDataDir()}`);
  const shutdown = () => server.close(() => process.exit(0));
  process.on('SIGINT', shutdown);
  process.on('SIGTERM', shutdown);
  return 0;
}

const isMain = process.argv[1] && realpathSync(process.argv[1]) === realpathSync(fileURLToPath(import.meta.url));
if (isMain) { main().then((code) => { process.exitCode = code; }); }

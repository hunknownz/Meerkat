#!/usr/bin/env node
// Meerkat dashboard: a loopback-only local task/agent board over a small
// private JSON store. Node 22 built-in HTTP only: no dependencies, no external
// assets, no shell execution. API writes are JSON-only, body-size-bounded, and
// origin-guarded; paths entered in the UI are stored as data, never executed.
//
// Workflow API (/api/workflow*): a read-only snapshot of the local workflow
// core plus two bounded writes (stop one active run, future-run settings).
// Writes require this server's own literal loopback Host, the same-origin
// guards, and a per-process random session token (X-Meerkat-Token) that is
// only handed out by GET /api/workflow on the same Host. There is no API that
// starts runs, executes commands, or accepts config/profile paths.
import { createServer } from 'node:http';
import { randomBytes, timingSafeEqual } from 'node:crypto';
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
  '/ui.js': { file: 'ui.js', type: 'text/javascript' },
  '/meerkat.png': { file: '../../assets/meerkat.png', type: 'image/png' },
};
const COLLECTIONS = ['workspaces', 'repositories', 'agents', 'tasks'];
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const SLUG_RE = /^[a-z0-9][a-z0-9._-]{0,63}$/i;
const PROFILE_ID_RE = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
const ROLES = ['developer', 'reviewer', 'polisher'];
const WORKFLOW_BODY_BYTES = 8 * 1024;
const CORE_URL = new URL('../workflow/core.mjs', import.meta.url);

/** Strict CSP: only same-origin scripts/styles/fetches, no inline script, no framing. */
export const CSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; "
  + "base-uri 'none'; form-action 'none'; frame-ancestors 'none'";
const SECURITY_HEADERS = {
  'Content-Security-Policy': CSP,
  'X-Content-Type-Options': 'nosniff',
  'Referrer-Policy': 'no-referrer',
  'Cross-Origin-Resource-Policy': 'same-origin',
};

function send(res, status, body, type, extra = {}) {
  res.writeHead(status, { 'Content-Type': `${type}; charset=utf-8`, 'Cache-Control': 'no-store', ...SECURITY_HEADERS, ...extra });
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

/** Reads the request body, bounded to `limit` bytes (default MAX_BODY_BYTES). */
function readBody(req, limit = MAX_BODY_BYTES) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    let tooLarge = false;
    req.on('data', (chunk) => {
      size += chunk.length;
      if (size > limit) {
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

async function readJsonBody(req, limit) {
  const type = String(req.headers['content-type'] || '').toLowerCase();
  if (!type.startsWith('application/json')) throw httpError(415, 'Content-Type must be application/json');
  const text = await readBody(req, limit);
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

// ---------- workflow API ----------

/** Literal `host:port` values this server answers to (its own bound loopback address only). */
function ownHosts(server) {
  const addr = server.address();
  if (!addr || typeof addr !== 'object') return [];
  const host = addr.address.includes(':') ? `[${addr.address}]` : addr.address;
  return [`${host}:${addr.port}`.toLowerCase()];
}

/** Rejects requests whose Host is not this server's literal loopback host:port (DNS rebinding). */
function guardHost(req, server) {
  const host = String(req.headers.host || '').toLowerCase();
  if (!ownHosts(server).includes(host)) throw httpError(403, 'foreign Host rejected');
}

function guardToken(req, token) {
  const given = Buffer.from(String(req.headers['x-meerkat-token'] || ''), 'utf8');
  const want = Buffer.from(token, 'utf8');
  if (given.length !== want.length || !timingSafeEqual(given, want)) throw httpError(403, 'missing or invalid session token');
}

/** All workflow writes: own Host, same-origin indicators, and the session token. */
function guardWorkflowWrite(req, server, token) {
  guardHost(req, server);
  guardOrigin(req, server);
  if (req.headers['sec-fetch-site'] === 'same-site') throw httpError(403, 'cross-origin write rejected');
  guardToken(req, token);
}

/** Removes absolute paths and control characters from error text; bounded length. */
export function sanitizeError(message) {
  return String(message ?? '')
    .replace(/[\u0000-\u001f\u007f]+/g, ' ')
    .replace(/(?:[A-Za-z]:)?[\\/](?:[^\s'"`\\/]+[\\/])+[^\s'"`]*/g, '<path>')
    .trim()
    .slice(0, 240) || 'request failed';
}

function serviceError(err, op) {
  const status = Number(err?.status ?? err?.statusCode);
  if (Number.isInteger(status) && status >= 400 && status < 500) return httpError(status, sanitizeError(err.message));
  if (err?.status === 503) return err;
  const system = typeof err?.code === 'string' && /^E[A-Z]+$/.test(err.code);
  if (op === 'read' || system) {
    const e = httpError(503, op === 'read' ? 'workflow state unavailable' : 'workflow storage unavailable');
    e.detail = sanitizeError(err?.message);
    e.log = err;
    return e;
  }
  // Validation/conflict errors raised by the core for a well-formed request.
  return httpError(422, sanitizeError(err?.message));
}

/** Lazily loads the exported core functions; 503 (not empty data) when the core is not installed. */
export function lazyWorkflowService(url = CORE_URL) {
  let loaded = null;
  const load = async () => {
    if (loaded) return loaded;
    let mod;
    try {
      mod = await import(url.href);
    } catch (err) {
      if (err?.code === 'ERR_MODULE_NOT_FOUND' && String(err.message).includes(url.pathname.split('/').pop())) {
        throw httpError(503, 'workflow core not installed');
      }
      const e = httpError(503, 'workflow core failed to load');
      e.log = err;
      throw e;
    }
    for (const name of ['readWorkflow', 'requestStop', 'updateSettings']) {
      if (typeof mod[name] !== 'function') throw httpError(503, 'workflow core incompatible');
    }
    loaded = mod;
    return mod;
  };
  return {
    readWorkflow: async (dir) => (await load()).readWorkflow(dir),
    requestStop: async (dir, runId, requestId) => (await load()).requestStop(dir, runId, requestId),
    updateSettings: async (dir, input) => (await load()).updateSettings(dir, input),
  };
}

function onlyKeys(body, allowed) {
  for (const key of Object.keys(body)) {
    if (!allowed.includes(key)) throw httpError(400, `unknown field: ${key.slice(0, 40)}`);
  }
}

/** Validates PUT /api/workflow/settings; returns a fresh object with known fields only. */
export function validateSettingsInput(body) {
  onlyKeys(body, ['maxConcurrency', 'maxFixRounds', 'defaultProfiles']);
  const out = {};
  if (body.maxConcurrency !== undefined) {
    if (!Number.isInteger(body.maxConcurrency) || body.maxConcurrency < 1 || body.maxConcurrency > 4) {
      throw httpError(400, 'maxConcurrency must be an integer 1-4');
    }
    out.maxConcurrency = body.maxConcurrency;
  }
  if (body.maxFixRounds !== undefined) {
    if (!Number.isInteger(body.maxFixRounds) || body.maxFixRounds < 0 || body.maxFixRounds > 2) {
      throw httpError(400, 'maxFixRounds must be an integer 0-2');
    }
    out.maxFixRounds = body.maxFixRounds;
  }
  if (body.defaultProfiles !== undefined) {
    const dp = body.defaultProfiles;
    if (!dp || typeof dp !== 'object' || Array.isArray(dp)) throw httpError(400, 'defaultProfiles must be an object');
    const projects = Object.keys(dp);
    if (projects.length > 16) throw httpError(400, 'too many projects in defaultProfiles');
    out.defaultProfiles = {};
    for (const projectId of projects) {
      if (!SLUG_RE.test(projectId)) throw httpError(400, 'invalid project id in defaultProfiles');
      const map = dp[projectId];
      if (!map || typeof map !== 'object' || Array.isArray(map)) throw httpError(400, 'defaultProfiles entries must be role maps');
      out.defaultProfiles[projectId] = {};
      for (const [role, id] of Object.entries(map)) {
        if (!ROLES.includes(role)) throw httpError(400, 'invalid role in defaultProfiles');
        if (typeof id !== 'string' || !PROFILE_ID_RE.test(id)) throw httpError(400, 'profile must be a registered profile id');
        out.defaultProfiles[projectId][role] = id;
      }
    }
  }
  if (Object.keys(out).length === 0) throw httpError(400, 'no settings given');
  return out;
}

async function handleWorkflow(req, res, method, parts, ctx) {
  const { store, server, service, token } = ctx;
  if (parts.length === 1) {
    if (method !== 'GET') return methodNotAllowed(res, 'GET');
    guardHost(req, server); // the response carries the session token
    let data;
    try {
      data = await service.readWorkflow(store.dir);
    } catch (err) {
      throw serviceError(err, 'read');
    }
    if (!data || typeof data !== 'object' || Array.isArray(data) || data.schemaVersion !== 1) {
      throw httpError(503, 'workflow snapshot malformed');
    }
    const legacyActive = await listActive(store.dir);
    return sendJson(res, 200, { ok: true, data, legacyActive, sessionToken: token });
  }
  if (parts.length === 4 && parts[1] === 'runs' && parts[3] === 'stop') {
    if (method !== 'POST') return methodNotAllowed(res, 'POST');
    guardWorkflowWrite(req, server, token);
    const runId = parts[2];
    if (!UUID_RE.test(runId)) throw httpError(400, 'run id must be a UUID');
    const body = await readJsonBody(req, WORKFLOW_BODY_BYTES);
    onlyKeys(body, ['requestId']);
    if (typeof body.requestId !== 'string' || !UUID_RE.test(body.requestId)) throw httpError(400, 'requestId must be a UUID');
    let result;
    try {
      result = await service.requestStop(store.dir, runId.toLowerCase(), body.requestId.toLowerCase());
    } catch (err) {
      throw serviceError(err, 'write');
    }
    // A stop request is only acknowledged; the controller decides when the run actually stops.
    return sendJson(res, 202, { ok: true, accepted: true, runId: runId.toLowerCase(), requestId: body.requestId.toLowerCase(), data: result ?? null });
  }
  if (parts.length === 2 && parts[1] === 'settings') {
    if (method !== 'PUT') return methodNotAllowed(res, 'PUT');
    guardWorkflowWrite(req, server, token);
    const input = validateSettingsInput(await readJsonBody(req, WORKFLOW_BODY_BYTES));
    let result;
    try {
      result = await service.updateSettings(store.dir, input);
    } catch (err) {
      throw serviceError(err, 'write');
    }
    return sendJson(res, 200, { ok: true, data: result ?? null });
  }
  return sendJson(res, 404, { ok: false, error: 'not found' });
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

async function handleApi(req, res, method, pathname, ctx) {
  const { store, server } = ctx;
  const parts = pathname.slice('/api/'.length).split('/').filter(Boolean);

  if (parts[0] === 'workflow') return handleWorkflow(req, res, method, parts, ctx);

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

async function handle(req, res, ctx) {
  let url;
  try {
    url = new URL(req.url || '/', `http://${HOST}`);
  } catch {
    return send(res, 400, '400 Bad Request\n', 'text/plain');
  }
  const { pathname } = url;
  const method = req.method || 'GET';

  if (pathname.startsWith('/api/')) {
    return handleApi(req, res, method, pathname, ctx);
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
 * options.workflow may inject a workflow service
 * { readWorkflow(dir), requestStop(dir, runId, requestId), updateSettings(dir, input) };
 * by default the exported functions of ../workflow/core.mjs are loaded lazily.
 * options.sessionToken overrides the random per-process write token (tests only).
 */
export function createDashboard(options = {}) {
  const store = options.store instanceof Store ? options.store : new Store(options.dataDir || defaultDataDir());
  const service = options.workflow || lazyWorkflowService();
  const token = typeof options.sessionToken === 'string' && options.sessionToken.length >= 16
    ? options.sessionToken : randomBytes(32).toString('base64url');
  const server = createServer((req, res) => {
    handle(req, res, { store, server, service, token }).catch((err) => {
      const status = err.status || 500;
      if (status >= 500 && status !== 503) console.error('dashboard error:', err);
      else if (err.log) console.error('dashboard workflow error:', sanitizeError(err.log?.message));
      const body = { ok: false, error: status >= 500 && status !== 503 ? 'internal error' : err.message };
      if (err.detail) body.detail = err.detail;
      if (res.headersSent) { res.destroy(); return; }
      sendJson(res, status, body);
    });
  });
  return server;
}

/** Starts the dashboard on the given port and host; resolves with the listening server. */
export async function start({ port = 0, host = HOST, dataDir, workflow, sessionToken } = {}) {
  const server = createDashboard({ dataDir, workflow, sessionToken });
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

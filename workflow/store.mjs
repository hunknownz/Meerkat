// Private, durable workflow store for Meerkat. Node 22 built-ins only.
//
// Layout (all under <dataDir>/workflow, dirs 0700, files 0600, writes atomic):
//   state.json        authority (projects, contexts, tasks, runs, deliveries, reviews, profiles);
//                     written ONLY by the holder of the controller lease.
//   settings.json     future-run settings; written by updateSettings (frontend-safe, never touches state).
//   requests/         per-request command files (stop-<requestId>.json), written by requestStop.
//   requests/processed/  private receipts of handled stop commands (same name), kept for requestId idempotence;
//                     never listed as pending and never acted on again.
//   controller.lock/  exclusive controller lock directory; owner.json holds {token,pid,heartbeatAt}.
//
// A malformed state/settings file is never replaced: reads throw StoreCorruptError and nothing is written.
import { randomBytes, randomUUID } from 'node:crypto';
import {
  chmodSync, closeSync, existsSync, fsyncSync, lstatSync, mkdirSync, openSync, readFileSync, readdirSync,
  renameSync, rmSync, statSync, unlinkSync, writeSync,
} from 'node:fs';
import { hostname } from 'node:os';
import { join, resolve } from 'node:path';

export const SCHEMA_VERSION = 1;
export const COLLECTIONS = ['projects', 'contexts', 'tasks', 'runs', 'deliveries', 'reviews', 'profiles'];
export const DEFAULT_STALE_MS = 30_000;
export const MAX_RUN_EVENTS = 50;
export const MAX_STOP_REQUESTS = 128;
export const MAX_STOP_RECEIPTS = 1024;
const MAX_STATE_BYTES = 32 * 1024 * 1024;
const MAX_SMALL_FILE = 64 * 1024;
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

/** Allowed persisted fields per collection; anything else is dropped on write. */
export const FIELDS = {
  projects: ['id', 'name', 'repositories', 'createdAt', 'updatedAt'],
  contexts: ['id', 'projectId', 'version', 'digest', 'text', 'sources', 'createdAt'],
  tasks: ['id', 'projectId', 'repository', 'worktree', 'branch', 'title', 'goal', 'scope', 'acceptance', 'dependencies',
    'contextRef', 'profileIds', 'state', 'stateReason', 'resumeRole', 'candidateSha', 'baselineSha', 'createdAt',
    'updatedAt', 'issueRef', 'budget'],
  runs: ['id', 'agentId', 'taskId', 'role', 'profileId', 'modelSnapshot', 'contextRef', 'state', 'pid', 'startedAt',
    'endedAt', 'updatedAt', 'events', 'summary', 'usage'],
  deliveries: ['id', 'taskId', 'contextRef', 'candidateSha', 'repository', 'runIds', 'checks', 'knownGaps', 'state',
    'createdAt', 'updatedAt'],
  reviews: ['id', 'taskId', 'runId', 'candidateSha', 'contextDigest', 'verdict', 'findings', 'checks', 'createdAt'],
  // Internal profile snapshot. authEnv is an env var NAME only; values are never read. Not public.
  profiles: ['id', 'projectId', 'role', 'provider', 'model', 'authEnv', 'instructions', 'limits', 'piCommand',
    'configFile', 'configDigest', 'createdAt'],
};
// Nested keys that must never be persisted inside free-form objects (summary, usage, events, checks...).
const SECRET_KEY_RE = /^(api[_-]?key|secret|password|passwd|authorization|auth|credentials?|env|authEnv|cookie|transcript|prompt|stdout|stderr|rawError)$/i;

export class StoreError extends Error {}
export class StoreCorruptError extends StoreError {}
export class ControllerBusyError extends StoreError {}
export class ControllerLostError extends StoreError {}

export function emptyState() {
  return { schemaVersion: SCHEMA_VERSION, projects: [], contexts: [], tasks: [], runs: [], deliveries: [], reviews: [], profiles: [] };
}

export function ensurePrivateDir(dir) {
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  const st = lstatSync(dir);
  if (!st.isDirectory() || st.isSymbolicLink()) throw new StoreError('workflow path is not a private directory');
  if ((st.mode & 0o777) !== 0o700) chmodSync(dir, 0o700);
}

/** Atomic private write: temp file (0600, exclusive) + fsync + rename. */
export function writeAtomic(path, text) {
  const tmp = `${path}.${randomBytes(6).toString('hex')}.tmp`;
  const fd = openSync(tmp, 'wx', 0o600);
  try {
    writeSync(fd, text);
    fsyncSync(fd);
  } finally { closeSync(fd); }
  try { renameSync(tmp, path); } catch (e) { try { unlinkSync(tmp); } catch { /* ignore */ } throw e; }
  chmodSync(path, 0o600);
}

function readBounded(path, max) {
  const st = statSync(path);
  if (!st.isFile()) throw new StoreCorruptError(`${path} is not a regular file`);
  if (st.size > max) throw new StoreCorruptError(`${path} exceeds size limit`);
  return readFileSync(path, 'utf8');
}

function scrub(value, depth = 0) {
  if (depth > 8) return null;
  if (Array.isArray(value)) return value.slice(0, 500).map((v) => scrub(v, depth + 1));
  if (value && typeof value === 'object') {
    const out = {};
    for (const [k, v] of Object.entries(value)) if (!SECRET_KEY_RE.test(k)) out[k] = scrub(v, depth + 1);
    return out;
  }
  if (typeof value === 'string') return value.length > 20_000 ? value.slice(0, 20_000) : value;
  return value;
}

/** Keeps only allowed top-level fields; nested free-form objects are scrubbed of secret-like keys. */
export function sanitizeRecord(collection, rec) {
  const out = {};
  for (const k of FIELDS[collection]) {
    if (rec[k] === undefined) continue;
    out[k] = typeof rec[k] === 'object' && rec[k] !== null ? scrub(rec[k]) : rec[k];
  }
  if (collection === 'runs' && Array.isArray(out.events)) out.events = out.events.slice(-MAX_RUN_EVENTS);
  return out;
}

function validateState(s) {
  if (!s || typeof s !== 'object' || Array.isArray(s)) throw new StoreCorruptError('workflow state is not an object');
  if (s.schemaVersion !== SCHEMA_VERSION) throw new StoreCorruptError('workflow state has unsupported schemaVersion');
  for (const c of COLLECTIONS) {
    if (c === 'profiles' && s[c] === undefined) continue;
    if (!Array.isArray(s[c])) throw new StoreCorruptError(`workflow state ${c} is not an array`);
    const seen = new Set();
    for (const r of s[c]) {
      if (!r || typeof r !== 'object' || typeof r.id !== 'string' || !r.id) throw new StoreCorruptError(`workflow state ${c} has an invalid record`);
      // contexts are keyed by family id + version; other collections by id
      const key = c === 'contexts' ? `${r.id}@${r.version}` : r.id;
      if (seen.has(key)) throw new StoreCorruptError(`workflow state ${c} has duplicate id`);
      seen.add(key);
    }
  }
}

export class WorkflowStore {
  constructor(dataDir, { staleMs = DEFAULT_STALE_MS } = {}) {
    if (typeof dataDir !== 'string' || !dataDir) throw new StoreError('dataDir is required');
    this.dataDir = resolve(dataDir);
    this.dir = join(this.dataDir, 'workflow');
    this.statePath = join(this.dir, 'state.json');
    this.settingsPath = join(this.dir, 'settings.json');
    this.requestsDir = join(this.dir, 'requests');
    this.processedDir = join(this.requestsDir, 'processed');
    this.lockDir = join(this.dir, 'controller.lock');
    this.ownerPath = join(this.lockDir, 'owner.json');
    this.staleMs = staleMs;
    this.lease = null;
  }

  /** Fresh read from disk. Missing file => actual empty state. Malformed => StoreCorruptError (file untouched). */
  read() {
    if (!existsSync(this.statePath)) return emptyState();
    let s;
    try { s = JSON.parse(readBounded(this.statePath, MAX_STATE_BYTES)); } catch (e) {
      if (e instanceof StoreCorruptError) throw e;
      throw new StoreCorruptError('workflow state is not valid JSON; refusing to continue');
    }
    validateState(s);
    if (!s.profiles) s.profiles = [];
    return s;
  }

  /** Atomically replaces authority state. Requires this instance to hold a live controller lease. */
  write(state) {
    if (!this.lease) throw new ControllerLostError('write requires a held controller lease');
    this.assertHeld(this.lease);
    const out = { schemaVersion: SCHEMA_VERSION };
    for (const c of COLLECTIONS) out[c] = (state[c] ?? []).map((r) => sanitizeRecord(c, r));
    validateState(out);
    ensurePrivateDir(this.dir);
    writeAtomic(this.statePath, `${JSON.stringify(out, null, 2)}\n`);
    return out;
  }

  // ---------- controller lock ----------

  _readOwner() {
    try {
      const o = JSON.parse(readBounded(this.ownerPath, MAX_SMALL_FILE));
      return o && typeof o === 'object' ? o : null;
    } catch { return null; }
  }

  _lockAgeMs(owner) {
    const t = owner && Date.parse(owner.heartbeatAt);
    if (Number.isFinite(t)) return Date.now() - t;
    try { return Date.now() - statSync(this.lockDir).mtimeMs; } catch { return Infinity; }
  }

  /** {state:'running'|'idle'|'unknown', heartbeatAt, pid?}. Stale/unreadable lock => 'unknown'. */
  controllerStatus() {
    if (!existsSync(this.lockDir)) return { state: 'idle', heartbeatAt: null };
    const owner = this._readOwner();
    const heartbeatAt = owner && typeof owner.heartbeatAt === 'string' ? owner.heartbeatAt : null;
    if (owner && this._lockAgeMs(owner) <= this.staleMs) return { state: 'running', heartbeatAt };
    return { state: 'unknown', heartbeatAt };
  }

  /**
   * Acquires the single per-dataDir controller lease (exclusive mkdir + random token).
   * A live lease => ControllerBusyError. A lease whose heartbeat is older than staleMs is recovered
   * (by heartbeat age only; PIDs from stale files are never signaled).
   */
  acquireController({ heartbeatMs } = {}) {
    if (this.lease) throw new ControllerBusyError('this store already holds the controller lease');
    ensurePrivateDir(this.dir);
    for (let attempt = 0; attempt < 5; attempt += 1) {
      try {
        mkdirSync(this.lockDir, { mode: 0o700 });
      } catch (e) {
        if (e.code !== 'EEXIST') throw e;
        const owner = this._readOwner();
        if (this._lockAgeMs(owner) <= this.staleMs) {
          throw new ControllerBusyError('another controller holds the workflow lock');
        }
        this._recoverStale(owner);
        continue;
      }
      const now = new Date().toISOString();
      const lease = { token: randomUUID(), pid: process.pid, host: hostname(), acquiredAt: now, heartbeatAt: now, timer: null, lost: false };
      writeAtomic(this.ownerPath, JSON.stringify({ token: lease.token, pid: lease.pid, host: lease.host, acquiredAt: now, heartbeatAt: now }));
      this.lease = lease;
      if (heartbeatMs) this.startHeartbeat(lease, heartbeatMs);
      return lease;
    }
    throw new ControllerBusyError('could not acquire controller lock');
  }

  _recoverStale(observed) {
    const aside = `${this.lockDir}.stale-${randomBytes(6).toString('hex')}`;
    try { renameSync(this.lockDir, aside); } catch (e) { if (e.code === 'ENOENT') return; throw e; }
    let moved = null;
    try { moved = JSON.parse(readFileSync(join(aside, 'owner.json'), 'utf8')); } catch { /* none */ }
    const sameLock = (observed?.token ?? null) === (moved?.token ?? null);
    if (!sameLock) {
      // We raced with a fresh acquirer; put its lock back if nobody took the slot.
      try { renameSync(aside, this.lockDir); return; } catch { /* the displaced owner will detect loss */ }
    }
    rmSync(aside, { recursive: true, force: true });
  }

  /** Throws ControllerLostError unless the on-disk lock still carries this lease's token. */
  assertHeld(lease) {
    const owner = this._readOwner();
    if (!lease || lease.lost || !owner || owner.token !== lease.token) {
      if (lease) lease.lost = true;
      throw new ControllerLostError('controller lease is no longer held');
    }
    return owner;
  }

  heartbeat(lease) {
    const owner = this.assertHeld(lease);
    lease.heartbeatAt = new Date().toISOString();
    writeAtomic(this.ownerPath, JSON.stringify({ ...owner, heartbeatAt: lease.heartbeatAt }));
    return lease.heartbeatAt;
  }

  startHeartbeat(lease, intervalMs = Math.max(1000, Math.floor(this.staleMs / 3)), onLost = () => {}) {
    if (lease.timer) clearInterval(lease.timer);
    lease.timer = setInterval(() => {
      try { this.heartbeat(lease); } catch { clearInterval(lease.timer); lease.timer = null; onLost(lease); }
    }, intervalMs);
    lease.timer.unref();
  }

  /** Releases only if the lock still belongs to this lease; never removes another controller's lock. */
  releaseController(lease) {
    if (!lease) return false;
    if (lease.timer) { clearInterval(lease.timer); lease.timer = null; }
    if (this.lease === lease) this.lease = null;
    const owner = this._readOwner();
    if (!owner || owner.token !== lease.token) { lease.lost = true; return false; }
    rmSync(this.lockDir, { recursive: true, force: true });
    lease.lost = true;
    return true;
  }

  // ---------- settings (own file; no lease required) ----------

  readSettings() {
    if (!existsSync(this.settingsPath)) return null;
    try {
      const s = JSON.parse(readBounded(this.settingsPath, MAX_SMALL_FILE));
      if (!s || typeof s !== 'object' || Array.isArray(s)) throw new Error('bad');
      return s;
    } catch { throw new StoreCorruptError('workflow settings are malformed; refusing to continue'); }
  }

  writeSettings(settings) {
    ensurePrivateDir(this.dir);
    writeAtomic(this.settingsPath, `${JSON.stringify(settings, null, 2)}\n`);
  }

  // ---------- stop requests (own files; no lease required) ----------

  _requestPath(requestId) {
    if (!UUID_RE.test(requestId)) throw new StoreError('requestId must be a lowercase UUID');
    return join(this.requestsDir, `stop-${requestId}.json`);
  }

  _receiptPath(requestId) {
    if (!UUID_RE.test(requestId)) throw new StoreError('requestId must be a lowercase UUID');
    return join(this.processedDir, `stop-${requestId}.json`);
  }

  _readSmall(p) {
    if (!existsSync(p)) return null;
    try { return JSON.parse(readBounded(p, 4096)); } catch { throw new StoreCorruptError('stop request is malformed'); }
  }

  /** Pending request or processed receipt for this requestId (null if never seen). */
  readStopRequest(requestId) {
    return this._readSmall(this._requestPath(requestId)) ?? this._readSmall(this._receiptPath(requestId));
  }

  /** Processed receipt only (the controller never acts on these). */
  readStopReceipt(requestId) {
    return this._readSmall(this._receiptPath(requestId));
  }

  /**
   * Exclusive create (0600). Returns {created:false, existing} if the id already exists as a pending request
   * or a processed receipt. The pending cap counts pending requests only.
   */
  createStopRequest(req) {
    ensurePrivateDir(this.requestsDir);
    const p = this._requestPath(req.requestId);
    const receipt = this.readStopReceipt(req.requestId);
    if (receipt) return { created: false, existing: receipt };
    const count = readdirSync(this.requestsDir).filter((f) => /^stop-.*\.json$/.test(f)).length;
    if (!existsSync(p) && count >= MAX_STOP_REQUESTS) throw new StoreError('too many pending stop requests');
    let fd;
    try { fd = openSync(p, 'wx', 0o600); } catch (e) {
      if (e.code === 'EEXIST') return { created: false, existing: this.readStopRequest(req.requestId) };
      throw e;
    }
    try { writeSync(fd, JSON.stringify(req)); fsyncSync(fd); } finally { closeSync(fd); }
    return { created: true, existing: req };
  }

  /** For the controller: pending, well-formed stop requests only (processed receipts and malformed files are skipped). */
  listStopRequests() {
    if (!existsSync(this.requestsDir)) return [];
    const out = [];
    for (const f of readdirSync(this.requestsDir)) {
      const m = /^stop-([0-9a-f-]{36})\.json$/.exec(f);
      if (!m || !UUID_RE.test(m[1])) continue;
      try { const r = this._readSmall(this._requestPath(m[1])); if (r && r.requestId === m[1] && UUID_RE.test(r.runId)) out.push(r); } catch { /* skip */ }
    }
    return out;
  }

  removeStopRequest(requestId) {
    try { unlinkSync(this._requestPath(requestId)); return true; } catch (e) { if (e.code === 'ENOENT') return false; throw e; }
  }

  /**
   * Moves a handled pending request to a private processed receipt (atomic rename), so the same requestId keeps
   * returning its original acknowledgement while no longer counting toward the pending cap. Oldest receipts
   * beyond MAX_STOP_RECEIPTS are pruned. Returns false if the pending request no longer exists.
   */
  markStopRequestProcessed(requestId) {
    const from = this._requestPath(requestId);
    ensurePrivateDir(this.processedDir);
    try { renameSync(from, this._receiptPath(requestId)); } catch (e) { if (e.code === 'ENOENT') return false; throw e; }
    try {
      const files = readdirSync(this.processedDir).filter((f) => /^stop-[0-9a-f-]{36}\.json$/.test(f));
      if (files.length > MAX_STOP_RECEIPTS) {
        const aged = files.map((f) => { try { return [f, statSync(join(this.processedDir, f)).mtimeMs]; } catch { return [f, 0]; } })
          .sort((a, b) => a[1] - b[1]);
        for (const [f] of aged.slice(0, files.length - MAX_STOP_RECEIPTS)) { try { unlinkSync(join(this.processedDir, f)); } catch { /* ignore */ } }
      }
    } catch { /* pruning is best effort */ }
    return true;
  }
}

// Meerkat workflow core: task preparation, public snapshot, stop requests and settings.
// Node 22 built-ins only. Execution (executeTasks) is intentionally NOT implemented here yet.
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { existsSync, realpathSync, statSync } from 'node:fs';
import { isAbsolute, posix, resolve } from 'node:path';
import { loadConfig, preflight, ROLES } from '../scripts/run.mjs';
import { ControllerBusyError, StoreError, WorkflowStore } from './store.mjs';

export { ROLES };
export const DEFAULT_SETTINGS = Object.freeze({ maxConcurrency: 2, maxFixRounds: 2 });
export const DEFAULT_BUDGET = Object.freeze({ maxTokens: 500_000, maxWallSeconds: 1800, maxFixRounds: 2 });
/** Run states that mean "a process may be live"; projected to 'unknown' without a live controller. */
export const ACTIVE_RUN_STATES = new Set(['starting', 'running', 'stopping']);
/** Task states that mean "in progress under a controller". */
export const ACTIVE_TASK_STATES = new Set(['implementing', 'checking', 'polishing', 'rechecking', 'fixing']);

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const SLUG_RE = /^[a-z0-9][a-z0-9-]{0,62}$/;
const HASH_RE = /^(?:sha256:)?[0-9a-f]{64}$/;
const SECRET_SEGMENT_RE = /^(?:\.env(?:\..*)?|\.git|\.pi-developer|\.ssh|\.aws|\.gnupg|\.npmrc|\.pypirc|\.netrc|id_rsa.*|id_ed25519.*|.*\.pem|.*\.key|.*\.p12|.*\.pfx|credentials(?:\..*)?|secrets?(?:\..*)?)$/i;

export class WorkflowInputError extends Error {}

const fail = (msg) => { throw new WorkflowInputError(msg); };
const now = () => new Date().toISOString();
const sha256 = (s) => createHash('sha256').update(s).digest('hex');
const isObj = (v) => v !== null && typeof v === 'object' && !Array.isArray(v);
const hasCtl = (s) => /[\u0000-\u001f\u007f]/.test(s);

function onlyKeys(obj, allowed, where) {
  if (!isObj(obj)) fail(`${where} must be an object`);
  const extra = Object.keys(obj).filter((k) => !allowed.includes(k));
  if (extra.length) fail(`${where} has unknown field(s): ${extra.map((k) => k.slice(0, 40)).join(', ')}`);
}

function str(v, where, { min = 1, max = 200, multiline = false } = {}) {
  if (typeof v !== 'string') fail(`${where} must be a string`);
  const t = multiline ? v : v.trim();
  if (t.length < min || t.length > max) fail(`${where} must be ${min}..${max} characters`);
  if (!multiline && hasCtl(t)) fail(`${where} must not contain control characters`);
  return t;
}

function strList(v, where, { max = 50, itemMax = 500, min = 0 } = {}) {
  if (v === undefined) v = [];
  if (!Array.isArray(v) || v.length > max || v.length < min) fail(`${where} must be an array of ${min}..${max} strings`);
  return v.map((x, i) => str(x, `${where}[${i}]`, { max: itemMax }));
}

function intIn(v, where, lo, hi) {
  if (!Number.isInteger(v) || v < lo || v > hi) fail(`${where} must be an integer ${lo}..${hi}`);
  return v;
}

const canonical = (v) => (Array.isArray(v) ? `[${v.map(canonical).join(',')}]`
  : isObj(v) ? `{${Object.keys(v).sort().map((k) => `${JSON.stringify(k)}:${canonical(v[k])}`).join(',')}}`
    : JSON.stringify(v));

function git(cwd, args) {
  const r = spawnSync('git', args, { cwd, encoding: 'utf8' });
  return r.status === 0 ? r.stdout.trim() : null;
}

// ---------- validation helpers ----------

/** Normalizes explicit relative scope paths; rejects traversal, absolute, globs, .git and secret-like paths. */
export function normalizeScope(scope) {
  if (!Array.isArray(scope) || scope.length < 1 || scope.length > 50) fail('scope must list 1..50 relative paths');
  const out = new Set();
  for (const [i, raw] of scope.entries()) {
    const p = str(raw, `scope[${i}]`, { max: 300 });
    if (p.includes('\\') || /[*?[\]{}]/.test(p)) fail(`scope[${i}] must be an explicit path (no globs or backslashes)`);
    if (p.startsWith('/') || isAbsolute(p) || /^[A-Za-z]:/.test(p)) fail(`scope[${i}] must be relative`);
    const segs = p.split('/').filter((s) => s !== '' && s !== '.');
    if (!segs.length) fail(`scope[${i}] must name a path inside the repository`);
    if (segs.includes('..')) fail(`scope[${i}] must not traverse outside the repository`);
    const bad = segs.find((s) => SECRET_SEGMENT_RE.test(s));
    if (bad) fail(`scope[${i}] names an unsafe path segment`);
    out.add(posix.normalize(segs.join('/')));
  }
  return [...out].sort();
}

export function validateIssueRef(ref) {
  if (ref === undefined || ref === null) return undefined;
  onlyKeys(ref, ['url', 'title', 'updatedAt', 'bodyHash'], 'issueRef');
  const raw = str(ref.url, 'issueRef.url', { max: 500 });
  let u;
  try { u = new URL(raw); } catch { fail('issueRef.url is not a URL'); }
  if (u.protocol !== 'https:' || u.username || u.password || u.search || u.hash || u.port) {
    fail('issueRef.url must be https without credentials, port, query or fragment');
  }
  if (!/^\/[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+\/issues\/[1-9][0-9]{0,9}$/.test(u.pathname)) {
    fail('issueRef.url must look like https://host/owner/repo/issues/<number>');
  }
  const out = { url: `https://${u.hostname}${u.pathname}`, title: str(ref.title, 'issueRef.title', { max: 300 }) };
  if (ref.updatedAt !== undefined) {
    if (typeof ref.updatedAt !== 'string' || !Number.isFinite(Date.parse(ref.updatedAt))) fail('issueRef.updatedAt must be an ISO timestamp');
    out.updatedAt = new Date(ref.updatedAt).toISOString();
  }
  if (ref.bodyHash !== undefined) {
    if (typeof ref.bodyHash !== 'string' || !HASH_RE.test(ref.bodyHash)) fail('issueRef.bodyHash must be a sha256 hex digest');
    out.bodyHash = ref.bodyHash;
  }
  return out;
}

function validateBudget(b) {
  if (b === undefined) return { ...DEFAULT_BUDGET };
  onlyKeys(b, ['maxTokens', 'maxWallSeconds', 'maxFixRounds'], 'budget');
  return {
    maxTokens: b.maxTokens === undefined ? DEFAULT_BUDGET.maxTokens : intIn(b.maxTokens, 'budget.maxTokens', 1, 10_000_000),
    maxWallSeconds: b.maxWallSeconds === undefined ? DEFAULT_BUDGET.maxWallSeconds : intIn(b.maxWallSeconds, 'budget.maxWallSeconds', 1, 86_400),
    maxFixRounds: b.maxFixRounds === undefined ? DEFAULT_BUDGET.maxFixRounds : intIn(b.maxFixRounds, 'budget.maxFixRounds', 0, 2),
  };
}

function validateSources(sources) {
  if (sources === undefined) return [];
  if (!Array.isArray(sources) || sources.length > 50) fail('context.sources must be an array of at most 50 entries');
  return sources.map((s, i) => {
    onlyKeys(s, ['url', 'title', 'hash'], `context.sources[${i}]`);
    const out = {};
    if (s.url !== undefined) {
      const raw = str(s.url, `context.sources[${i}].url`, { max: 500 });
      let u;
      try { u = new URL(raw); } catch { fail(`context.sources[${i}].url is not a URL`); }
      if (u.protocol !== 'https:' || u.username || u.password) fail(`context.sources[${i}].url must be https without credentials`);
      out.url = `https://${u.host}${u.pathname}`; // query/fragment dropped: they commonly carry tokens
    }
    if (s.title !== undefined) out.title = str(s.title, `context.sources[${i}].title`, { max: 300 });
    if (s.hash !== undefined) {
      if (typeof s.hash !== 'string' || !HASH_RE.test(s.hash)) fail(`context.sources[${i}].hash must be a sha256 hex digest`);
      out.hash = s.hash;
    }
    if (!Object.keys(out).length) fail(`context.sources[${i}] is empty`);
    return out;
  });
}

function validateContextInput(c) {
  onlyKeys(c, ['id', 'version', 'text', 'sources'], 'context');
  if (c.id !== undefined && (typeof c.id !== 'string' || !UUID_RE.test(c.id))) fail('context.id must be a lowercase UUID');
  const version = intIn(c.version, 'context.version', 1, 10_000);
  const text = str(c.text, 'context.text', { max: 200_000, multiline: true });
  if (!text.trim()) fail('context.text must not be blank');
  const sources = validateSources(c.sources);
  const digest = `sha256:${sha256(canonical({ text, sources }))}`;
  return { id: c.id, version, text, sources, digest };
}

/**
 * Context family resolution:
 *  - explicit id: same id+version must have the same digest (immutable); a new version must stay in the same project.
 *  - no id: reuse a context with the same project+version+digest, else start a new family (new UUID).
 * Returns { contextRef, record (only when new) }.
 */
export function resolveContext(state, projectId, ctx) {
  if (ctx.id) {
    const family = state.contexts.filter((c) => c.id === ctx.id);
    if (family.some((c) => c.projectId !== projectId)) fail('context.id belongs to a different project');
    const same = family.find((c) => c.version === ctx.version);
    if (same) {
      if (same.digest !== ctx.digest) fail(`context ${ctx.id} version ${ctx.version} already exists with different content; use a new version`);
      return { contextRef: { id: same.id, version: same.version, digest: same.digest } };
    }
    return { contextRef: { id: ctx.id, version: ctx.version, digest: ctx.digest }, record: { id: ctx.id, projectId, ...pickCtx(ctx), createdAt: now() } };
  }
  const dup = state.contexts.find((c) => c.projectId === projectId && c.version === ctx.version && c.digest === ctx.digest);
  if (dup) return { contextRef: { id: dup.id, version: dup.version, digest: dup.digest } };
  const id = randomUUID();
  return { contextRef: { id, version: ctx.version, digest: ctx.digest }, record: { id, projectId, ...pickCtx(ctx), createdAt: now() } };
}
const pickCtx = (c) => ({ version: c.version, digest: c.digest, text: c.text, sources: c.sources });

function realDir(p, where) {
  if (typeof p !== 'string' || !isAbsolute(p)) fail(`${where} must be an absolute path`);
  if (!existsSync(p) || !statSync(p).isDirectory()) fail(`${where} does not exist`);
  return realpathSync(p);
}

/** Repository + linked worktree checks: both Git roots, same common dir, worktree linked (not primary). */
export function validateRepositoryPair(repository, worktree) {
  const repo = realDir(repository, 'repository');
  const wt = realDir(worktree, 'worktree');
  if (repo === wt) fail('worktree must be a linked worktree distinct from repository');
  const top = (d) => { const t = git(d, ['rev-parse', '--show-toplevel']); return t && realpathSync(t); };
  if (top(repo) !== repo) fail('repository must be a Git root');
  if (top(wt) !== wt) fail('worktree must be a Git worktree root');
  const common = (d) => { const c = git(d, ['rev-parse', '--git-common-dir']); return c && realpathSync(resolve(d, c)); };
  if (!common(repo) || common(repo) !== common(wt)) fail('repository and worktree must share the same Git common directory');
  return { repository: repo, worktree: wt };
}

/** Frozen, allowed-fields-only profile snapshot from runner.loadConfig. */
export function freezeProfile(configFile, role, projectId) {
  if (typeof configFile !== 'string' || !isAbsolute(configFile)) fail(`profiles.${role} must be an absolute config path`);
  let c;
  try { c = loadConfig(configFile); } catch (e) { fail(`profiles.${role}: ${e instanceof SyntaxError ? 'config is not valid JSON' : e.message}`); }
  if (c.projectId !== projectId) fail(`profiles.${role}: config projectId does not match project.id`);
  if (!Array.isArray(c.instructions) || !c.instructions.every((x) => typeof x === 'string' && x && !isAbsolute(x) && !x.split('/').includes('..'))) {
    fail(`profiles.${role}: instructions must be relative paths`);
  }
  if (!Array.isArray(c.piCommand) || !c.piCommand.length || !c.piCommand.every((x) => typeof x === 'string' && x)) fail(`profiles.${role}: piCommand must be a non-empty string array`);
  const limits = {};
  for (const k of ['maxWallSeconds', 'maxTokens']) {
    const v = c.limits[k];
    if (!Number.isFinite(v) || v <= 0) fail(`profiles.${role}: limits.${k} must be a positive number`);
    limits[k] = v;
  }
  const frozen = {
    projectId: c.projectId, provider: c.provider, model: c.model, authEnv: c.authEnv,
    instructions: [...c.instructions], limits, piCommand: [...c.piCommand],
  };
  const configDigest = `sha256:${sha256(canonical(frozen))}`;
  return { ...frozen, role, configFile: realpathSync(configFile), configDigest, config: c };
}

// ---------- prepareTask ----------

const PREPARE_KEYS = ['project', 'repository', 'worktree', 'title', 'goal', 'scope', 'acceptance', 'context', 'profiles',
  'dependencies', 'issueRef', 'budget'];

/**
 * Validates a trusted prepare input and records a new ready Task. Requires the controller lock
 * (pass options.store holding a lease, or the call acquires/releases one; a live foreign controller => ControllerBusyError).
 * Returns the stored Task record.
 */
export async function prepareTask(dataDir, input, { store = null } = {}) {
  onlyKeys(input, PREPARE_KEYS, 'input');
  onlyKeys(input.project, ['id', 'name'], 'project');
  const projectId = str(input.project.id, 'project.id', { max: 63 });
  if (!SLUG_RE.test(projectId)) fail('project.id must be a lowercase slug');
  const projectName = str(input.project.name, 'project.name', { max: 120 });
  const title = str(input.title, 'title', { max: 200 });
  const goal = str(input.goal, 'goal', { max: 4000, multiline: true });
  const scope = normalizeScope(input.scope);
  const acceptance = strList(input.acceptance, 'acceptance', { min: 1, max: 50, itemMax: 500 });
  const dependencies = input.dependencies === undefined ? [] : input.dependencies;
  if (!Array.isArray(dependencies) || dependencies.length > 50 || !dependencies.every((d) => typeof d === 'string' && UUID_RE.test(d))) {
    fail('dependencies must be an array of task UUIDs');
  }
  const issueRef = validateIssueRef(input.issueRef);
  const budget = validateBudget(input.budget);
  if (!isObj(input.context)) fail('context is required');
  const ctx = validateContextInput(input.context);
  onlyKeys(input.profiles, ROLES, 'profiles');
  const { repository, worktree } = validateRepositoryPair(input.repository, input.worktree);

  const frozen = {};
  let pre = null;
  for (const role of ROLES) {
    if (input.profiles[role] === undefined) fail(`profiles.${role} is required`);
    frozen[role] = freezeProfile(input.profiles[role], role, projectId);
    try {
      // taskPath: the worktree itself; the brief is generated per run by the executor.
      const p = preflight({ config: frozen[role].config, worktree, taskPath: worktree });
      if (pre && (p.baseline !== pre.baseline || p.branch !== pre.branch)) fail('worktree changed during preparation');
      pre = p;
    } catch (e) { if (e instanceof WorkflowInputError) throw e; fail(`preflight (${role}): ${e.message}`); }
  }

  const ws = store ?? new WorkflowStore(dataDir);
  const ownLease = !store;
  const lease = ownLease ? ws.acquireController() : ws.lease;
  if (!lease) throw new ControllerBusyError('store has no controller lease');
  try {
    const state = ws.read();
    const deps = new Set(dependencies);
    for (const d of deps) {
      const t = state.tasks.find((x) => x.id === d);
      if (!t) fail(`dependency ${d} does not exist`);
      if (t.projectId !== projectId) fail(`dependency ${d} belongs to a different project`);
    }
    const { contextRef, record } = resolveContext(state, projectId, ctx);
    if (record) state.contexts.push(record);

    const ts = now();
    let project = state.projects.find((p) => p.id === projectId);
    if (!project) { project = { id: projectId, name: projectName, repositories: [], createdAt: ts, updatedAt: ts }; state.projects.push(project); }
    if (!project.repositories.includes(repository)) { project.repositories.push(repository); project.updatedAt = ts; }

    const profileIds = {};
    for (const role of ROLES) {
      const f = frozen[role];
      let prof = state.profiles.find((p) => p.projectId === projectId && p.role === role && p.configDigest === f.configDigest && p.configFile === f.configFile);
      if (!prof) {
        let id = `${projectId}.${role}.${f.configDigest.slice(7, 19)}`;
        if (state.profiles.some((p) => p.id === id)) id = `${projectId}.${role}.${randomUUID().slice(0, 8)}`;
        const { config: _c, ...rest } = f;
        prof = { id, ...rest, createdAt: ts };
        state.profiles.push(prof);
      }
      profileIds[role] = prof.id;
    }

    const task = {
      id: randomUUID(), projectId, repository, worktree, branch: pre.branch, title, goal, scope, acceptance,
      dependencies: [...deps], contextRef, profileIds, state: 'ready', resumeRole: null, candidateSha: null,
      baselineSha: pre.baseline, createdAt: ts, updatedAt: ts, budget,
    };
    if (issueRef) task.issueRef = issueRef;
    state.tasks.push(task);
    const saved = ws.write(state);
    return saved.tasks.find((t) => t.id === task.id);
  } finally {
    if (ownLease) ws.releaseController(lease);
  }
}

// ---------- public snapshot ----------

const pick = (o, keys) => Object.fromEntries(keys.filter((k) => o[k] !== undefined).map((k) => [k, o[k]]));
const PUBLIC = {
  projects: ['id', 'name', 'repositories', 'createdAt', 'updatedAt'],
  contexts: ['id', 'projectId', 'version', 'digest', 'text', 'sources', 'createdAt'],
  tasks: ['id', 'projectId', 'repository', 'worktree', 'branch', 'title', 'goal', 'scope', 'acceptance', 'dependencies',
    'contextRef', 'profileIds', 'state', 'stateReason', 'resumeRole', 'candidateSha', 'baselineSha', 'createdAt', 'updatedAt',
    'issueRef', 'budget'],
  runs: ['id', 'agentId', 'taskId', 'role', 'profileId', 'modelSnapshot', 'contextRef', 'state', 'startedAt', 'endedAt',
    'updatedAt', 'events', 'summary', 'usage'],
  deliveries: ['id', 'taskId', 'contextRef', 'candidateSha', 'repository', 'runIds', 'checks', 'knownGaps', 'state', 'createdAt', 'updatedAt'],
  reviews: ['id', 'taskId', 'runId', 'candidateSha', 'contextDigest', 'verdict', 'findings', 'checks', 'createdAt'],
};

function effectiveSettings(raw) {
  const s = { ...DEFAULT_SETTINGS, defaultProfiles: {} };
  if (!raw) return s;
  if (Number.isInteger(raw.maxConcurrency) && raw.maxConcurrency >= 1 && raw.maxConcurrency <= 4) s.maxConcurrency = raw.maxConcurrency;
  if (Number.isInteger(raw.maxFixRounds) && raw.maxFixRounds >= 0 && raw.maxFixRounds <= 2) s.maxFixRounds = raw.maxFixRounds;
  if (isObj(raw.defaultProfiles)) s.defaultProfiles = raw.defaultProfiles;
  if (typeof raw.updatedAt === 'string') s.updatedAt = raw.updatedAt;
  return s;
}

/** Live, secret-free snapshot read fresh from disk. Missing store => truthful empty state; corrupt => throws. */
export async function readWorkflow(dataDir, { staleMs } = {}) {
  const ws = new WorkflowStore(dataDir, staleMs ? { staleMs } : {});
  const state = ws.read();
  const settings = effectiveSettings(ws.readSettings());
  const controller = ws.controllerStatus();
  const live = controller.state === 'running';
  const runs = state.runs.map((r) => {
    const out = pick(r, PUBLIC.runs);
    if (!live && ACTIVE_RUN_STATES.has(r.state)) { out.recordedState = r.state; out.state = 'unknown'; }
    return out;
  });
  const tasks = state.tasks.map((t) => {
    const out = pick(t, PUBLIC.tasks);
    if (!live && ACTIVE_TASK_STATES.has(t.state)) { out.recordedState = t.state; out.state = 'unknown'; }
    out.worktreeExists = typeof t.worktree === 'string' && existsSync(t.worktree);
    return out;
  });
  const profiles = state.profiles.map((p) => ({
    id: p.id, projectId: p.projectId, role: p.role, provider: p.provider, model: p.model,
    limits: pick(p.limits ?? {}, ['maxWallSeconds', 'maxTokens']),
  }));
  return {
    schemaVersion: 1,
    observedAt: now(),
    controller: { state: controller.state, heartbeatAt: controller.heartbeatAt },
    projects: state.projects.map((p) => pick(p, PUBLIC.projects)),
    contexts: state.contexts.map((c) => pick(c, PUBLIC.contexts)),
    tasks,
    runs,
    deliveries: state.deliveries.map((d) => pick(d, PUBLIC.deliveries)),
    reviews: state.reviews.map((r) => pick(r, PUBLIC.reviews)),
    profiles,
    settings,
    counts: {
      running: runs.filter((r) => r.state === 'running').length,
      queued: tasks.filter((t) => t.state === 'queued').length,
      unknown: runs.filter((r) => r.state === 'unknown').length,
    },
  };
}

// ---------- stop requests ----------

/**
 * Writes a per-request stop command for a currently active run (live controller required).
 * Idempotent per requestId; reusing a requestId for a different run is rejected. Never signals processes.
 */
export async function requestStop(dataDir, runId, requestId) {
  if (typeof runId !== 'string' || !UUID_RE.test(runId)) fail('runId must be a lowercase UUID');
  if (typeof requestId !== 'string' || !UUID_RE.test(requestId)) fail('requestId must be a lowercase UUID');
  const ws = new WorkflowStore(dataDir);
  const prior = ws.readStopRequest(requestId);
  if (prior) {
    if (prior.runId !== runId) fail('requestId already used for a different run');
    return { requestId, runId, createdAt: prior.createdAt, duplicate: true };
  }
  const state = ws.read();
  const run = state.runs.find((r) => r.id === runId);
  if (!run) fail('run does not exist');
  if (!ACTIVE_RUN_STATES.has(run.state)) fail('run is not active');
  if (ws.controllerStatus().state !== 'running') fail('no live controller owns this run; its state is unknown');
  const req = { type: 'stop', requestId, runId, createdAt: now() };
  const r = ws.createStopRequest(req);
  if (!r.created) {
    if (r.existing?.runId !== runId) fail('requestId already used for a different run');
    return { requestId, runId, createdAt: r.existing.createdAt, duplicate: true };
  }
  return { requestId, runId, createdAt: req.createdAt, duplicate: false };
}

// ---------- settings ----------

/** Updates future-run settings in settings.json (never state.json). Returns effective settings. */
export async function updateSettings(dataDir, input) {
  onlyKeys(input, ['maxConcurrency', 'maxFixRounds', 'defaultProfiles'], 'settings');
  const ws = new WorkflowStore(dataDir);
  const current = effectiveSettings(ws.readSettings());
  const next = { maxConcurrency: current.maxConcurrency, maxFixRounds: current.maxFixRounds, defaultProfiles: current.defaultProfiles };
  if (input.maxConcurrency !== undefined) next.maxConcurrency = intIn(input.maxConcurrency, 'maxConcurrency', 1, 4);
  if (input.maxFixRounds !== undefined) next.maxFixRounds = intIn(input.maxFixRounds, 'maxFixRounds', 0, 2);
  if (input.defaultProfiles !== undefined) {
    if (!isObj(input.defaultProfiles)) fail('defaultProfiles must be an object');
    const state = ws.read();
    const merged = { ...current.defaultProfiles };
    for (const [projectId, roles] of Object.entries(input.defaultProfiles)) {
      if (!state.projects.some((p) => p.id === projectId)) fail('defaultProfiles references an unknown project');
      onlyKeys(roles, ROLES, `defaultProfiles.${projectId.slice(0, 63)}`);
      const map = { ...(merged[projectId] ?? {}) };
      for (const [role, profileId] of Object.entries(roles)) {
        const prof = state.profiles.find((p) => p.id === profileId);
        if (!prof || prof.projectId !== projectId || prof.role !== role) fail(`defaultProfiles.${projectId}.${role} must be a registered ${role} profile of the same project`);
        map[role] = profileId;
      }
      merged[projectId] = map;
    }
    next.defaultProfiles = merged;
  }
  next.updatedAt = now();
  ws.writeSettings(next);
  return effectiveSettings(next);
}

export { StoreError };

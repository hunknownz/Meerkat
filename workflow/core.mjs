// Meerkat workflow core: task preparation, local execution pipeline (executeTasks), public snapshot,
// stop requests and settings. Node 22 built-ins only.
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { existsSync, realpathSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { isAbsolute, join, posix, resolve } from 'node:path';
import { executeRun, loadConfig, preflight, ROLES, UsageError, validateReport } from '../scripts/run.mjs';
import { ControllerBusyError, ControllerLostError, ensurePrivateDir, MAX_RUN_EVENTS, StoreError, WorkflowStore } from './store.mjs';

export { ROLES };
export const DEFAULT_SETTINGS = Object.freeze({ maxConcurrency: 2, maxFixRounds: 2 });
export const DEFAULT_BUDGET = Object.freeze({ maxTokens: 500_000, maxWallSeconds: 1800, maxFixRounds: 2 });
/** Run states that mean "a process may be live"; projected to 'unknown' without a live controller. */
export const ACTIVE_RUN_STATES = new Set(['starting', 'running', 'stopping']);
/** Task states that mean "in progress under a controller". */
export const ACTIVE_TASK_STATES = new Set(['implementing', 'first_delivery', 'checking', 'final_candidate', 'polishing', 'rechecking', 'fixing']);
/** Practical upper bounds for registered profile limits (also keeps runner timers in range). */
export const MAX_PROFILE_WALL_SECONDS = 86_400;
export const MAX_PROFILE_TOKENS = 1_000_000_000;

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
    const hi = k === 'maxWallSeconds' ? MAX_PROFILE_WALL_SECONDS : MAX_PROFILE_TOKENS;
    if (!Number.isFinite(v) || v <= 0 || v > hi) fail(`profiles.${role}: limits.${k} must be a positive number <= ${hi}`);
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
    out.usage = aggregateUsage(state.runs.filter((r) => r.taskId === t.id));
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

// ---------- usage aggregation ----------

const TOKEN_FIELDS = ['input', 'output', 'cacheRead', 'cacheWrite'];
const validCount = (v) => typeof v === 'number' && Number.isFinite(v) && v >= 0;

/** Secret-free copy of a runner usage view; invalid/missing counters stay null (never coerced to zero). */
export function sanitizeUsage(u) {
  if (!isObj(u) || !isObj(u.tokens)) return null;
  const tokens = {};
  for (const k of [...TOKEN_FIELDS, 'total']) tokens[k] = validCount(u.tokens[k]) ? u.tokens[k] : null;
  return {
    tokens,
    usageCompleteness: ['complete', 'partial', 'unknown'].includes(u.usageCompleteness) ? u.usageCompleteness : 'unknown',
    estimatedCostUsd: validCount(u.estimatedCostUsd) ? u.estimatedCostUsd : null,
  };
}

/**
 * Cumulative usage of runs (cache counters included in totals). A run that started a process but has no
 * known usage makes the aggregate partial/unknown: unknown is never turned into zero. `knownSubtotal` is
 * labelled as a subtotal; `tokens.total` is only set when every counted run is complete.
 */
export function aggregateUsage(runs) {
  const sums = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
  const unknownKey = new Set();
  let knownSubtotal = 0; let counted = 0; let spawned = 0; let complete = true; let cost = 0; let costKnown = true;
  for (const r of runs) {
    const u = r.usage;
    const hadProcess = Number.isInteger(r.pid) || isObj(u);
    if (!hadProcess) continue;
    spawned += 1;
    if (!isObj(u) || !isObj(u.tokens)) { complete = false; costKnown = false; TOKEN_FIELDS.forEach((k) => unknownKey.add(k)); continue; }
    for (const k of TOKEN_FIELDS) { if (validCount(u.tokens[k])) sums[k] += u.tokens[k]; else unknownKey.add(k); }
    if (validCount(u.tokens.total)) { knownSubtotal += u.tokens.total; counted += 1; } else complete = false;
    if (u.usageCompleteness !== 'complete') complete = false;
    if (validCount(u.estimatedCostUsd) && u.estimatedCostUsd > 0) cost += u.estimatedCostUsd; else costKnown = false;
  }
  const completeness = spawned === 0 ? 'complete' : counted === 0 ? 'unknown' : complete ? 'complete' : 'partial';
  const tokens = {};
  for (const k of TOKEN_FIELDS) tokens[k] = unknownKey.has(k) ? null : sums[k];
  tokens.total = completeness === 'complete' ? knownSubtotal : null;
  return {
    tokens, knownSubtotal, completeness, runsWithProcess: spawned,
    estimatedCostUsd: completeness === 'complete' && costKnown && spawned > 0 ? Number(cost.toFixed(6)) : null,
  };
}

// ---------- executor helpers ----------

const SAFE_EVENT_TYPES = new Set(['tool', 'lifecycle', 'state']);
const SAFE_SUMMARY_RE = /^[a-z][a-z0-9_]{0,39}$/;
const TERMINAL_RUN_STATES = new Set(['succeeded', 'failed', 'stopped', 'interrupted']);
const SHA_RE = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;

function pushEvent(run, type, summary, observedAt = now()) {
  run.events = [...(Array.isArray(run.events) ? run.events : []), { type, summary, observedAt }].slice(-MAX_RUN_EVENTS);
  run.updatedAt = observedAt;
}

function safeEvent(e) {
  if (!isObj(e) || !SAFE_EVENT_TYPES.has(e.type) || typeof e.summary !== 'string' || !SAFE_SUMMARY_RE.test(e.summary)) return null;
  const t = typeof e.observedAt === 'string' && Number.isFinite(Date.parse(e.observedAt)) ? new Date(e.observedAt).toISOString() : now();
  return { type: e.type, summary: e.summary, observedAt: t };
}

const gitOk = (cwd, args) => spawnSync('git', args, { cwd, stdio: 'ignore' }).status === 0;
const isAncestor = (cwd, a, b) => SHA_RE.test(a ?? '') && SHA_RE.test(b ?? '') && gitOk(cwd, ['merge-base', '--is-ancestor', a, b]);

/** Actual Git state of a task worktree (never trusts reports). */
export function inspectWorktree(worktree) {
  if (typeof worktree !== 'string' || !existsSync(worktree)) return { exists: false };
  const head = git(worktree, ['rev-parse', 'HEAD']);
  const branch = git(worktree, ['symbolic-ref', '--short', '-q', 'HEAD']);
  const status = git(worktree, ['status', '--porcelain']);
  return { exists: true, head, branch, clean: status === '' };
}

/** Repository-relative paths changed between two commits plus any dirty paths. */
function changedPathsBetween(wt, from, to) {
  const set = new Set();
  if (from !== to) {
    const r = spawnSync('git', ['diff', '--name-only', '-z', from, to], { cwd: wt, encoding: 'utf8' });
    if (r.status !== 0) return null;
    for (const p of r.stdout.split('\0')) if (p) set.add(p);
  }
  const st = spawnSync('git', ['status', '--porcelain', '-z'], { cwd: wt, encoding: 'utf8' });
  if (st.status !== 0) return null;
  const parts = st.stdout.split('\0');
  for (let i = 0; i < parts.length; i += 1) {
    const e = parts[i];
    if (e.length < 4) continue;
    set.add(e.slice(3));
    if (e[0] === 'R' || e[0] === 'C') i += 1;
  }
  return [...set].sort();
}

export const inScope = (path, scope) => scope.some((s) => path === s || path.startsWith(`${s}/`));

/** Liveness probe only (signal 0 delivers nothing). EPERM means a process exists. */
function pidAppearsAlive(pid) {
  if (!Number.isInteger(pid) || pid <= 0) return false;
  try { process.kill(pid, 0); return true; } catch (e) { return e.code === 'EPERM'; }
}

/** Derives pipeline position purely from durable succeeded runs (order of creation). */
export function pipelineState(state, task) {
  const p = { implemented: false, polished: false, verdict: null, fixRounds: 0, lastReviewRunId: null, startSha: null };
  for (const r of state.runs) {
    if (r.taskId !== task.id || r.state !== 'succeeded') continue;
    if (r.role === 'developer') {
      if (!p.implemented) p.startSha = r.summary?.startSha ?? null;
      p.implemented = true; p.verdict = null;
      if (r.summary?.purpose === 'fix') p.fixRounds += 1;
    } else if (r.role === 'reviewer') {
      p.verdict = r.summary?.verdict ?? null; p.lastReviewRunId = r.id;
    } else if (r.role === 'polisher') {
      p.polished = true; p.verdict = null;
    }
  }
  return p;
}

export function nextStep(p, maxFixRounds) {
  if (!p.implemented) return { role: 'developer', purpose: 'implement', taskState: 'implementing' };
  if (!p.verdict) return p.polished ? { role: 'reviewer', purpose: 'recheck', taskState: 'rechecking' } : { role: 'reviewer', purpose: 'check', taskState: 'checking' };
  if (p.verdict === 'changes_requested') {
    return p.fixRounds >= maxFixRounds ? { done: 'blocked', reason: 'fix_rounds_exhausted' } : { role: 'developer', purpose: 'fix', taskState: 'fixing' };
  }
  if (p.verdict === 'pass') return p.polished ? { done: 'delivered' } : { role: 'polisher', purpose: 'polish', taskState: 'polishing' };
  return { done: 'failed', reason: 'unknown_verdict' };
}

function deliveredCandidate(state, taskId) {
  const d = state.deliveries.filter((x) => x.taskId === taskId && x.state === 'delivered').at(-1);
  return d && SHA_RE.test(d.candidateSha ?? '') ? d : null;
}

/**
 * The commit a task's first developer run may start from: the frozen baseline, or (after deliberate
 * serialization/integration) a delivered candidate of the same repository that descends from the baseline.
 */
function initialStart(state, task, head) {
  if (!head) return null;
  if (head === task.baselineSha) return head;
  if (!isAncestor(task.worktree, task.baselineSha, head)) return null;
  const known = state.tasks.some((t) => t.id !== task.id && t.repository === task.repository && deliveredCandidate(state, t.id)?.candidateSha === head);
  return known ? head : null;
}

function expectedHead(state, task, p, head) {
  return p.implemented ? task.candidateSha : initialStart(state, task, head);
}

function effectiveProfileId(settings, task, role) {
  return settings.defaultProfiles?.[task.projectId]?.[role] ?? task.profileIds?.[role];
}

/** Resolves the registered profile for a role and verifies the config file still has the frozen digest. */
function verifiedProfile(state, settings, task, role) {
  const id = effectiveProfileId(settings, task, role);
  const prof = state.profiles.find((x) => x.id === id);
  if (!prof || prof.projectId !== task.projectId || prof.role !== role) return { error: 'profile_missing' };
  let fresh;
  try { fresh = freezeProfile(prof.configFile, role, task.projectId); } catch { return { error: 'profile_changed' }; }
  if (fresh.configDigest !== prof.configDigest) return { error: 'profile_changed' };
  const { maxWallSeconds, maxTokens } = prof.limits ?? {};
  if (!Number.isFinite(maxWallSeconds) || maxWallSeconds < 1 || maxWallSeconds > MAX_PROFILE_WALL_SECONDS
    || !Number.isFinite(maxTokens) || maxTokens < 1 || maxTokens > MAX_PROFILE_TOKENS) return { error: 'profile_limits_invalid' };
  return { profile: prof };
}

function budgetUse(state, task) {
  const runs = state.runs.filter((r) => r.taskId === task.id);
  let seconds = 0;
  for (const r of runs) {
    const a = Date.parse(r.startedAt); const b = r.endedAt ? Date.parse(r.endedAt) : Date.now();
    if (Number.isFinite(a) && Number.isFinite(b) && b > a) seconds += (b - a) / 1000;
  }
  return { seconds, usage: aggregateUsage(runs) };
}

const ROLE_CONTRACT = {
  implement: ['Implement the goal within the scope below. Run relevant local checks.',
    'Finish with one new local commit containing only in-scope paths, leaving the worktree clean. Report decision "changed".'],
  fix: ['Address ONLY the review findings listed below, within the scope. Run relevant local checks.',
    'Finish with one new local commit containing only in-scope paths, leaving the worktree clean. Report decision "changed".'],
  check: ['Review the exact candidate commit against the goal, scope and acceptance criteria. Do not modify anything.',
    'Report verdict "pass" or "changes_requested" with concrete findings.'],
  recheck: ['Re-review the exact candidate commit (after polishing) against the goal, scope and acceptance criteria. Do not modify anything.',
    'Report verdict "pass" or "changes_requested" with concrete findings.'],
  polish: ['Make only small, safe improvements to paths already in scope. If you change anything, finish with one new local commit and a clean worktree (decision "changed").',
    'If nothing needs changing, leave HEAD and the worktree untouched and report decision "no_change".'],
};

/** Per-run brief: frozen common context + own contract/findings/dependency references only. */
function buildBrief(state, task, step, { candidateSha, findings }) {
  const ctx = state.contexts.find((c) => c.id === task.contextRef.id && c.version === task.contextRef.version);
  const lines = [
    `# ${task.title}`, '', `Task ID: ${task.id}`, `Role: ${step.role} (${step.purpose})`,
    ...(candidateSha ? [`Candidate commit: ${candidateSha}`] : []), '',
    '## Goal', task.goal, '',
    '## Scope (only these repository-relative paths may change)', ...task.scope.map((s) => `- ${s}`), '',
    '## Acceptance criteria', ...task.acceptance.map((a) => `- ${a}`), '',
    `## Frozen context (version ${task.contextRef.version}, ${task.contextRef.digest})`, ctx ? ctx.text : '(missing)', '',
  ];
  const deps = task.dependencies.map((id) => ({ t: state.tasks.find((x) => x.id === id), d: deliveredCandidate(state, id) })).filter((x) => x.t && x.d);
  if (deps.length) {
    lines.push('## Dependency candidates (references only; the controller never merges them)');
    for (const { t, d } of deps) {
      lines.push(`- ${t.title} (${t.id}): ${d.candidateSha} ${t.repository === task.repository ? '[same repository, present in this worktree history]' : `[other repository ${t.repository}; reference only]`}`);
    }
    lines.push('');
  }
  if (findings?.length) {
    lines.push('## Review findings to address');
    for (const f of findings) lines.push(`- ${f.id}: ${f.summary}${f.path ? ` (${f.path}${f.line ? `:${f.line}` : ''})` : ''}`);
    lines.push('');
  }
  lines.push('## Role contract', ...ROLE_CONTRACT[step.purpose]);
  return `${lines.join('\n')}\n`;
}

function reportView(rep) {
  const out = { summary: rep.summary, checks: rep.checks, knownGaps: rep.knownGaps };
  if (rep.verdict) { out.verdict = rep.verdict; out.findings = rep.findings; }
  if (rep.decision) out.decision = rep.decision;
  return out;
}

const STOP_CATEGORIES = new Set(['aborted', 'token_limit', 'wall_timeout', 'stopped']);

// ---------- executeTasks ----------

/**
 * Runs the selected prepared tasks through developer -> reviewer -> (bounded fix -> reviewer) -> polisher ->
 * reviewer -> delivered under the single per-dataDir controller lease. Independent tasks run concurrently
 * (settings.maxConcurrency, default 2); tasks sharing a worktree are serialized; dependencies must be delivered
 * and (same repository) present in the dependent's history. Never pushes, merges, creates branches or worktrees.
 *
 * Returns { fatal, stopped, tasks:[{id,state,stateReason,candidateSha,resumeRole}] }. Throws WorkflowInputError
 * for invalid selections (nothing runs) and ControllerBusyError when another controller is live.
 */
export async function executeTasks({
  dataDir, taskIds, resume = false, acknowledgeInterruption = false, executeRunImpl = executeRun,
  env = process.env, signal = null, handleSignals = false, staleMs, heartbeatMs, pollMs = 500, onSummary = null,
} = {}) {
  if (typeof dataDir !== 'string' || !dataDir) fail('dataDir is required');
  if (!Array.isArray(taskIds) || taskIds.length < 1 || taskIds.length > 50) fail('taskIds must list 1..50 task UUIDs');
  for (const id of taskIds) if (typeof id !== 'string' || !UUID_RE.test(id)) fail('taskIds must be lowercase task UUIDs');
  if (typeof executeRunImpl !== 'function') fail('executeRunImpl must be a function');
  if (!Number.isInteger(pollMs) || pollMs < 10 || pollMs > 60_000) fail('pollMs must be an integer 10..60000');
  const ids = [...new Set(taskIds)];

  const ws = new WorkflowStore(dataDir, staleMs ? { staleMs } : {});
  const lease = ws.acquireController();
  const ctl = { fatal: null, aborting: null, lanes: new Map() };
  let state = null;
  let lastSettings = null;
  let wakeFn = null;
  const wake = () => { const f = wakeFn; wakeFn = null; f?.(); };
  const abortAll = (reason) => {
    ctl.aborting ??= reason;
    for (const l of ctl.lanes.values()) { l.stopReason ??= reason; l.ac.abort(); }
    wake();
  };
  const setFatal = (cat) => { ctl.fatal ??= cat; abortAll(cat); };
  const persist = () => {
    if (ctl.fatal === 'controller_lost') throw new ControllerLostError('controller lease lost');
    try { ws.write(state); } catch (e) {
      setFatal(e instanceof ControllerLostError ? 'controller_lost' : 'persistence_failed');
      throw e;
    }
  };
  const tryPersist = () => { try { persist(); return true; } catch { return false; } };
  const readSettings = () => {
    try { lastSettings = effectiveSettings(ws.readSettings()); } catch (e) { if (!lastSettings) throw e; }
    return lastSettings;
  };
  const notify = (info) => { if (typeof onSummary === 'function') { try { onSummary(info); } catch { /* observer only */ } } };

  ws.startHeartbeat(lease, heartbeatMs ?? Math.max(250, Math.floor(ws.staleMs / 3)), () => setFatal('controller_lost'));

  // ----- run one role -----
  const finishRunFailure = (task, run, runState, category, reason) => {
    run.state = runState; run.endedAt ??= now();
    run.summary = { ...(run.summary ?? {}), outcome: runState, errorCategory: category, reason: String(reason ?? category).slice(0, 300) };
    pushEvent(run, 'state', runState);
    task.state = runState === 'stopped' ? 'stopped' : 'failed';
    task.stateReason = category; task.resumeRole = run.role; task.updatedAt = now();
  };

  const verifyRun = (task, role, base, summary, runId) => {
    const digest = task.contextRef.digest;
    const insp = inspectWorktree(task.worktree);
    if (!insp.exists) return { category: 'worktree_missing' };
    if (insp.branch !== task.branch) return { category: 'branch_changed' };
    if (!insp.clean) return { category: 'dirty' };
    if (!isObj(summary) || summary.runId !== runId || summary.role !== role || summary.contextDigest !== digest
      || summary.candidateSha !== base || summary.resultSha !== insp.head) return { category: 'summary_mismatch' };
    if (!summary.report) return { category: 'report_missing' };
    let rep;
    try { rep = validateReport(role, summary.report); } catch { return { category: 'report_invalid' }; }
    if (rep.candidateSha !== insp.head || rep.contextDigest !== digest) return { category: 'report_stale' };
    const changed = changedPathsBetween(task.worktree, base, insp.head);
    if (!changed) return { category: 'git_unverifiable' };
    if (role === 'reviewer') {
      if (insp.head !== base || changed.length) return { category: 'reviewer_mutation' };
    } else if (role === 'developer') {
      if (insp.head === base || !isAncestor(task.worktree, base, insp.head)) return { category: 'no_commit' };
      if (rep.decision !== 'changed') return { category: 'decision_mismatch' };
    } else if (rep.decision === 'no_change') {
      if (insp.head !== base) return { category: 'decision_mismatch' };
    } else if (insp.head === base || !isAncestor(task.worktree, base, insp.head)) return { category: 'decision_mismatch' };
    const outside = changed.filter((p) => !inScope(p, task.scope));
    if (outside.length) return { category: 'scope_violation', reason: `changed paths outside scope: ${outside.slice(0, 5).join(', ')}` };
    return { head: insp.head, changed, report: rep };
  };

  const runRole = async (lane, task, step, p) => {
    const settings = readSettings();
    const vp = verifiedProfile(state, settings, task, step.role);
    if (vp.error) {
      task.state = 'failed'; task.stateReason = vp.error === 'profile_changed' ? 'profile_changed_requires_prepare' : vp.error;
      task.resumeRole = step.role; task.updatedAt = now(); persist(); return { ok: false };
    }
    const prof = vp.profile;
    const use = budgetUse(state, task);
    const remainingTokens = task.budget.maxTokens - use.usage.knownSubtotal;
    const remainingSeconds = Math.floor(task.budget.maxWallSeconds - use.seconds);
    if (remainingTokens < 1 || remainingSeconds < 1) {
      task.state = 'stopped'; task.stateReason = remainingTokens < 1 ? 'budget_tokens' : 'budget_time';
      task.resumeRole = step.role; task.updatedAt = now(); persist(); return { ok: false };
    }
    const capTokens = Math.floor(Math.min(prof.limits.maxTokens, remainingTokens));
    const capWall = Math.floor(Math.min(prof.limits.maxWallSeconds, remainingSeconds));
    if (!(capWall >= 1 && capWall <= MAX_PROFILE_WALL_SECONDS && capTokens >= 1 && capTokens <= MAX_PROFILE_TOKENS)) {
      task.state = 'failed'; task.stateReason = 'limits_out_of_range'; task.resumeRole = step.role; persist(); return { ok: false };
    }
    const insp = inspectWorktree(task.worktree);
    const base = insp.exists ? expectedHead(state, task, p, insp.head) : null;
    let pre = null;
    if (!insp.exists) pre = 'worktree_missing';
    else if (insp.branch !== task.branch) pre = 'branch_changed';
    else if (!insp.clean) pre = 'dirty_worktree_preserved';
    else if (!base) pre = p.implemented ? 'candidate_missing' : 'baseline_changed_requires_prepare';
    else if (insp.head !== base) pre = 'candidate_mismatch';
    if (pre) { task.state = 'failed'; task.stateReason = pre; task.resumeRole = step.role; task.updatedAt = now(); persist(); return { ok: false }; }

    const findings = step.purpose === 'fix' ? (state.reviews.find((r) => r.runId === p.lastReviewRunId)?.findings ?? []) : [];
    const runId = randomUUID();
    const runsDir = join(ws.dir, 'runs');
    const runDir = join(runsDir, runId);
    const ts = now();
    const run = {
      id: runId, agentId: lane.agentId, taskId: task.id, role: step.role, profileId: prof.id,
      modelSnapshot: { profileId: prof.id, provider: prof.provider, model: prof.model },
      contextRef: { ...task.contextRef }, state: 'starting', startedAt: ts, updatedAt: ts, events: [],
      summary: { purpose: step.purpose, ...(step.purpose === 'fix' ? { fixRound: p.fixRounds + 1 } : {}), limits: { maxTokens: capTokens, maxWallSeconds: capWall } },
    };
    pushEvent(run, 'state', 'starting', ts);
    state.runs.push(run);
    task.state = step.taskState; task.stateReason = null; task.resumeRole = step.role; task.updatedAt = ts;
    persist();

    const laneRun = { ac: new AbortController(), runId, taskId: task.id, stopReason: null, fatal: null };
    ctl.lanes.set(runId, laneRun);
    if (ctl.aborting) { laneRun.stopReason = ctl.aborting; laneRun.ac.abort(); }
    // Every persistence callback is guarded synchronously: a failed authoritative write aborts the run
    // and keeps a fatal controller category, so the run can never be recorded as successful.
    const guard = (fn) => (arg) => {
      if (laneRun.fatal) return;
      try { fn(arg); persist(); } catch (e) {
        laneRun.fatal = e instanceof ControllerLostError ? 'controller_lost' : 'persistence_failed';
        setFatal(laneRun.fatal);
        laneRun.ac.abort();
      }
    };
    const callbacks = {
      onStart: guard(({ pid } = {}) => { if (Number.isInteger(pid) && pid > 0) run.pid = pid; run.state = 'running'; pushEvent(run, 'state', 'running'); }),
      onEvent: guard((e) => { const s = safeEvent(e); if (s) pushEvent(run, s.type, s.summary, s.observedAt); }),
      onUsage: guard((u) => { const s = sanitizeUsage(u); if (s) { run.usage = s; run.updatedAt = now(); } }),
    };

    let summary = null; let thrown = null;
    try {
      ensurePrivateDir(runsDir); ensurePrivateDir(runDir);
      const briefPath = join(runDir, 'brief.md');
      writeFileSync(briefPath, buildBrief(state, task, step, { candidateSha: p.implemented ? base : null, findings }), { mode: 0o600, flag: 'wx' });
      const config = {
        projectId: prof.projectId, provider: prof.provider, model: prof.model, authEnv: prof.authEnv,
        instructions: [...prof.instructions], piCommand: [...prof.piCommand],
        limits: { maxTokens: capTokens, maxWallSeconds: capWall },
      };
      summary = await executeRunImpl({
        config, worktree: task.worktree, taskPath: briefPath, taskId: task.id, role: step.role,
        reportFile: join(runDir, 'report.json'), candidateSha: base, contextDigest: task.contextRef.digest,
        runId, agentId: lane.agentId, dataDir: ws.dataDir, signal: laneRun.ac.signal, env, ...callbacks,
      });
    } catch (e) { thrown = e; }
    ctl.lanes.delete(runId);
    try { rmSync(runDir, { recursive: true, force: true }); } catch { /* private scratch only */ }

    run.endedAt = now();
    if (summary) { const u = sanitizeUsage(summary); if (u) run.usage = u; }
    if (laneRun.fatal || ctl.fatal) {
      finishRunFailure(task, run, 'failed', laneRun.fatal ?? ctl.fatal, 'controller could not record run state');
    } else if (thrown) {
      const cat = thrown instanceof UsageError ? 'preflight' : 'runner_error';
      finishRunFailure(task, run, 'failed', cat, thrown instanceof UsageError ? `preflight: ${thrown.message}` : cat);
    } else if (!isObj(summary) || summary.outcome !== 'success') {
      const cat = isObj(summary) && typeof summary.errorCategory === 'string' ? summary.errorCategory : 'runner_failed';
      const stopped = laneRun.stopReason || summary?.outcome === 'stopped' || STOP_CATEGORIES.has(cat);
      let reason = laneRun.stopReason ?? cat;
      if (!laneRun.stopReason && cat === 'token_limit') reason = capTokens < prof.limits.maxTokens ? 'budget_tokens' : 'token_limit';
      if (!laneRun.stopReason && cat === 'wall_timeout') reason = capWall < prof.limits.maxWallSeconds ? 'budget_time' : 'wall_timeout';
      finishRunFailure(task, run, stopped ? 'stopped' : 'failed', reason, summary?.reason ?? reason);
    } else {
      const v = verifyRun(task, step.role, base, summary, runId);
      if (v.category) finishRunFailure(task, run, 'failed', v.category, v.reason ?? v.category);
      else {
        run.state = 'succeeded';
        run.summary = {
          ...run.summary, outcome: 'success', baselineSha: base, resultSha: v.head, changedPaths: v.changed.slice(0, 200),
          ...(step.purpose === 'implement' ? { startSha: base } : {}),
          elapsedSeconds: Number(((Date.parse(run.endedAt) - Date.parse(run.startedAt)) / 1000).toFixed(1)),
          report: reportView(v.report), ...(v.report.verdict ? { verdict: v.report.verdict } : {}),
          ...(v.report.decision ? { decision: v.report.decision } : {}),
        };
        pushEvent(run, 'state', 'succeeded');
        const t2 = now();
        if (step.role === 'developer' || step.role === 'polisher') task.candidateSha = v.head;
        if (step.purpose === 'implement') {
          state.deliveries.push({
            id: randomUUID(), taskId: task.id, contextRef: { ...task.contextRef }, candidateSha: v.head, repository: task.repository,
            runIds: [runId], checks: deliveryChecks({ head: true, clean: true, scope: true }, v.report), knownGaps: v.report.knownGaps, state: 'first', createdAt: t2, updatedAt: t2,
          });
          task.state = 'first_delivery';
        }
        if (step.role === 'reviewer') {
          state.reviews.push({
            id: randomUUID(), taskId: task.id, runId, candidateSha: v.head, contextDigest: v.report.contextDigest,
            verdict: v.report.verdict, findings: v.report.findings, checks: v.report.checks, createdAt: t2,
          });
          if (v.report.verdict === 'pass' && step.purpose === 'check') {
            state.deliveries.push({
              id: randomUUID(), taskId: task.id, contextRef: { ...task.contextRef }, candidateSha: v.head, repository: task.repository,
              runIds: taskRunIds(task.id), checks: deliveryChecks({ head: true, clean: true, scope: true, review: true }, v.report),
              knownGaps: v.report.knownGaps, state: 'final_candidate', createdAt: t2, updatedAt: t2,
            });
            task.state = 'final_candidate';
          }
        }
        task.updatedAt = t2;
      }
    }
    persist();
    notify({ taskId: task.id, runId, role: run.role, state: run.state, category: run.summary?.errorCategory ?? null });
    return { ok: run.state === 'succeeded' };
  };

  const taskRunIds = (taskId) => state.runs.filter((r) => r.taskId === taskId && r.state === 'succeeded').map((r) => r.id).slice(-50);

  // Explicit, verified statuses; agent-reported checks are labelled "reported", never inferred as QA/acceptance.
  function deliveryChecks(verified, report) {
    const out = [];
    if (verified.head) out.push({ name: 'git_head_matches_candidate', status: 'pass' });
    if (verified.clean) out.push({ name: 'worktree_clean', status: 'pass' });
    if (verified.scope) out.push({ name: 'changed_paths_in_scope', status: 'pass' });
    if (verified.context) out.push({ name: 'context_digest_matches', status: 'pass' });
    if (verified.review) out.push({ name: 'review_verdict', status: 'pass' });
    for (const c of report?.checks ?? []) out.push({ name: 'reported', status: 'reported', command: c.command, result: c.result });
    return out.slice(0, 60);
  }

  const finalizeDelivery = (task, p) => {
    const insp = inspectWorktree(task.worktree);
    const review = state.reviews.find((r) => r.runId === p.lastReviewRunId);
    const ctxRec = state.contexts.find((c) => c.id === task.contextRef.id && c.version === task.contextRef.version && c.digest === task.contextRef.digest);
    let bad = null;
    if (!insp.exists) bad = 'worktree_missing';
    else if (insp.branch !== task.branch || !insp.clean) bad = 'worktree_state';
    else if (!SHA_RE.test(task.candidateSha ?? '') || insp.head !== task.candidateSha) bad = 'candidate_mismatch';
    else if (!review || review.verdict !== 'pass' || review.candidateSha !== task.candidateSha) bad = 'review_not_on_candidate';
    else if (!ctxRec || review.contextDigest !== task.contextRef.digest) bad = 'context_not_fresh';
    else if (!p.startSha || !isAncestor(task.worktree, task.baselineSha, task.candidateSha) || !isAncestor(task.worktree, p.startSha, task.candidateSha)) bad = 'baseline_not_ancestor';
    else {
      const changed = changedPathsBetween(task.worktree, p.startSha, task.candidateSha);
      if (!changed || changed.some((x) => !inScope(x, task.scope))) bad = 'scope_violation';
    }
    const t = now();
    if (bad) {
      task.state = 'failed'; task.stateReason = `delivery_verification_failed:${bad}`; task.resumeRole = 'reviewer'; task.updatedAt = t;
    } else {
      state.deliveries.push({
        id: randomUUID(), taskId: task.id, contextRef: { ...task.contextRef }, candidateSha: task.candidateSha, repository: task.repository,
        runIds: taskRunIds(task.id), checks: deliveryChecks({ head: true, clean: true, scope: true, context: true, review: true }, review),
        knownGaps: state.runs.find((r) => r.id === review.runId)?.summary?.report?.knownGaps ?? [], state: 'delivered', createdAt: t, updatedAt: t,
      });
      task.state = 'delivered'; task.stateReason = null; task.resumeRole = null; task.updatedAt = t;
    }
    persist();
  };

  const runTask = async (task, agentId) => {
    const lane = { agentId };
    try {
      for (let guardSteps = 0; guardSteps < 20; guardSteps += 1) {
        const p = pipelineState(state, task);
        const maxFix = Math.min(task.budget?.maxFixRounds ?? DEFAULT_BUDGET.maxFixRounds, readSettings().maxFixRounds);
        const step = nextStep(p, maxFix);
        if (step.done === 'delivered') { finalizeDelivery(task, p); return; }
        if (step.done) {
          task.state = step.done; task.stateReason = step.reason; task.resumeRole = null; task.updatedAt = now(); persist(); return;
        }
        if (ctl.aborting || ctl.fatal) {
          task.state = 'stopped'; task.stateReason = ctl.fatal ?? ctl.aborting; task.resumeRole = step.role; task.updatedAt = now();
          tryPersist(); return;
        }
        const r = await runRole(lane, task, step, p);
        if (!r.ok) return;
      }
      task.state = 'failed'; task.stateReason = 'step_limit'; persist();
    } catch {
      // Authoritative write failed: controller is fatal; keep the in-memory record truthful and stop.
      if (!['failed', 'stopped', 'blocked', 'delivered'].includes(task.state)) { task.state = 'failed'; task.stateReason = ctl.fatal ?? 'controller_error'; }
    }
  };

  // ----- stop requests: only runs owned by this controller -----
  const pollStops = () => {
    let reqs;
    try { reqs = ws.listStopRequests(); } catch { return; }
    for (const req of reqs) {
      const l = ctl.lanes.get(req.runId);
      if (l) {
        if (!l.stopReason) {
          l.stopReason = 'stop_requested'; l.ac.abort();
          const run = state.runs.find((r) => r.id === req.runId);
          if (run) { run.state = 'stopping'; pushEvent(run, 'state', 'stop_requested'); tryPersist(); }
        }
      } else {
        const run = state?.runs.find((r) => r.id === req.runId);
        if (run && TERMINAL_RUN_STATES.has(run.state)) { try { ws.removeStopRequest(req.requestId); } catch { /* retry later */ } }
      }
    }
  };

  // ----- scheduler -----
  const schedule = async (selected) => {
    const pending = [...selected];
    const active = new Map();
    const slots = [];
    const orig = new Map(selected.map((t) => [t.id, { state: t.state, stateReason: t.stateReason ?? null }]));
    const depStatus = (t) => {
      let wait = false;
      for (const d of t.dependencies ?? []) {
        if (pending.some((x) => x.id === d) || active.has(d)) { wait = true; continue; }
        const dt = state.tasks.find((x) => x.id === d);
        const dl = dt && dt.state === 'delivered' ? deliveredCandidate(state, d) : null;
        if (!dl) return { block: selected.some((x) => x.id === d) ? 'dependency_failed' : 'dependency_not_delivered' };
        if (dt.repository === t.repository) {
          const head = inspectWorktree(t.worktree).head;
          if (!head || !isAncestor(t.worktree, dl.candidateSha, head)) return { block: 'dependency_not_integrated' };
        }
      }
      return { wait };
    };
    while (pending.length || active.size) {
      if (ctl.aborting || ctl.fatal) {
        for (const t of pending.splice(0)) {
          const o = orig.get(t.id);
          if (o.state === 'ready' || o.state === 'blocked') { t.state = o.state; t.stateReason = o.stateReason; }
          else { t.state = 'stopped'; t.stateReason = 'controller_stopped'; }
          t.updatedAt = now();
        }
        tryPersist();
        await Promise.allSettled([...active.values()].map((a) => a.promise));
        break;
      }
      const max = readSettings().maxConcurrency;
      let changed = false;
      for (const t of [...pending]) {
        const dep = depStatus(t);
        if (dep.block) {
          pending.splice(pending.indexOf(t), 1);
          t.state = 'blocked'; t.stateReason = dep.block; t.updatedAt = now(); changed = true; continue;
        }
        const busy = [...active.values()].some((a) => a.worktree === t.worktree);
        const reason = dep.wait ? 'dependencies' : busy ? 'worktree' : active.size >= max ? 'concurrency' : null;
        if (reason) {
          if (t.state !== 'queued' || t.stateReason !== reason) { t.state = 'queued'; t.stateReason = reason; t.updatedAt = now(); changed = true; }
          continue;
        }
        pending.splice(pending.indexOf(t), 1);
        let slot = slots.findIndex((x) => !x); if (slot < 0) slot = slots.length;
        slots[slot] = true;
        const agentId = `Pi-${String(slot + 1).padStart(2, '0')}`;
        const entry = { worktree: t.worktree, promise: null };
        active.set(t.id, entry);
        entry.promise = runTask(t, agentId).finally(() => { active.delete(t.id); slots[slot] = false; wake(); });
      }
      if (changed && !tryPersist()) continue;
      if (!pending.length && !active.size) break;
      await new Promise((r) => { wakeFn = r; setTimeout(r, 1000).unref(); });
    }
  };

  // ----- selection / resume validation (nothing runs if this fails) -----
  const validateSelection = (settings) => {
    const selected = ids.map((id) => state.tasks.find((t) => t.id === id) ?? fail(`task ${id} does not exist`));
    const done = new Set(); const visiting = new Set();
    const visit = (t) => {
      if (done.has(t.id)) return;
      if (visiting.has(t.id)) fail('task dependencies contain a cycle');
      visiting.add(t.id);
      for (const d of t.dependencies ?? []) {
        const dt = state.tasks.find((x) => x.id === d);
        if (!dt) fail(`task ${t.id} depends on missing task ${d}`);
        if (dt.projectId !== t.projectId) fail(`task ${t.id} depends on a task of a different project`);
        visit(dt);
      }
      visiting.delete(t.id); done.add(t.id);
    };
    selected.forEach(visit);
    const acknowledged = [];
    for (const t of selected) {
      const resumable = ['failed', 'stopped', 'unknown'].includes(t.state);
      const readyLike = t.state === 'ready' || (t.state === 'blocked' && String(t.stateReason ?? '').startsWith('dependency_'));
      if (!readyLike && !resumable) fail(`task ${t.id} is ${t.state}; only ready, failed, stopped or acknowledged unknown tasks can execute`);
      if (resumable && !resume) fail(`task ${t.id} is ${t.state}; use --resume to continue it explicitly`);
      if (t.state === 'unknown') {
        if (!acknowledgeInterruption) fail(`task ${t.id} was interrupted with an unverified process; pass --acknowledge-interruption after confirming it is gone`);
        const unknownRuns = state.runs.filter((r) => r.taskId === t.id && r.state === 'unknown');
        if (unknownRuns.some((r) => pidAppearsAlive(r.pid))) fail(`task ${t.id}: a recorded process id still appears alive; refusing to resume`);
        acknowledged.push(...unknownRuns);
      }
      for (const role of ROLES) {
        const vp = verifiedProfile(state, settings, t, role);
        if (vp.error === 'profile_changed') fail(`task ${t.id}: ${role} profile config changed since preparation; prepare a new task`);
        if (vp.error) fail(`task ${t.id}: ${role} profile is not usable (${vp.error})`);
      }
      if (resumable) {
        const insp = inspectWorktree(t.worktree);
        if (!insp.exists) fail(`task ${t.id}: worktree is missing`);
        if (insp.branch !== t.branch) fail(`task ${t.id}: worktree branch does not match the task`);
        if (!insp.clean) fail(`task ${t.id}: worktree has uncommitted changes; they are preserved, resolve them first`);
        const exp = expectedHead(state, t, pipelineState(state, t), insp.head);
        if (!exp || insp.head !== exp) fail(`task ${t.id}: worktree HEAD does not match the recorded candidate; refusing to resume`);
      }
    }
    for (const r of acknowledged) { r.state = 'interrupted'; r.endedAt ??= now(); pushEvent(r, 'state', 'interruption_acknowledged'); }
    return selected;
  };

  // Previous controller died: nothing nonterminal is verifiable, so mark it and never replay automatically.
  const markInterrupted = () => {
    let changed = false;
    const unknownTasks = new Set();
    for (const r of state.runs) {
      if (ACTIVE_RUN_STATES.has(r.state)) { r.state = 'unknown'; pushEvent(r, 'state', 'controller_restart_unverified'); unknownTasks.add(r.taskId); changed = true; }
    }
    for (const t of state.tasks) {
      if (unknownTasks.has(t.id)) { t.state = 'unknown'; t.stateReason = 'controller_interrupted'; t.updatedAt = now(); changed = true; }
      else if (ACTIVE_TASK_STATES.has(t.state) || t.state === 'queued') {
        const hasRuns = state.runs.some((r) => r.taskId === t.id);
        t.state = hasRuns ? 'stopped' : 'ready'; t.stateReason = 'controller_interrupted'; t.updatedAt = now(); changed = true;
      }
    }
    return changed;
  };

  let pollTimer = null;
  const onSig = () => abortAll('controller_signal');
  try {
    state = ws.read();
    if (markInterrupted()) persist();
    const settings = readSettings();
    const selected = validateSelection(settings);
    persist();
    if (handleSignals) { process.on('SIGINT', onSig); process.on('SIGTERM', onSig); }
    if (signal) { if (signal.aborted) abortAll('controller_signal'); else signal.addEventListener('abort', onSig, { once: true }); }
    pollTimer = setInterval(pollStops, pollMs);
    pollTimer.unref();
    await schedule(selected);
    pollStops();
  } finally {
    clearInterval(pollTimer);
    if (handleSignals) { process.off('SIGINT', onSig); process.off('SIGTERM', onSig); }
    signal?.removeEventListener?.('abort', onSig);
    ws.releaseController(lease);
  }
  return {
    fatal: ctl.fatal,
    stopped: ctl.aborting && !ctl.fatal ? ctl.aborting : null,
    tasks: ids.map((id) => {
      const t = state.tasks.find((x) => x.id === id);
      return { id, state: t.state, stateReason: t.stateReason ?? null, candidateSha: t.candidateSha ?? null, resumeRole: t.resumeRole ?? null };
    }),
  };
}

export { StoreError };

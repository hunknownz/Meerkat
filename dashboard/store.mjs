// Private local store for the Pi Developer dashboard.
// Node 22 built-ins only. Persists JSON collections in a data directory with
// 0700 dir / 0600 files, written atomically (temp file + rename). All IDs are
// server-generated UUIDs; client-supplied ids are always ignored.
import { randomUUID } from 'node:crypto';
import { chmodSync, mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

/** The task stage enum, in workflow order. */
export const STAGES = ['inbox', 'ready', 'implementing', 'review', 'verify', 'done'];

/** Maximum accepted request body size in bytes (bounded JSON). */
export const MAX_BODY_BYTES = 256 * 1024;

const COLLECTIONS = ['workspaces', 'repositories', 'agents', 'tasks'];
const ID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Restricts identity fields to server-generated UUIDs (no arbitrary ID injection). */
export function isValidId(value) {
  return typeof value === 'string' && ID_RE.test(value);
}

/** Default private user data directory — under the home dir, never inside a repository. */
export function defaultDataDir() {
  return join(homedir(), '.codex-pi-developer', 'dashboard');
}

/** Error carrying an HTTP status for the API layer. */
export class HttpError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

/** Raised when an existing data file cannot be read or parsed; the file is left untouched. */
export class StoreLoadError extends Error {
  constructor(message) {
    super(message);
    this.name = 'StoreLoadError';
  }
}

const bad = (message) => new HttpError(400, message);
const nf = (kind, id) => new HttpError(404, `${kind} not found: ${id}`);

function str(value, max, label, { required = false } = {}) {
  if (value === undefined || value === null) {
    if (required) throw bad(`${label} is required`);
    return undefined;
  }
  if (typeof value !== 'string') throw bad(`${label} must be a string`);
  if (value.length > max) throw bad(`${label} must be at most ${max} characters`);
  if (required && !value.trim()) throw bad(`${label} cannot be empty`);
  return value;
}

function httpUrl(value, max, label) {
  if (value === undefined || value === null) return undefined;
  if (typeof value !== 'string' || value.length > max) throw bad(`${label} must be a string of at most ${max} characters`);
  if (value.trim() === '') return '';
  let parsed;
  try {
    parsed = new URL(value);
  } catch {
    throw bad(`${label} must be a valid http(s) URL`);
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') throw bad(`${label} must be a valid http(s) URL`);
  return value;
}

function refId(value, list, label, { required = false } = {}) {
  if (value === undefined || value === null) {
    if (required) throw bad(`${label} is required`);
    return undefined;
  }
  if (!isValidId(value)) throw bad(`${label} must reference an existing record`);
  if (!list.some((x) => x.id === value)) throw bad(`${label} does not exist: ${value}`);
  return value;
}

function pickStage(value) {
  if (!STAGES.includes(value)) throw bad(`stage must be one of: ${STAGES.join(', ')}`);
  return value;
}

const nowIso = () => new Date().toISOString();

export class Store {
  constructor(dataDir) {
    if (typeof dataDir !== 'string' || !dataDir) throw new Error('dataDir is required');
    this.dir = dataDir;
    this.collections = { workspaces: [], repositories: [], agents: [], tasks: [] };
    this.load();
  }

  file(name) {
    return join(this.dir, `${name}.json`);
  }

  /**
   * Loads every collection or none: a missing file means an empty collection,
   * but any other read error, malformed JSON, or malformed row throws a
   * StoreLoadError and leaves both the files and in-memory state untouched.
   */
  load() {
    mkdirSync(this.dir, { recursive: true, mode: 0o700 });
    try {
      chmodSync(this.dir, 0o700);
    } catch {
      // best effort; mkdir mode usually suffices
    }
    const loaded = {};
    for (const name of COLLECTIONS) {
      const file = this.file(name);
      let raw;
      try {
        raw = readFileSync(file, 'utf8');
      } catch (e) {
        if (e && e.code === 'ENOENT') {
          loaded[name] = [];
          continue;
        }
        throw new StoreLoadError(`cannot read ${file}: ${e && e.message}; the file was not modified`);
      }
      let parsed;
      try {
        parsed = JSON.parse(raw);
      } catch (e) {
        throw new StoreLoadError(`malformed JSON in ${file}: ${e.message}; fix or move the file aside (it was not modified)`);
      }
      if (!Array.isArray(parsed)) {
        throw new StoreLoadError(`invalid data in ${file}: expected a JSON array; fix or move the file aside (it was not modified)`);
      }
      const seen = new Set();
      parsed.forEach((row, i) => {
        if (!row || typeof row !== 'object' || Array.isArray(row) || typeof row.id !== 'string') {
          throw new StoreLoadError(`invalid record #${i} in ${file}: expected an object with a string id; fix or move the file aside (it was not modified)`);
        }
        if (seen.has(row.id)) {
          throw new StoreLoadError(`duplicate id ${row.id} in ${file}; fix or move the file aside (it was not modified)`);
        }
        seen.add(row.id);
      });
      loaded[name] = parsed;
    }
    this.collections = loaded;
  }

  saveCollection(name) {
    const body = JSON.stringify(this.collections[name], null, 2) + '\n';
    const tmp = `${this.file(name)}.${process.pid}.${randomUUID()}.tmp`;
    writeFileSync(tmp, body, { mode: 0o600 });
    renameSync(tmp, this.file(name));
  }

  /** Validates on a draft copy, persists, then publishes; a rejected update never mutates state. */
  commitUpdate(name, current, draft) {
    const list = this.collections[name];
    const idx = list.indexOf(current);
    list[idx] = draft;
    try {
      this.saveCollection(name);
    } catch (e) {
      list[idx] = current;
      throw e;
    }
    return draft;
  }

  commitCreate(name, row) {
    this.collections[name].push(row);
    try {
      this.saveCollection(name);
    } catch (e) {
      this.collections[name].pop();
      throw e;
    }
    return row;
  }

  list(name) {
    return this.collections[name];
  }

  get(name, id) {
    return this.collections[name].find((x) => x.id === id);
  }

  state() {
    return this.collections;
  }

  // ---- workspaces ----
  createWorkspace(input) {
    const ws = {
      id: randomUUID(),
      name: str(input.name, 200, 'name', { required: true }),
    };
    if (input.description !== undefined && input.description !== null) {
      ws.description = str(input.description, 4000, 'description');
    }
    return this.commitCreate('workspaces', ws);
  }

  updateWorkspace(id, input) {
    const current = this.get('workspaces', id);
    if (!current) throw nf('workspace', id);
    const ws = { ...current };
    if (input.name !== undefined) ws.name = str(input.name, 200, 'name', { required: true });
    if (input.description === null) delete ws.description;
    else if (input.description !== undefined) ws.description = str(input.description, 4000, 'description');
    return this.commitUpdate('workspaces', current, ws);
  }

  // ---- repositories ----
  createRepository(input) {
    const workspaceId = refId(input.workspaceId, this.collections.workspaces, 'workspaceId', { required: true });
    const repo = {
      id: randomUUID(),
      workspaceId,
      name: str(input.name, 200, 'name', { required: true }),
      path: str(input.path, 2000, 'path', { required: true }),
    };
    if (input.defaultBranch !== undefined && input.defaultBranch !== null) {
      repo.defaultBranch = str(input.defaultBranch, 200, 'defaultBranch');
    }
    return this.commitCreate('repositories', repo);
  }

  updateRepository(id, input) {
    const current = this.get('repositories', id);
    if (!current) throw nf('repository', id);
    const repo = { ...current };
    if (input.workspaceId !== undefined) {
      repo.workspaceId = refId(input.workspaceId, this.collections.workspaces, 'workspaceId', { required: true });
      if (repo.workspaceId !== current.workspaceId && this.collections.tasks.some((t) => t.repositoryId === id)) {
        throw bad('cannot move a repository that has tasks to another workspace');
      }
    }
    if (input.name !== undefined) repo.name = str(input.name, 200, 'name', { required: true });
    if (input.path !== undefined) repo.path = str(input.path, 2000, 'path', { required: true });
    if (input.defaultBranch === null) delete repo.defaultBranch;
    else if (input.defaultBranch !== undefined) repo.defaultBranch = str(input.defaultBranch, 200, 'defaultBranch');
    return this.commitUpdate('repositories', current, repo);
  }

  // ---- agent profiles ----
  createAgent(input) {
    const agent = {
      id: randomUUID(),
      name: str(input.name, 200, 'name', { required: true }),
      role: str(input.role, 100, 'role', { required: true }),
    };
    if (input.provider !== undefined && input.provider !== null) agent.provider = str(input.provider, 200, 'provider');
    if (input.model !== undefined && input.model !== null) agent.model = str(input.model, 200, 'model');
    if (input.description !== undefined && input.description !== null) agent.description = str(input.description, 4000, 'description');
    return this.commitCreate('agents', agent);
  }

  updateAgent(id, input) {
    const current = this.get('agents', id);
    if (!current) throw nf('agent', id);
    const agent = { ...current };
    if (input.name !== undefined) agent.name = str(input.name, 200, 'name', { required: true });
    if (input.role !== undefined) agent.role = str(input.role, 100, 'role', { required: true });
    for (const key of ['provider', 'model', 'description']) {
      if (input[key] === null) delete agent[key];
      else if (input[key] !== undefined) agent[key] = str(input[key], key === 'description' ? 4000 : 200, key);
    }
    return this.commitUpdate('agents', current, agent);
  }

  // ---- tasks ----
  createTask(input) {
    const workspaceId = refId(input.workspaceId, this.collections.workspaces, 'workspaceId', { required: true });
    const repositoryId = refId(input.repositoryId, this.collections.repositories, 'repositoryId', { required: true });
    const repo = this.get('repositories', repositoryId);
    if (repo.workspaceId !== workspaceId) throw bad('repositoryId does not belong to workspaceId');
    const task = {
      id: randomUUID(),
      workspaceId,
      repositoryId,
      title: str(input.title, 300, 'title', { required: true }),
      brief: str(input.brief, 20000, 'brief', { required: true }),
      stage: input.stage === undefined ? 'inbox' : pickStage(input.stage),
      createdAt: nowIso(),
      updatedAt: nowIso(),
    };
    if (input.agentId !== undefined && input.agentId !== null) {
      task.agentId = refId(input.agentId, this.collections.agents, 'agentId');
    }
    if (input.issueUrl !== undefined && input.issueUrl !== null) task.issueUrl = httpUrl(input.issueUrl, 2000, 'issueUrl');
    if (input.worktreePath !== undefined && input.worktreePath !== null) task.worktreePath = str(input.worktreePath, 2000, 'worktreePath');
    if (input.branch !== undefined && input.branch !== null) task.branch = str(input.branch, 200, 'branch');
    return this.commitCreate('tasks', task);
  }

  updateTask(id, input) {
    const current = this.get('tasks', id);
    if (!current) throw nf('task', id);
    // Validate the proposed workspace/repository pair as a unit before building
    // the draft, so a rejected move leaves the stored task untouched.
    const workspaceId = input.workspaceId === undefined
      ? current.workspaceId
      : refId(input.workspaceId, this.collections.workspaces, 'workspaceId', { required: true });
    const repositoryId = input.repositoryId === undefined
      ? current.repositoryId
      : refId(input.repositoryId, this.collections.repositories, 'repositoryId', { required: true });
    const repo = this.get('repositories', repositoryId);
    if (!repo || repo.workspaceId !== workspaceId) throw bad('repositoryId does not belong to workspaceId');
    const task = { ...current, workspaceId, repositoryId };
    if (input.title !== undefined) task.title = str(input.title, 300, 'title', { required: true });
    if (input.brief !== undefined) task.brief = str(input.brief, 20000, 'brief', { required: true });
    if (input.stage !== undefined) task.stage = pickStage(input.stage);
    if (input.agentId === null) delete task.agentId;
    else if (input.agentId !== undefined) task.agentId = refId(input.agentId, this.collections.agents, 'agentId');
    if (input.issueUrl === null) delete task.issueUrl;
    else if (input.issueUrl !== undefined) task.issueUrl = httpUrl(input.issueUrl, 2000, 'issueUrl');
    if (input.worktreePath === null) delete task.worktreePath;
    else if (input.worktreePath !== undefined) task.worktreePath = str(input.worktreePath, 2000, 'worktreePath');
    if (input.branch === null) delete task.branch;
    else if (input.branch !== undefined) task.branch = str(input.branch, 200, 'branch');
    task.updatedAt = nowIso();
    return this.commitUpdate('tasks', current, task);
  }

  deleteTask(id) {
    const idx = this.collections.tasks.findIndex((x) => x.id === id);
    if (idx === -1) throw nf('task', id);
    const [removed] = this.collections.tasks.splice(idx, 1);
    try {
      this.saveCollection('tasks');
    } catch (e) {
      this.collections.tasks.splice(idx, 0, removed);
      throw e;
    }
    return true;
  }
}
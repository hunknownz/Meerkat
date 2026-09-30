import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { start, urlOf, HOST } from '../dashboard/server.mjs';
import { Store, StoreLoadError, MAX_BODY_BYTES } from '../dashboard/store.mjs';

const here = fileURLToPath(new URL('.', import.meta.url));
const serverFile = join(here, '../dashboard/server.mjs');

function tempDir() {
  return mkdtempSync(join(tmpdir(), 'pi-dash-test-'));
}

function close(server) {
  return new Promise((resolve) => server.close(resolve));
}

/** Starts a dashboard on an ephemeral port over a fresh temp data dir. */
async function withServer(fn, dataDir = tempDir()) {
  const server = await start({ port: 0, dataDir });
  try {
    return await fn(urlOf(server), dataDir, server);
  } finally {
    await close(server);
  }
}

async function api(base, method, path, body, headers = {}) {
  const init = { method, headers: { ...headers } };
  if (body !== undefined) {
    init.headers['Content-Type'] ??= 'application/json';
    init.body = typeof body === 'string' ? body : JSON.stringify(body);
  }
  const res = await fetch(new URL(path, base), init);
  const text = await res.text();
  let json = null;
  try { json = JSON.parse(text); } catch { /* non-JSON body */ }
  return { status: res.status, headers: res.headers, json, text };
}

/** Creates workspace + repository + agent; returns their records. */
async function seed(base, suffix = '') {
  const ws = await api(base, 'POST', '/api/workspaces', { name: `WS${suffix}` });
  assert.equal(ws.status, 201, ws.text);
  const repo = await api(base, 'POST', '/api/repositories', {
    workspaceId: ws.json.data.id, name: `repo${suffix}`, path: `/tmp/repo${suffix}`, defaultBranch: 'main',
  });
  assert.equal(repo.status, 201, repo.text);
  const agent = await api(base, 'POST', '/api/agents', { name: `Pi${suffix}`, role: 'developer', model: 'm' });
  assert.equal(agent.status, 201, agent.text);
  return { ws: ws.json.data, repo: repo.json.data, agent: agent.json.data };
}

// ---------- static UI / server basics ----------

test('starts on port 0, binds loopback, and serves the control-panel UI', async () => {
  await withServer(async (base, _dir, server) => {
    const addr = server.address();
    assert.ok(addr.port > 0, 'picks an ephemeral port');
    assert.equal(addr.family, 'IPv4');
    assert.equal(addr.address, HOST, 'binds the loopback host only');
    assert.equal(base, `http://${HOST}:${addr.port}/`);

    const res = await fetch(base);
    assert.equal(res.status, 200);
    assert.match(res.headers.get('content-type') || '', /text\/html/);
    const body = await res.text();
    assert.match(body, /<title>Meerkat · Agent 状态<\/title>/);
    assert.match(body, /<h1><img class="brandmark" src="\/meerkat\.svg" alt="">Meerkat/);
    assert.match(body, /src="\/app\.js"/);

    const css = await fetch(new URL('/app.css', base));
    assert.equal(css.status, 200);
    assert.match(css.headers.get('content-type') || '', /text\/css/);
    const js = await fetch(new URL('/app.js', base));
    assert.equal(js.status, 200);
    assert.match(js.headers.get('content-type') || '', /javascript/);
    const icon = await fetch(new URL('/meerkat.svg', base));
    assert.equal(icon.status, 200);
    assert.match(icon.headers.get('content-type') || '', /image\/svg\+xml/);
    assert.match(await icon.text(), /viewBox="0 0 128 128"/);
  });
});

test('non-GET methods on UI assets return 405 JSON with Allow: GET', async () => {
  await withServer(async (base) => {
    for (const path of ['/', '/app.css', '/app.js', '/meerkat.svg']) {
      for (const method of ['POST', 'PUT', 'DELETE', 'PATCH', 'OPTIONS', 'HEAD']) {
        const res = await fetch(new URL(path, base), { method });
        assert.equal(res.status, 405, `${method} ${path}`);
        assert.equal(res.headers.get('allow'), 'GET');
        if (method !== 'HEAD') {
          const json = await res.json();
          assert.equal(json.ok, false);
          assert.match(json.error, /method not allowed/);
        }
      }
    }
  });
});

test('unknown paths return 404', async () => {
  await withServer(async (base) => {
    for (const path of ['/nope', '/dashboard', '/index.html', '/favicon.ico', '/etc/passwd', '/public/app.js']) {
      const res = await fetch(new URL(path, base));
      assert.equal(res.status, 404, path);
      assert.match(res.headers.get('content-type') || '', /text\/plain/);
      assert.match(await res.text(), /404/);
    }
    for (const path of ['/api/', '/api/nope', '/api/tasks/not-a-uuid', '/api/tasks/a/b']) {
      const r = await api(base, 'GET', path);
      assert.equal(r.status, 404, path);
      assert.equal(r.json.ok, false);
    }
  });
});

test('API method rules: state/runs are GET-only; only tasks are deletable', async () => {
  await withServer(async (base) => {
    let r = await api(base, 'POST', '/api/state', {});
    assert.equal(r.status, 405);
    assert.equal(r.headers.get('allow'), 'GET');
    r = await api(base, 'DELETE', '/api/workspaces');
    assert.equal(r.status, 405);
    assert.equal(r.headers.get('allow'), 'GET, POST');
    const { ws } = await seed(base);
    r = await api(base, 'DELETE', `/api/workspaces/${ws.id}`);
    assert.equal(r.status, 405);
    assert.equal(r.headers.get('allow'), 'GET, PUT');
  });
});

test('exits cleanly when the requested port is already in use', async () => {
  const holder = await start({ port: 0, dataDir: tempDir() });
  try {
    const port = holder.address().port;
    const r = spawnSync(process.execPath, [serverFile, '--port', String(port), '--data-dir', tempDir()], { encoding: 'utf8' });
    assert.equal(r.status, 1);
    assert.match(r.stderr, /dashboard: .*EADDRINUSE/);
    assert.doesNotMatch(r.stderr, /UnhandledPromiseRejection|^\s+at /m, 'no stack trace crash');
  } finally {
    await close(holder);
  }
});

// ---------- CRUD flow ----------

test('creates workspace, repository, agent, and task; state reflects them', async () => {
  await withServer(async (base, dataDir) => {
    const { ws, repo, agent } = await seed(base);
    const r = await api(base, 'POST', '/api/tasks', {
      id: 'client-chosen', workspaceId: ws.id, repositoryId: repo.id, agentId: agent.id,
      title: 'Add login', brief: 'Implement login form', issueUrl: 'https://example.com/i/1',
      worktreePath: '/tmp/wt', branch: 'feat/login',
    });
    assert.equal(r.status, 201, r.text);
    const task = r.json.data;
    assert.notEqual(task.id, 'client-chosen', 'client ids are ignored');
    assert.match(task.id, /^[0-9a-f-]{36}$/);
    assert.equal(task.stage, 'inbox');
    assert.equal(task.agentId, agent.id);

    const state = await api(base, 'GET', '/api/state');
    assert.equal(state.status, 200);
    assert.deepEqual(state.json.stages, ['inbox', 'ready', 'implementing', 'review', 'verify', 'done']);
    assert.equal(state.json.data.workspaces.length, 1);
    assert.equal(state.json.data.repositories.length, 1);
    assert.equal(state.json.data.agents.length, 1);
    assert.deepEqual(state.json.data.tasks, [task]);

    const one = await api(base, 'GET', `/api/tasks/${task.id}`);
    assert.deepEqual(one.json.data, task);
    const list = await api(base, 'GET', '/api/tasks');
    assert.deepEqual(list.json.tasks, [task]);

    const runs = await api(base, 'GET', '/api/runs');
    assert.equal(runs.status, 200);
    assert.deepEqual(runs.json.runs, []);

    // Paths are stored as data only: nothing is created on disk for worktreePath/repo path.
    assert.equal(existsSync('/tmp/wt/.pi-developer'), false);

    if (process.platform !== 'win32') {
      assert.equal(statSync(dataDir).mode & 0o777, 0o700);
      assert.equal(statSync(join(dataDir, 'tasks.json')).mode & 0o777, 0o600);
    }
  });
});

test('moves, edits, and deletes a task', async () => {
  await withServer(async (base) => {
    const { ws, repo, agent } = await seed(base);
    const created = (await api(base, 'POST', '/api/tasks', {
      workspaceId: ws.id, repositoryId: repo.id, title: 'T', brief: 'B', agentId: agent.id,
    })).json.data;

    let r = await api(base, 'PUT', `/api/tasks/${created.id}`, { stage: 'implementing' });
    assert.equal(r.status, 200, r.text);
    assert.equal(r.json.data.stage, 'implementing');

    r = await api(base, 'PUT', `/api/tasks/${created.id}`, { stage: 'shipped' });
    assert.equal(r.status, 400);
    assert.match(r.json.error, /stage must be one of/);

    r = await api(base, 'PUT', `/api/tasks/${created.id}`, {
      title: 'T2', brief: 'B2', branch: 'feat/x', agentId: null, id: 'ignored',
    });
    assert.equal(r.status, 200, r.text);
    assert.equal(r.json.data.id, created.id);
    assert.equal(r.json.data.title, 'T2');
    assert.equal(r.json.data.brief, 'B2');
    assert.equal(r.json.data.branch, 'feat/x');
    assert.equal(r.json.data.stage, 'implementing');
    assert.equal('agentId' in r.json.data, false, 'null clears optional field');

    // Move to another workspace together with a repository in that workspace.
    const other = await seed(base, '2');
    r = await api(base, 'PUT', `/api/tasks/${created.id}`, { workspaceId: other.ws.id, repositoryId: other.repo.id });
    assert.equal(r.status, 200, r.text);
    assert.equal(r.json.data.workspaceId, other.ws.id);
    assert.equal(r.json.data.repositoryId, other.repo.id);

    r = await api(base, 'DELETE', `/api/tasks/${created.id}`);
    assert.equal(r.status, 200);
    assert.equal(r.json.ok, true);
    r = await api(base, 'GET', `/api/tasks/${created.id}`);
    assert.equal(r.status, 404);
    r = await api(base, 'DELETE', `/api/tasks/${created.id}`);
    assert.equal(r.status, 404);
    r = await api(base, 'PUT', `/api/tasks/${created.id}`, { title: 'x' });
    assert.equal(r.status, 404);
  });
});

test('data persists across server restart with the same --data-dir', async () => {
  const dataDir = tempDir();
  let ids;
  await withServer(async (base) => {
    const { ws, repo, agent } = await seed(base);
    const task = (await api(base, 'POST', '/api/tasks', {
      workspaceId: ws.id, repositoryId: repo.id, title: 'Persist me', brief: 'B',
    })).json.data;
    await api(base, 'PUT', `/api/tasks/${task.id}`, { stage: 'review' });
    ids = { ws: ws.id, repo: repo.id, agent: agent.id, task: task.id };
  }, dataDir);

  await withServer(async (base) => {
    const { json } = await api(base, 'GET', '/api/state');
    assert.deepEqual(json.data.workspaces.map((x) => x.id), [ids.ws]);
    assert.deepEqual(json.data.repositories.map((x) => x.id), [ids.repo]);
    assert.deepEqual(json.data.agents.map((x) => x.id), [ids.agent]);
    assert.equal(json.data.tasks.length, 1);
    assert.equal(json.data.tasks[0].id, ids.task);
    assert.equal(json.data.tasks[0].title, 'Persist me');
    assert.equal(json.data.tasks[0].stage, 'review');
  }, dataDir);

  // The CLI honours --data-dir too.
  const r = spawnSync(process.execPath, [serverFile, '--help'], { encoding: 'utf8' });
  assert.equal(r.status, 0);
  assert.match(r.stdout, /--data-dir/);
});

// ---------- request hardening ----------

test('rejects cross-origin writes and leaves state unchanged', async () => {
  await withServer(async (base) => {
    const { ws, repo } = await seed(base);
    const task = (await api(base, 'POST', '/api/tasks', {
      workspaceId: ws.id, repositoryId: repo.id, title: 'T', brief: 'B',
    })).json.data;

    const evil = { Origin: 'http://evil.example' };
    let r = await api(base, 'POST', '/api/workspaces', { name: 'x' }, evil);
    assert.equal(r.status, 403);
    assert.match(r.json.error, /cross-origin/);
    r = await api(base, 'PUT', `/api/tasks/${task.id}`, { title: 'hacked' }, evil);
    assert.equal(r.status, 403);
    r = await api(base, 'DELETE', `/api/tasks/${task.id}`, undefined, evil);
    assert.equal(r.status, 403);
    r = await api(base, 'POST', '/api/workspaces', { name: 'x' }, { 'Sec-Fetch-Site': 'cross-site' });
    assert.equal(r.status, 403);
    // A localhost alias on the same port is still a different origin.
    r = await api(base, 'POST', '/api/workspaces', { name: 'x' }, { Origin: base.replace('127.0.0.1', 'localhost').replace(/\/$/, '') });
    assert.equal(r.status, 403);

    const state = (await api(base, 'GET', '/api/state')).json.data;
    assert.equal(state.workspaces.length, 1);
    assert.deepEqual(state.tasks, [task]);

    // Same-origin writes are allowed.
    r = await api(base, 'POST', '/api/workspaces', { name: 'ok' }, { Origin: base.replace(/\/$/, '') });
    assert.equal(r.status, 201, r.text);
  });
});

test('rejects invalid input: content type, JSON, shape, fields', async () => {
  await withServer(async (base) => {
    let r = await api(base, 'POST', '/api/workspaces', 'name=x', { 'Content-Type': 'text/plain' });
    assert.equal(r.status, 415);
    r = await api(base, 'POST', '/api/workspaces', '{not json');
    assert.equal(r.status, 400);
    assert.match(r.json.error, /invalid JSON/);
    r = await api(base, 'POST', '/api/workspaces', '');
    assert.equal(r.status, 400);
    for (const body of ['[]', 'null', '42', '"s"']) {
      r = await api(base, 'POST', '/api/workspaces', body);
      assert.equal(r.status, 400, body);
      assert.match(r.json.error, /JSON object/);
    }
    r = await api(base, 'POST', '/api/workspaces', {});
    assert.equal(r.status, 400);
    assert.match(r.json.error, /name is required/);
    r = await api(base, 'POST', '/api/workspaces', { name: '   ' });
    assert.equal(r.status, 400);
    r = await api(base, 'POST', '/api/workspaces', { name: 5 });
    assert.equal(r.status, 400);
    r = await api(base, 'POST', '/api/workspaces', { name: 'x'.repeat(201) });
    assert.equal(r.status, 400);
    r = await api(base, 'POST', '/api/agents', { name: 'a' });
    assert.equal(r.status, 400);
    assert.match(r.json.error, /role is required/);

    const { ws, repo } = await seed(base);
    r = await api(base, 'POST', '/api/tasks', { workspaceId: ws.id, repositoryId: repo.id, title: 'T', brief: 'B', issueUrl: 'javascript:alert(1)' });
    assert.equal(r.status, 400);
    assert.match(r.json.error, /issueUrl/);
    r = await api(base, 'POST', '/api/tasks', { workspaceId: ws.id, repositoryId: repo.id, title: 'T', brief: 'B', stage: 'nope' });
    assert.equal(r.status, 400);
    r = await api(base, 'POST', '/api/tasks', { workspaceId: ws.id, repositoryId: repo.id, title: 'T' });
    assert.equal(r.status, 400);
    assert.match(r.json.error, /brief is required/);
    r = await api(base, 'POST', '/api/tasks', { workspaceId: ws.id, repositoryId: repo.id, title: 'T', brief: 'B', agentId: '00000000-0000-4000-8000-000000000000' });
    assert.equal(r.status, 400);
    assert.match(r.json.error, /agentId does not exist/);

    const state = (await api(base, 'GET', '/api/state')).json.data;
    assert.equal(state.workspaces.length, 1);
    assert.equal(state.agents.length, 1);
    assert.equal(state.tasks.length, 0);
  });
});

test('rejects request bodies larger than the limit with 413', async () => {
  await withServer(async (base) => {
    const big = JSON.stringify({ name: 'x', description: 'y'.repeat(MAX_BODY_BYTES) });
    assert.ok(Buffer.byteLength(big) > MAX_BODY_BYTES);
    const r = await api(base, 'POST', '/api/workspaces', big);
    assert.equal(r.status, 413);
    assert.match(r.json.error, /too large/);
    const state = (await api(base, 'GET', '/api/state')).json.data;
    assert.equal(state.workspaces.length, 0);
  });
});

// ---------- data integrity ----------

const MISSING_ID = '00000000-0000-4000-8000-000000000000';

test('createRepository requires an existing workspaceId', async () => {
  await withServer(async (base) => {
    for (const workspaceId of [undefined, null, '', 'not-a-uuid', MISSING_ID]) {
      const r = await api(base, 'POST', '/api/repositories', { workspaceId, name: 'r', path: '/tmp/r' });
      assert.equal(r.status, 400, `workspaceId=${workspaceId}`);
      assert.match(r.json.error, /workspaceId/);
    }
    assert.equal((await api(base, 'GET', '/api/repositories')).json.repositories.length, 0);
  });
});

test('createTask requires existing workspaceId and repositoryId in the same workspace', async () => {
  await withServer(async (base) => {
    const a = await seed(base, 'A');
    const b = await seed(base, 'B');
    const base_ = { title: 'T', brief: 'B' };
    const cases = [
      [{ repositoryId: a.repo.id }, /workspaceId is required/],
      [{ workspaceId: a.ws.id }, /repositoryId is required/],
      [{ workspaceId: MISSING_ID, repositoryId: a.repo.id }, /workspaceId does not exist/],
      [{ workspaceId: a.ws.id, repositoryId: MISSING_ID }, /repositoryId does not exist/],
      [{ workspaceId: 'bogus', repositoryId: a.repo.id }, /workspaceId/],
      [{ workspaceId: a.ws.id, repositoryId: b.repo.id }, /does not belong/],
    ];
    for (const [ids, re] of cases) {
      const r = await api(base, 'POST', '/api/tasks', { ...base_, ...ids });
      assert.equal(r.status, 400, JSON.stringify(ids));
      assert.match(r.json.error, re);
    }
    assert.equal((await api(base, 'GET', '/api/tasks')).json.tasks.length, 0);
  });
});

test('updateTask validates workspaceId+repositoryId together; rejected updates change nothing', async () => {
  const dataDir = tempDir();
  await withServer(async (base) => {
    const a = await seed(base, 'A');
    const b = await seed(base, 'B');
    const task = (await api(base, 'POST', '/api/tasks', {
      workspaceId: a.ws.id, repositoryId: a.repo.id, title: 'T', brief: 'B', stage: 'ready',
    })).json.data;
    const onDisk = readFileSync(join(dataDir, 'tasks.json'), 'utf8');

    const rejected = [
      { workspaceId: b.ws.id, title: 'changed' }, // new workspace, old repo
      { repositoryId: b.repo.id, stage: 'done' }, // new repo, old workspace
      { workspaceId: b.ws.id, repositoryId: a.repo.id, title: 'changed' },
      { workspaceId: MISSING_ID, repositoryId: b.repo.id },
      { workspaceId: b.ws.id, repositoryId: MISSING_ID },
      { workspaceId: null, title: 'changed' },
      { repositoryId: null, title: 'changed' },
      { title: 'changed', stage: 'bogus' }, // valid field before invalid one
    ];
    for (const body of rejected) {
      const r = await api(base, 'PUT', `/api/tasks/${task.id}`, body);
      assert.equal(r.status, 400, JSON.stringify(body));
      const now = (await api(base, 'GET', `/api/tasks/${task.id}`)).json.data;
      assert.deepEqual(now, task, `state unchanged after ${JSON.stringify(body)}`);
      assert.equal(readFileSync(join(dataDir, 'tasks.json'), 'utf8'), onDisk, 'file unchanged');
    }

    // A repository that has tasks cannot be moved out from under them.
    const r = await api(base, 'PUT', `/api/repositories/${a.repo.id}`, { workspaceId: b.ws.id });
    assert.equal(r.status, 400);
    assert.equal((await api(base, 'GET', `/api/repositories/${a.repo.id}`)).json.data.workspaceId, a.ws.id);
  }, dataDir);
});

test('Store.load fails visibly on corrupt JSON and never overwrites it', async () => {
  for (const [name, content, re] of [
    ['tasks', '{ not json', /malformed JSON/],
    ['workspaces', '{"a":1}', /expected a JSON array/],
    ['agents', '[1, 2]', /invalid record/],
    ['repositories', '[{"id":"x"},{"id":"x"}]', /duplicate id/],
  ]) {
    const dataDir = tempDir();
    const file = join(dataDir, `${name}.json`);
    writeFileSync(file, content);
    assert.throws(() => new Store(dataDir), (e) => e instanceof StoreLoadError && re.test(e.message), name);
    assert.equal(readFileSync(file, 'utf8'), content, 'corrupt file left untouched');

    // The CLI refuses to start instead of replacing data.
    const r = spawnSync(process.execPath, [serverFile, '--port', '0', '--data-dir', dataDir], { encoding: 'utf8', timeout: 10000 });
    assert.equal(r.status, 1, r.stderr);
    assert.match(r.stderr, /dashboard: /);
    assert.match(r.stderr, re);
    assert.equal(readFileSync(file, 'utf8'), content);
  }
});

test('Store.load fails on read errors other than ENOENT', { skip: process.platform === 'win32' }, () => {
  // A directory where a data file should be yields EISDIR.
  const dataDir = tempDir();
  mkdirSync(join(dataDir, 'tasks.json'));
  assert.throws(() => new Store(dataDir), (e) => e instanceof StoreLoadError && /cannot read/.test(e.message));

  // An unreadable file yields EACCES (skipped when running as root).
  if (typeof process.getuid === 'function' && process.getuid() !== 0) {
    const dir2 = tempDir();
    const file = join(dir2, 'agents.json');
    writeFileSync(file, '[]');
    chmodSync(file, 0o000);
    try {
      assert.throws(() => new Store(dir2), (e) => e instanceof StoreLoadError && /cannot read/.test(e.message));
    } finally {
      chmodSync(file, 0o600);
    }
  }

  // Missing files are simply empty collections.
  const fresh = join(tempDir(), 'nested', 'store');
  const store = new Store(fresh);
  assert.deepEqual(store.state(), { workspaces: [], repositories: [], agents: [], tasks: [] });
  rmSync(fresh, { recursive: true, force: true });
});

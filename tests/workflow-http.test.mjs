// Workflow HTTP surface of the dashboard server, exercised over raw node:http against a
// private temp dataDir with an injected workflow service (the workflow core is not part
// of this checkout). No credentials, GitHub, browser, or desktop app involved.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { start } from '../dashboard/server.mjs';
import { writeActive } from '../dashboard/active.mjs';

const TOKEN = 'workflow-http-test-token-0123456789';
const RUN_ID = '3f2b8c1e-6a4d-4e2f-9b7a-1c2d3e4f5a6b';
const REQ_ID = '9d8c7b6a-5f4e-4d3c-8b2a-1f0e9d8c7b6a';
const SNAPSHOT = {
  schemaVersion: 1,
  observedAt: '2026-10-02T09:00:00Z',
  projects: [], tasks: [], deliveries: [], reviews: [], contexts: [], profiles: [],
  runs: [{ id: RUN_ID, taskId: 't', role: 'developer', state: 'running', startedAt: '2026-10-02T08:55:00Z' }],
  counts: { running: 1, queued: 0 },
  controller: { state: 'running' },
};

/** Recording workflow service; individual methods can be overridden per test. */
function fakeService(overrides = {}) {
  const calls = [];
  const service = {
    readWorkflow: async (dir) => { calls.push(['read', dir]); return structuredClone(SNAPSHOT); },
    requestStop: async (dir, runId, requestId) => { calls.push(['stop', dir, runId, requestId]); return { requested: true }; },
    updateSettings: async (dir, input) => { calls.push(['settings', dir, input]); return { ...input }; },
  };
  for (const [k, fn] of Object.entries(overrides)) service[k] = async (...a) => { calls.push([k, ...a]); return fn(...a); };
  return { service, calls, writes: () => calls.filter(([k]) => k !== 'read' && k !== 'readWorkflow') };
}

async function serve(t, overrides) {
  const dataDir = mkdtempSync(join(tmpdir(), 'meerkat-workflow-http-'));
  const fake = fakeService(overrides);
  const server = await start({ port: 0, dataDir, workflow: fake.service, sessionToken: TOKEN });
  t.after(async () => {
    server.closeAllConnections?.();
    await new Promise((r) => server.close(r));
    rmSync(dataDir, { recursive: true, force: true });
  });
  const { port } = server.address();
  const ownHost = `127.0.0.1:${port}`;
  /** Raw request so Host/Origin/Sec-Fetch-Site can be set freely. */
  const request = (method, path, { headers = {}, body } = {}) => new Promise((resolve, reject) => {
    const payload = body === undefined ? undefined : (typeof body === 'string' ? body : JSON.stringify(body));
    const all = {
      host: ownHost, connection: 'close',
      ...(payload !== undefined ? { 'content-type': 'application/json', 'content-length': Buffer.byteLength(payload) } : {}),
      ...headers,
    };
    for (const k of Object.keys(all)) if (all[k] === undefined) delete all[k]; // allows omitting a header
    const req = http.request({ host: '127.0.0.1', port, method, path, agent: false, headers: all }, (res) => {
      const chunks = [];
      res.on('data', (c) => chunks.push(c));
      res.on('end', () => {
        const text = Buffer.concat(chunks).toString('utf8');
        let json = null;
        try { json = JSON.parse(text); } catch { /* not JSON */ }
        resolve({ status: res.statusCode, headers: res.headers, text, json });
      });
    });
    req.on('error', reject);
    req.end(payload);
  });
  const write = (method, path, body, headers = {}) => request(method, path, { body, headers: { 'x-meerkat-token': TOKEN, origin: `http://${ownHost}`, ...headers } });
  return { ...fake, dataDir, port, ownHost, request, write };
}

const STOP = `/api/workflow/runs/${RUN_ID}/stop`;

test('GET /api/workflow answers only to its own literal loopback Host', async (t) => {
  const s = await serve(t);
  const ok = await s.request('GET', '/api/workflow');
  assert.equal(ok.status, 200);
  assert.deepEqual(ok.json.data, SNAPSHOT);
  assert.equal(ok.json.sessionToken, TOKEN);
  assert.deepEqual(s.calls, [['read', s.dataDir]]);
  for (const host of [`localhost:${s.port}`, 'evil.test', `127.0.0.1:${s.port + 1}`, `evil.test:${s.port}`, '127.0.0.1', `[::1]:${s.port}`]) {
    const r = await s.request('GET', '/api/workflow', { headers: { host } });
    assert.equal(r.status, 403, host);
    assert.doesNotMatch(r.text, new RegExp(TOKEN), host);
  }
  assert.equal(s.calls.length, 1, 'foreign Host never reaches the service');
});

test('workflow writes require own Host, same-origin indicators, and the session token', async (t) => {
  const s = await serve(t);
  const body = { requestId: REQ_ID };
  for (const [name, headers] of [
    ['no token', { 'x-meerkat-token': '' }],
    ['wrong token', { 'x-meerkat-token': `${TOKEN.slice(0, -1)}x` }],
    ['short token', { 'x-meerkat-token': 'abc' }],
    ['foreign Host', { host: `localhost:${s.port}` }],
    ['cross Origin', { origin: 'http://evil.test' }],
    ['localhost Origin', { origin: `http://localhost:${s.port}` }],
    ['sec-fetch cross-site', { 'sec-fetch-site': 'cross-site' }],
    ['sec-fetch same-site', { 'sec-fetch-site': 'same-site' }],
  ]) {
    const stop = await s.write('POST', STOP, body, headers);
    assert.equal(stop.status, 403, `stop: ${name}`);
    const set = await s.write('PUT', '/api/workflow/settings', { maxConcurrency: 2 }, headers);
    assert.equal(set.status, 403, `settings: ${name}`);
  }
  assert.deepEqual(s.writes(), [], 'rejected writes never reach the service');
  // No Origin header at all (non-browser client with the token) is allowed.
  const r = await s.write('POST', STOP, body, { origin: undefined });
  assert.equal(r.status, 202);
});

test('write bodies are capped, typed JSON objects with known fields only', async (t) => {
  const s = await serve(t);
  const big = await s.write('POST', STOP, { requestId: REQ_ID, pad: 'x'.repeat(9 * 1024) });
  assert.equal(big.status, 413);
  const bigSettings = await s.write('PUT', '/api/workflow/settings', `{"maxConcurrency":2,"x":"${'y'.repeat(9000)}"}`);
  assert.equal(bigSettings.status, 413);
  assert.equal((await s.write('POST', STOP, 'requestId=x', { 'content-type': 'text/plain' })).status, 415);
  assert.equal((await s.write('POST', STOP, '{nope')).status, 400);
  assert.equal((await s.write('POST', STOP, [REQ_ID])).status, 400);
  assert.equal((await s.write('POST', STOP, { requestId: REQ_ID, force: true })).status, 400);
  assert.equal((await s.write('POST', STOP, { requestId: 'not-a-uuid' })).status, 400);
  assert.equal((await s.write('POST', '/api/workflow/runs/not-a-uuid/stop', { requestId: REQ_ID })).status, 400);
  assert.equal((await s.write('GET', STOP)).status, 405);
  assert.deepEqual(s.writes(), []);
});

test('accepted stop is 202 "requested", never a confirmation that the run stopped', async (t) => {
  const s = await serve(t);
  const r = await s.write('POST', `/api/workflow/runs/${RUN_ID.toUpperCase()}/stop`, { requestId: REQ_ID.toUpperCase() });
  assert.equal(r.status, 202);
  assert.equal(r.json.ok, true);
  assert.equal(r.json.accepted, true);
  assert.equal(r.json.runId, RUN_ID);
  assert.equal(r.json.requestId, REQ_ID);
  assert.ok(!('stopped' in r.json) && !('state' in r.json), 'response does not claim a final state');
  assert.deepEqual(s.writes(), [['stop', s.dataDir, RUN_ID, REQ_ID]]);
  // The snapshot is still the controller's view: the run keeps running until it reports otherwise.
  const snap = await s.request('GET', '/api/workflow');
  assert.equal(snap.json.data.runs[0].state, 'running');
});

test('settings accept only validated fields and pass a fresh object to the service', async (t) => {
  const s = await serve(t);
  const good = { maxConcurrency: 3, maxFixRounds: 0, defaultProfiles: { meerkat: { developer: 'codex:gpt-5', reviewer: 'claude.sonnet' } } };
  const r = await s.write('PUT', '/api/workflow/settings', good);
  assert.equal(r.status, 200);
  assert.deepEqual(r.json.data, good);
  assert.deepEqual(s.writes(), [['settings', s.dataDir, good]]);
  for (const bad of [
    {}, { maxConcurrency: 0 }, { maxConcurrency: 5 }, { maxConcurrency: '2' }, { maxConcurrency: 1.5 },
    { maxFixRounds: 3 }, { maxFixRounds: -1 }, { command: 'rm -rf /' }, { maxConcurrency: 2, configPath: '/etc/x' },
    { defaultProfiles: [] }, { defaultProfiles: { '../x': { developer: 'p' } } },
    { defaultProfiles: { meerkat: { admin: 'p' } } }, { defaultProfiles: { meerkat: { developer: '/bin/sh' } } },
    { defaultProfiles: { meerkat: { developer: '~/profile.json' } } }, { defaultProfiles: { meerkat: 'p' } },
  ]) {
    const b = await s.write('PUT', '/api/workflow/settings', bad);
    assert.equal(b.status, 400, JSON.stringify(bad));
  }
  assert.equal((await s.write('POST', '/api/workflow/settings', { maxConcurrency: 2 })).status, 405);
  assert.equal(s.writes().length, 1, 'invalid settings never reach the service');
});

test('service corruption or storage failure is 503 (never empty data), without leaking details', async (t) => {
  const leak = new Error('corrupt JSON at /Users/someone/.meerkat/workflow/state.json');
  const ioErr = Object.assign(new Error('EIO: i/o error, open /private/tmp/x/state.json'), { code: 'EIO' });
  const s = await serve(t, {
    readWorkflow: () => { throw leak; },
    requestStop: () => { throw ioErr; },
    updateSettings: () => { throw ioErr; },
  });
  const read = await s.request('GET', '/api/workflow');
  assert.equal(read.status, 503);
  assert.equal(read.json.ok, false);
  assert.ok(!('data' in read.json) && !('sessionToken' in read.json));
  assert.doesNotMatch(read.text, /Users|someone|state\.json/);
  const stop = await s.write('POST', STOP, { requestId: REQ_ID });
  assert.equal(stop.status, 503);
  assert.doesNotMatch(stop.text, /private|state\.json/);
  const set = await s.write('PUT', '/api/workflow/settings', { maxConcurrency: 2 });
  assert.equal(set.status, 503);

  const malformed = await serve(t, { readWorkflow: () => ({ schemaVersion: 2, runs: [] }) });
  const m = await malformed.request('GET', '/api/workflow');
  assert.equal(m.status, 503);
  assert.match(m.json.error, /malformed/);
});

test('there is no HTTP route that executes, starts, or runs anything', async (t) => {
  const s = await serve(t);
  for (const [method, path] of [
    ['POST', '/api/workflow'], ['PUT', '/api/workflow'],
    ['POST', '/api/workflow/execute'], ['POST', '/api/workflow/exec'], ['POST', '/api/workflow/run'],
    ['POST', '/api/workflow/runs'], ['POST', `/api/workflow/runs/${RUN_ID}`], ['POST', `/api/workflow/runs/${RUN_ID}/start`],
    ['POST', `/api/workflow/runs/${RUN_ID}/exec`], ['POST', `/api/workflow/runs/${RUN_ID}/stop/now`],
    ['POST', '/api/workflow/tasks'], ['POST', '/api/workflow/controller/start'],
    ['POST', '/api/exec'], ['POST', '/api/execute'], ['POST', '/api/run'], ['POST', '/api/shell'],
  ]) {
    const r = await s.write(method, path, { command: 'echo pwned', requestId: REQ_ID });
    assert.ok([404, 405].includes(r.status), `${method} ${path} -> ${r.status}`);
  }
  assert.deepEqual(s.writes(), []);
});

test('legacy GET /api/active still lists live independent runs from the private dataDir', async (t) => {
  const s = await serve(t);
  const empty = await s.request('GET', '/api/active');
  assert.equal(empty.status, 200);
  assert.deepEqual(empty.json, { ok: true, count: 0, agents: [] });
  const id = '0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d';
  await writeActive({ id, task: 'legacy pi task', model: 'm', worktree: '/w', pid: process.pid }, s.dataDir);
  const r = await s.request('GET', '/api/active');
  assert.equal(r.status, 200);
  assert.equal(r.json.count, 1);
  assert.equal(r.json.agents[0].id, id);
  assert.equal(r.json.agents[0].task, 'legacy pi task');
  assert.equal((await s.request('POST', '/api/active', { body: {} })).status, 405);
  // The workflow snapshot exposes the same legacy runs alongside (not instead of) workflow data.
  const wf = await s.request('GET', '/api/workflow');
  assert.equal(wf.json.legacyActive[0].id, id);
  assert.equal(wf.json.data.counts.running, 1);
});

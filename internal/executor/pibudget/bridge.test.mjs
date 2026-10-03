import test from 'node:test';
import assert from 'node:assert/strict';
import { createGatedFetch, inspectPayload, usageObserver, installBridge } from './bridge.mjs';

const config = { provider: 'fixture', model: 'text-model', version: 'pi-http-v1' };
const payload = () => ({ model: config.model, messages: [{ role: 'user', content: 'private prompt' }], stream: true, max_tokens: 1000 });
const input = value => new Request('http://127.0.0.1/fixture', { method: 'POST', body: JSON.stringify(value), headers: { 'Content-Type': 'application/json' } });
const data = value => `data: ${JSON.stringify(value)}\n\n`;
const usage = { prompt_tokens: 100, completion_tokens: 20, total_tokens: 120, prompt_tokens_details: { cached_tokens: 30 } };
function authority(events, mutate = () => {}) {
  return async (_config, path, value) => {
    events.push({ path, value }); mutate(path, value);
    if (path === '/reserve') return { id: value.id, maxOutput: 50, reservedTokens: value.inputEstimate + 50 };
    return { accepted: true };
  };
}

test('payload refuses media, missing limits, mixed caps and alternate models', () => {
  assert.equal(inspectPayload(JSON.stringify(payload()), config).maxOutput, 1000);
  for (const change of [p => delete p.max_tokens, p => p.max_completion_tokens = 100, p => p.model = 'other',
    p => p.messages[0].content = [{ type: 'image_url', image_url: { url: 'private' } }], p => p.stream = false, p => p.n = 2]) {
    const p = payload(); change(p);
    assert.throws(() => inspectPayload(JSON.stringify(p), config), /budget_unavailable/);
  }
});

test('wire cap is enforced after one permit and original text does not enter authority', async () => {
  const events = [], wire = [];
  const fetch = createGatedFetch(config, async req => {
    wire.push(JSON.parse(await req.text()));
    assert.deepEqual(events.map(e => e.path), ['/reserve', '/begin']);
    return new Response(data({ choices: [{ delta: { content: '狐獴' }, finish_reason: 'stop' }], usage }) + 'data: [DONE]\n\n', { headers: { 'Content-Type': 'text/event-stream' } });
  }, authority(events));
  assert.match(await (await fetch(input(payload()))).text(), /狐獴/);
  assert.equal(wire[0].max_tokens, 50);
  assert.equal(events[2].path, '/settle');
  assert.deepEqual(events[2].value.tokens, { input: 70, output: 20, cacheRead: 30, cacheWrite: null, total: 120 });
  assert.equal(events[2].value.state, 'settled');
  assert(!JSON.stringify(events).includes('private prompt'));
});

test('denied or lost permits do not reach model and are not retried', async () => {
  for (const failedPath of ['/reserve', '/begin']) {
    let network = 0; const events = [];
    const fetch = createGatedFetch(config, async () => { network++; }, authority(events, path => { if (path === failedPath) throw Error('lost reply'); }));
    await assert.rejects(fetch(input(payload())));
    assert.equal(network, 0);
    assert.equal(events.filter(e => e.path === failedPath).length, 1);
  }
});

test('missing raw usage and transport failure stay unknown', async () => {
  for (const mode of ['no-usage', 'network', 'bad-count', 'non-sse']) {
    const events = [];
    const fetch = createGatedFetch(config, async () => {
      if (mode === 'network') throw Error('provider failed');
      const chunk = mode === 'bad-count' ? data({ usage: { ...usage, total_tokens: 1 } }) : data({ choices: [{ finish_reason: 'stop' }] });
      return new Response(chunk + 'data: [DONE]\n\n', { headers: { 'Content-Type': mode === 'non-sse' ? 'application/json' : 'text/event-stream' } });
    }, authority(events));
    if (mode === 'network') await assert.rejects(fetch(input(payload())));
    else await (await fetch(input(payload()))).text();
    assert.equal(events.at(-1).value.state, 'unknown');
    assert.equal(events.at(-1).value.tokens.total, null);
  }
});

test('SSE chunk boundaries and absent cache information preserve unknown fields', () => {
  const observer = usageObserver();
  const bytes = Buffer.from(data({ choices: [{ delta: { content: '狐獴' } }] }) + data({ usage: { prompt_tokens: 4, completion_tokens: 1, total_tokens: 5 } }) + 'data: [DONE]\r\n');
  for (const byte of bytes) observer.push(new Uint8Array([byte]));
  assert.deepEqual(observer.result(), { terminal: true, tokens: { input: null, output: 1, cacheRead: null, cacheWrite: null, total: 5 } });
});

test('a disconnected stream retains partial usage without declaring completion', async () => {
  const events = [];
  let sent = false;
  const stream = new ReadableStream({ pull(controller) {
    if (!sent) { sent = true; controller.enqueue(new TextEncoder().encode(data({ usage }))); }
    else controller.error(new Error('connection lost'));
  } });
  const fetch = createGatedFetch(config, async () => new Response(stream, { headers: { 'Content-Type': 'text/event-stream' } }), authority(events));
  await assert.rejects((await fetch(input(payload()))).text());
  assert.equal(events.at(-1).value.state, 'unknown');
  assert.equal(events.at(-1).value.terminal, false);
  assert.equal(events.at(-1).value.tokens.total, 120);
});

test('abort before begin refunds only an unsent reservation', async () => {
  const c = new AbortController(), events = [];
  const fetch = createGatedFetch(config, () => { assert.fail('network sent'); }, authority(events, path => { if (path === '/reserve') c.abort(); }));
  await assert.rejects(fetch(input(payload()), { signal: c.signal }));
  assert.deepEqual(events.map(e => e.path), ['/reserve', '/settle']);
  assert.equal(events[1].value.state, 'canceled');
});

test('each genuine fetch has its own reservation', async () => {
  const events = [];
  const fetch = createGatedFetch(config, async () => new Response(data({ choices: [{ finish_reason: 'stop' }], usage }) + 'data: [DONE]\n\n', { headers: { 'Content-Type': 'text/event-stream' } }), authority(events));
  for (let i = 0; i < 2; i++) await (await fetch(input(payload()))).text();
  assert.equal(new Set(events.filter(e => e.path === '/reserve').map(e => e.value.id)).size, 2);
});

test('version and all-provider wrapping block alternate compaction routes', async () => {
  let start; const calls = [], ready = [];
  const model = { id: config.model, provider: config.provider, api: 'openai-completions' };
  const providers = new Map(['fixture', 'alternate'].map(id => [id, { id, stream: (_m, _c, o) => calls.push(o), streamSimple: (_m, _c, o) => calls.push(o) }]));
  const ctx = { model, modelRegistry: { getAll: () => [model, { provider: 'alternate' }], getProvider: id => providers.get(id), registerProvider: p => providers.set(p.id, p) } };
  await installBridge({ on: (_e, fn) => start = fn }, '0.99.1', config, async (_c, _p, v) => ready.push(v));
  await start({}, ctx);
  assert.equal(ready[0].installed, true);
  providers.get('fixture').stream(model, {}, { maxTokens: 20000, maxRetries: 4, fetch: () => {} });
  assert.equal(calls[0].maxTokens, 8192);
  assert.equal(calls[0].maxRetries, 0);
  assert.equal(calls[0].transport, 'sse');
  assert.throws(() => providers.get('alternate').stream({ ...model, provider: 'alternate' }, {}));
  await installBridge({ on: (_e, fn) => start = fn }, 'different-version', config, async (_c, _p, v) => ready.push(v));
  await start({}, ctx);
  assert.equal(ready.at(-1).installed, false);
});

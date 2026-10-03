// Thin trusted Pi HTTP bridge. Go owns policy, reservations and settlement.
import { createHash, randomUUID } from 'node:crypto';
import { request as httpRequest } from 'node:http';

const VERSION = 'pi-http-v1';
const MAX_BYTES = 4 * 1024 * 1024;
const digest = value => createHash('sha256').update(value).digest('hex');
const emptyTokens = () => ({ input: null, output: null, cacheRead: null, cacheWrite: null, total: null });
const count = value => Number.isSafeInteger(value) && value >= 0 && value <= 1e12;
const failure = () => new Error('meerkat_request_budget_unavailable');

export function channel(config, path, value) {
  return new Promise((resolve, reject) => {
    const body = JSON.stringify(value);
    const req = httpRequest({ socketPath: config.socket, path, method: 'POST',
      headers: { Authorization: `Bearer ${config.token}`, 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) }, timeout: 5000 }, res => {
      let raw = '';
      res.setEncoding('utf8');
      res.on('data', chunk => { raw += chunk; if (raw.length > 8192) { res.destroy(); reject(failure()); } });
      res.on('error', () => reject(failure()));
      res.on('end', () => {
        if (res.statusCode !== 200) { reject(failure()); return; }
        try { resolve(JSON.parse(raw)); } catch { reject(failure()); }
      });
    });
    req.on('timeout', () => req.destroy(failure()));
    req.on('error', () => reject(failure()));
    req.end(body);
  });
}

export function inspectPayload(raw, config) {
  if (Buffer.byteLength(raw) > MAX_BYTES) throw failure();
  let payload;
  try { payload = JSON.parse(raw); } catch { throw failure(); }
  if (!payload || payload.model !== config.model || payload.stream !== true || !Array.isArray(payload.messages) || payload.messages.length === 0) throw failure();
  for (const message of payload.messages) {
    if (!message || message.audio || message.content && typeof message.content !== 'string' && !Array.isArray(message.content)) throw failure();
    if (Array.isArray(message.content) && message.content.some(part => !part || part.type !== 'text' || typeof part.text !== 'string')) throw failure();
  }
  if (payload.modalities?.some(v => v !== 'text')) throw failure();
  const fields = ['max_tokens', 'max_completion_tokens'].filter(key => payload[key] !== undefined);
  if (fields.length !== 1 || !count(payload[fields[0]]) || payload[fields[0]] < 1 || payload.n !== undefined && payload.n !== 1) throw failure();
  return { payload, field: fields[0], inputEstimate: Buffer.byteLength(raw) + 1024, maxOutput: payload[fields[0]], digest: digest(raw) };
}

// Observe only structural termination and raw provider usage, retaining no text.
export function usageObserver() {
  let buffer = '', terminal = false, bad = false, tokens = emptyTokens();
  const decoder = new TextDecoder('utf-8', { fatal: true });
  const line = text => {
    if (!text.startsWith('data:')) return;
    const data = text.slice(5).trim();
    if (data === '[DONE]') { terminal = true; return; }
    if (!data) return;
    let value;
    try { value = JSON.parse(data); } catch { bad = true; return; }
    if (value.choices?.some(choice => choice.finish_reason != null)) terminal = true;
    const u = value.usage;
    if (!u) return;
    if (!count(u.prompt_tokens) || !count(u.completion_tokens) || !count(u.total_tokens) || u.total_tokens !== u.prompt_tokens + u.completion_tokens || tokens.total !== null && u.total_tokens < tokens.total) { bad = true; return; }
    const cached = u.prompt_tokens_details?.cached_tokens;
    if (cached !== undefined && (!count(cached) || cached > u.prompt_tokens)) { bad = true; return; }
    tokens = { input: cached === undefined ? null : u.prompt_tokens - cached, output: u.completion_tokens,
      cacheRead: cached ?? null, cacheWrite: null, total: u.total_tokens };
  };
  return {
    push(bytes) {
      try { buffer += decoder.decode(bytes, { stream: true }); } catch { bad = true; buffer = ''; return; }
      if (buffer.length > 1024 * 1024) { bad = true; buffer = ''; return; }
      let pos;
      while ((pos = buffer.indexOf('\n')) >= 0) { line(buffer.slice(0, pos).replace(/\r$/, '')); buffer = buffer.slice(pos + 1); }
    },
    result() {
      try { buffer += decoder.decode(); } catch { bad = true; }
      if (buffer) { line(buffer); buffer = ''; }
      return { tokens: bad ? emptyTokens() : tokens, terminal: terminal && !bad };
    },
  };
}

export function createGatedFetch(config, baseFetch = globalThis.fetch, communicate = channel) {
  return async (input, init) => {
    const original = new Request(input, init);
    if (original.method !== 'POST' || original.signal.aborted) throw failure();
    const raw = await original.text();
    const inspected = inspectPayload(raw, config);
    const id = randomUUID();
    // No retry of a lost reserve/begin reply: the Go ledger retains uncertainty.
    const grant = await communicate(config, '/reserve', { id, digest: inspected.digest, api: 'openai-completions',
      provider: config.provider, model: config.model, inputEstimate: inspected.inputEstimate, maxOutput: inspected.maxOutput });
    if (grant.id !== id || !count(grant.maxOutput) || grant.maxOutput < 1 || grant.maxOutput > inspected.maxOutput || grant.maxOutput > 8192 || grant.reservedTokens !== inspected.inputEstimate + grant.maxOutput) throw failure();
    inspected.payload[inspected.field] = grant.maxOutput;
    const body = JSON.stringify(inspected.payload);
    if (original.signal.aborted) {
      await communicate(config, '/settle', { id, state: 'canceled', tokens: emptyTokens(), terminal: false });
      throw failure();
    }
    await communicate(config, '/begin', { id, digest: digest(body) });
    const observation = usageObserver();
    let finalized = false;
    const settle = async () => {
      if (finalized) return;
      finalized = true;
      const { tokens, terminal } = observation.result();
      await communicate(config, '/settle', { id, state: terminal && tokens.total !== null ? 'settled' : 'unknown', tokens, terminal });
    };
    let response;
    try {
      // Rewrite the actual request so sampling parameters cannot bypass the cap.
      const headers = new Headers(original.headers);
      headers.delete('content-length');
      response = await baseFetch(new Request(original.url, { method: 'POST', headers, body, signal: original.signal }));
    } catch {
      await settle();
      throw failure();
    }
    if (!response.ok || !response.body || !response.headers.get('content-type')?.includes('text/event-stream')) {
      await settle();
      return response;
    }
    const reader = response.body.getReader();
    const stream = new ReadableStream({
      async pull(controller) {
        try {
          const next = await reader.read();
          if (next.done) { await settle(); controller.close(); return; }
          observation.push(next.value);
          controller.enqueue(next.value);
        } catch { try { await settle(); } catch {} controller.error(failure()); }
      },
      async cancel() { try { await reader.cancel(); } finally { await settle(); } },
    });
    return new Response(stream, { status: response.status, statusText: response.statusText, headers: response.headers });
  };
}

export async function installBridge(pi, piVersion, config, communicate = channel) {
  let installed = false;
  pi.on('session_start', async (_event, ctx) => {
    const selected = ctx.model;
    if (installed || piVersion !== '0.99.1' || config.version !== VERSION || !selected || selected.provider !== config.provider || selected.id !== config.model || selected.api !== 'openai-completions') {
      await communicate(config, '/ready', { version: VERSION, piVersion, provider: config.provider, model: config.model, api: selected?.api ?? '', installed: false });
      return;
    }
    const ids = new Set(ctx.modelRegistry.getAll().map(model => model.provider));
    ids.add(config.provider);
    for (const id of ids) {
      const provider = ctx.modelRegistry.getProvider(id);
      if (!provider || typeof provider.stream !== 'function' || typeof provider.streamSimple !== 'function') throw failure();
      const wrap = method => (model, context, options = {}) => {
        if (model.provider !== config.provider || model.id !== config.model || model.api !== 'openai-completions') throw failure();
        return provider[method](model, context, { ...options, maxTokens: Math.min(options.maxTokens ?? 8192, 8192),
          transport: 'sse', fetch: createGatedFetch(config), maxRetries: 0 });
      };
      // Block alternate compaction/fallback providers from bypassing the gate.
      ctx.modelRegistry.registerProvider({ ...provider, stream: wrap('stream'), streamSimple: wrap('streamSimple'),
        generateImages: () => { throw failure(); }, classify: () => { throw failure(); } });
    }
    installed = true;
    await communicate(config, '/ready', { version: VERSION, piVersion, provider: selected.provider, model: selected.id, api: selected.api, installed: true });
  });
}

export default async function (pi, piVersion) {
  let config;
  try { config = JSON.parse(process.env.MEERKAT_PRIVATE_BUDGET ?? ''); } catch { throw failure(); }
  if (!config.socket || !config.token || config.version !== VERSION) throw failure();
  await installBridge(pi, piVersion, config);
}

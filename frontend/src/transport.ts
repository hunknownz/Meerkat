import type { LegacyActive, Snapshot, SettingsInput, WorkflowEnvelope } from './generated/workflow';
import { validateEnvelope, validateSettingsInput } from './generated/validate.js';

export interface SnapshotUpdate { snapshot: Snapshot; legacyActive: LegacyActive[] }
export interface StopAccepted { accepted: true; requestId: string }

/** Host-agnostic data/action channel. Shared App never calls fetch directly. */
export interface Transport {
  readonly readonly: boolean;
  read(): Promise<SnapshotUpdate>;
  /** onUpdate receives full snapshots; onStale is called immediately when the live channel fails. Returns unsubscribe. */
  subscribe(onUpdate: (u: SnapshotUpdate) => void, onStale: (message: string) => void): () => void;
  stop(runId: string, requestId: string): Promise<StopAccepted>;
  settings(input: SettingsInput): Promise<void>;
}

export class ContractError extends Error {}

/** Runtime contract check against contracts/workflow.schema.json (standalone Ajv). */
export function parseEnvelope(body: unknown): WorkflowEnvelope {
  if (!validateEnvelope(body)) {
    const e = validateEnvelope.errors?.[0];
    throw new ContractError(`快照不符合契约${e ? `：${e.instancePath || '/'} ${e.message ?? ''}` : ''}`);
  }
  return body as WorkflowEnvelope;
}

export function assertSettingsInput(input: unknown): asserts input is SettingsInput {
  if (!validateSettingsInput(input)) throw new ContractError('设置输入无效');
}

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
export const isUuid = (s: string): boolean => UUID_RE.test(s);

interface BrowserDeps {
  fetch?: typeof fetch;
  EventSource?: typeof EventSource;
  timeoutMs?: number;
  base?: string;
}

/**
 * Same-origin browser transport. The session write token is kept in a private field and
 * never exposed to the UI. Every fetch has a 3 s timeout. SSE events only trigger a full
 * snapshot fetch (one in flight; a request arriving meanwhile coalesces into one follow-up).
 */
export class BrowserTransport implements Transport {
  readonly readonly = false;
  #token = '';
  #fetch: typeof fetch;
  #ES: typeof EventSource | undefined;
  #timeout: number;
  #base: string;
  #closed = new AbortController();

  constructor(deps: BrowserDeps = {}) {
    this.#fetch = deps.fetch ?? globalThis.fetch.bind(globalThis);
    this.#ES = deps.EventSource ?? globalThis.EventSource;
    this.#timeout = deps.timeoutMs ?? 3000;
    this.#base = deps.base ?? '';
  }

  async #request(path: string, init: RequestInit = {}): Promise<unknown> {
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(new Error('请求超时（3 秒）')), this.#timeout);
    const onClose = () => ctl.abort(new Error('已关闭'));
    this.#closed.signal.addEventListener('abort', onClose);
    try {
      const res = await this.#fetch(this.#base + path, { ...init, signal: ctl.signal, credentials: 'same-origin', cache: 'no-store' });
      const body: unknown = await res.json().catch(() => null);
      if (!res.ok) {
        const msg = body && typeof body === 'object' && 'error' in body && typeof body.error === 'string' ? body.error : `HTTP ${res.status}`;
        throw new Error(msg.slice(0, 200));
      }
      return body;
    } catch (e) {
      if (ctl.signal.aborted) throw ctl.signal.reason instanceof Error ? ctl.signal.reason : new Error('请求已中止');
      throw e;
    } finally {
      clearTimeout(timer);
      this.#closed.signal.removeEventListener('abort', onClose);
    }
  }

  async read(): Promise<SnapshotUpdate> {
    const env = parseEnvelope(await this.#request('/api/workflow'));
    this.#token = env.sessionToken;
    return { snapshot: env.data, legacyActive: env.legacyActive ?? [] };
  }

  subscribe(onUpdate: (u: SnapshotUpdate) => void, onStale: (message: string) => void): () => void {
    let active = true;
    let inflight: Promise<void> | null = null;
    let again = false;
    const refresh = (): Promise<void> => {
      if (!active) return Promise.resolve();
      if (inflight) { again = true; return inflight; }
      inflight = this.read()
        .then((u) => { if (active) onUpdate(u); })
        .catch((e: unknown) => { if (active) onStale(e instanceof Error ? e.message : '读取失败'); })
        .finally(() => {
          inflight = null;
          if (again && active) { again = false; void refresh(); }
        });
      return inflight;
    };
    let es: EventSource | null = null;
    const onMessage = () => { void refresh(); };
    const onOpen = () => { void refresh(); }; // initial and every reconnect → full snapshot
    const onError = () => { if (active) onStale('实时连接中断'); };
    if (this.#ES) {
      es = new this.#ES(`${this.#base}/api/workflow/events`);
      es.addEventListener('message', onMessage);
      es.addEventListener('snapshot', onMessage);
      es.addEventListener('open', onOpen);
      es.addEventListener('error', onError);
    }
    void refresh();
    return () => {
      active = false;
      if (es) {
        es.removeEventListener('message', onMessage);
        es.removeEventListener('snapshot', onMessage);
        es.removeEventListener('open', onOpen);
        es.removeEventListener('error', onError);
        es.close();
      }
    };
  }

  #write(method: string, path: string, body: unknown): Promise<unknown> {
    if (!this.#token) return Promise.reject(new Error('尚未取得会话令牌，请先重新连接'));
    return this.#request(path, { method, headers: { 'Content-Type': 'application/json', 'X-Meerkat-Token': this.#token }, body: JSON.stringify(body) });
  }

  async stop(runId: string, requestId: string): Promise<StopAccepted> {
    if (!isUuid(runId) || !isUuid(requestId)) throw new Error('无效的运行 ID');
    await this.#write('POST', `/api/workflow/runs/${encodeURIComponent(runId)}/stop`, { requestId });
    return { accepted: true, requestId };
  }

  async settings(input: SettingsInput): Promise<void> {
    assertSettingsInput(input);
    await this.#write('PUT', '/api/workflow/settings', input);
  }

  /** Aborts in-flight requests; subscriptions must be unsubscribed by their owners. */
  close(): void {
    this.#closed.abort();
    this.#token = '';
  }
}

export function newRequestId(): string {
  const c = globalThis.crypto;
  if (c && typeof c.randomUUID === 'function') return c.randomUUID();
  const b = new Uint8Array(16);
  c.getRandomValues(b);
  b[6] = (b[6]! & 0x0f) | 0x40;
  b[8] = (b[8]! & 0x3f) | 0x80;
  const h = [...b].map((x) => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

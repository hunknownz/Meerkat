import { afterEach, describe, expect, it, vi } from 'vitest';
import { BrowserTransport, ContractError, parseEnvelope } from '../transport';
import { RUN_A, envelope, snapshot } from './fixtures';

class FakeES {
  static last: FakeES | null = null;
  listeners = new Map<string, Set<() => void>>();
  closed = false;
  constructor(public url: string) { FakeES.last = this; }
  addEventListener(t: string, fn: () => void) { if (!this.listeners.has(t)) this.listeners.set(t, new Set()); this.listeners.get(t)!.add(fn); }
  removeEventListener(t: string, fn: () => void) { this.listeners.get(t)?.delete(fn); }
  emit(t: string) { for (const fn of this.listeners.get(t) ?? []) fn(); }
  close() { this.closed = true; }
  count() { let n = 0; for (const s of this.listeners.values()) n += s.size; return n; }
}
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const flush = () => new Promise((r) => setTimeout(r, 0));

afterEach(() => { vi.useRealTimers(); });

describe('contract', () => {
  it('accepts a snapshot with unknown/missing usage fields', () => {
    expect(parseEnvelope(envelope()).data.runs[0]!.usage!.tokens!.output).toBeNull();
  });
  it('rejects context text, unsafe profile fields and ok:false', () => {
    const s = snapshot();
    expect(() => parseEnvelope(envelope({ ...s, contexts: [{ ...s.contexts[0]!, text: 'secret' } as never] }))).toThrow(ContractError);
    expect(() => parseEnvelope(envelope({ ...s, profiles: [{ ...s.profiles[0]!, authEnv: 'X' } as never] }))).toThrow(ContractError);
    expect(() => parseEnvelope({ ...envelope(), ok: false })).toThrow(ContractError);
    expect(() => parseEnvelope({ ...envelope(), data: { ...s, runs: [{ id: RUN_A, taskId: 't1', role: 'developer' }] } })).toThrow(ContractError);
  });
});

describe('BrowserTransport', () => {
  it('fetches a full snapshot initially and per SSE event, one in flight, stale immediately on error, cleans up', async () => {
    let resolve: ((r: Response) => void) | null = null;
    const fetchFn = vi.fn((_u: RequestInfo | URL, _i?: RequestInit) => new Promise<Response>((r) => { resolve = r; }));
    const t = new BrowserTransport({ fetch: fetchFn as typeof fetch, EventSource: FakeES as never });
    const onUpdate = vi.fn();
    const onStale = vi.fn();
    const unsub = t.subscribe(onUpdate, onStale);
    expect(fetchFn).toHaveBeenCalledTimes(1);
    expect(fetchFn.mock.calls[0]![0]).toBe('/api/workflow');
    const es = FakeES.last!;
    es.emit('message'); es.emit('message'); es.emit('open');
    expect(fetchFn).toHaveBeenCalledTimes(1); // coalesced while in flight
    resolve!(json(envelope()));
    await flush(); await flush();
    expect(onUpdate).toHaveBeenCalledTimes(1);
    expect(fetchFn).toHaveBeenCalledTimes(2); // one follow-up for coalesced events
    resolve!(json(envelope()));
    await flush(); await flush();
    es.emit('error');
    expect(onStale).toHaveBeenCalledWith('实时连接中断');
    unsub();
    expect(es.closed).toBe(true);
    expect(es.count()).toBe(0);
  });

  it('times out after 3 s and reports stale', async () => {
    vi.useFakeTimers();
    const fetchFn = vi.fn((_u: RequestInfo | URL, i?: RequestInit) => new Promise<Response>((_r, rej) => {
      i!.signal!.addEventListener('abort', () => rej(new DOMException('aborted', 'AbortError')));
    }));
    const t = new BrowserTransport({ fetch: fetchFn as typeof fetch, EventSource: undefined as never });
    const p = t.read();
    const assertion = expect(p).rejects.toThrow('超时');
    await vi.advanceTimersByTimeAsync(3000);
    await assertion;
  });

  it('keeps the token private and sends stop with a requestId body; validates settings', async () => {
    const fetchFn = vi.fn(async (_u: RequestInfo | URL, _i?: RequestInit) => json(envelope()));
    const t = new BrowserTransport({ fetch: fetchFn as typeof fetch });
    await expect(t.stop(RUN_A, RUN_A)).rejects.toThrow('令牌');
    await t.read();
    expect(JSON.stringify(t)).not.toContain('tok-secret');
    fetchFn.mockResolvedValueOnce(json({ ok: true }, 202));
    const reqId = '33333333-3333-4333-8333-333333333333';
    await expect(t.stop(RUN_A, reqId)).resolves.toEqual({ accepted: true, requestId: reqId });
    const [url, init] = fetchFn.mock.calls[1]!;
    expect(url).toBe(`/api/workflow/runs/${RUN_A}/stop`);
    expect(init!.method).toBe('POST');
    expect(JSON.parse(init!.body as string)).toEqual({ requestId: reqId });
    expect((init!.headers as Record<string, string>)['X-Meerkat-Token']).toBe('tok-secret');
    await expect(t.settings({ maxConcurrency: 9 })).rejects.toThrow('设置');
    await expect(t.settings({ repoPath: '/x' } as never)).rejects.toThrow('设置');
  });
});

import type { SettingsInput } from './generated/workflow';
import { ControlActionError, parseControlReceipt, parseEnvelope, type InterventionActions, type SnapshotUpdate, type StopAccepted, type Transport } from './transport';

/** Subset of a tools/call result the monitor understands. Full snapshots travel in `_meta.snapshot` only. */
export interface McpToolResult {
  isError?: boolean;
  content?: unknown;
  structuredContent?: unknown;
  _meta?: { snapshot?: unknown; legacyActive?: unknown; [key: string]: unknown };
}

/** Typed host bridge (implemented by the ext-apps SDK `App`). The transport never touches the network itself. */
export interface McpBridge {
  callServerTool(
    params: { name: string; arguments: Record<string, unknown> },
    options?: { signal?: AbortSignal; timeout?: number },
  ): Promise<McpToolResult>;
}

export interface McpTransportOptions {
  /** Polling interval for app-only get_monitor_snapshot (default 4 s). */
  pollMs?: number;
  /** Bound for every host tool call (default 3 s). */
  timeoutMs?: number;
  /** Wait for the initial open_monitor result after connecting before requesting a snapshot (default 1.5 s). */
  initialWaitMs?: number;
}

export const SNAPSHOT_TOOL = 'get_monitor_snapshot';
const READONLY_MSG = '设置保持只读；请使用 Agent 干预入口停止运行';

const errText = (e: unknown, fallback: string): string => String(e instanceof Error && e.message ? e.message : fallback).slice(0, 200);

function toolErrorText(r: McpToolResult): string {
  if (Array.isArray(r.content)) {
    for (const c of r.content as unknown[]) {
      if (c && typeof c === 'object' && (c as { type?: unknown }).type === 'text' && typeof (c as { text?: unknown }).text === 'string') {
        return (c as { text: string }).text.slice(0, 200);
      }
    }
  }
  return '宿主工具调用失败';
}

/** Validates a host tool result through the same runtime snapshot contract as the browser. Never consumes tokens. */
export function parseToolResult(result: unknown): SnapshotUpdate {
  if (!result || typeof result !== 'object') throw new Error('宿主返回的结果无效');
  const r = result as McpToolResult;
  if (r.isError) throw new Error(toolErrorText(r));
  const meta = r._meta;
  if (!meta || typeof meta !== 'object' || meta.snapshot === undefined) throw new Error('宿主结果缺少快照');
  const env = parseEnvelope({ ok: true, data: meta.snapshot, legacyActive: meta.legacyActive ?? [], sessionToken: 'host' });
  return { snapshot: env.data, legacyActive: env.legacyActive ?? [] };
}

interface Listener { onUpdate: (u: SnapshotUpdate) => void; onStale: (message: string) => void }

/**
 * Read-only transport over the MCP Apps host bridge. One poll loop is shared by all
 * subscribers; at most one host request is in flight and every request is bounded.
 * Failures mark the view stale while keeping the last validated snapshot.
 */
export class McpTransport implements Transport {
  readonly readonly = true;
  readonly intervention: InterventionActions = {
    send: async (input) => parseControlReceipt(await this.#control('send_run_instruction', { ...input }), input.requestId, input.runId, input.sessionId),
    followUp: async (input) => parseControlReceipt(await this.#control('queue_follow_up_from_ui', {...input}), input.requestId, input.runId, input.sessionId, 'follow_up'),
    pause: async (input) => parseControlReceipt(await this.#control('pause_run_from_ui', {...input}), input.requestId, input.runId, input.sessionId, 'pause'),
    receipt: async (requestId) => parseControlReceipt(await this.#control('get_intervention_receipt', { requestId }), requestId),
    stop: async (runId, requestId) => parseControlReceipt(await this.#control('stop_run_from_ui', { runId, requestId }), requestId, runId),
  };
  #bridge: McpBridge;
  #pollMs: number;
  #timeoutMs: number;
  #initialWaitMs: number;
  #listeners = new Set<Listener>();
  #last: SnapshotUpdate | null = null;
  #stale: string | null = null;
  #connected = false;
  #closed = false;
  #timer: ReturnType<typeof setTimeout> | null = null;
  #inflight: Promise<SnapshotUpdate> | null = null;
  #pending = new Set<AbortController>();

  constructor(bridge: McpBridge, opts: McpTransportOptions = {}) {
    this.#bridge = bridge;
    this.#pollMs = opts.pollMs ?? 4000;
    this.#timeoutMs = opts.timeoutMs ?? 3000;
    this.#initialWaitMs = opts.initialWaitMs ?? 1500;
  }

  get closed(): boolean { return this.#closed; }

  /** Feeds a host-pushed tool result (open_monitor / get_monitor_snapshot). Invalid results mark the view stale. */
  pushToolResult(result: unknown): void {
    if (this.#closed) return;
    try {
      this.#accept(parseToolResult(result));
    } catch (e) {
      this.markStale(errText(e, '快照无效'));
      return;
    }
    // A fresh result counts as the latest refresh; push the next poll back a full interval.
    if (this.#timer !== null) this.#schedule(this.#pollMs);
  }

  /** Called once the SDK handshake (ui/initialize) has completed; polling may start. */
  setConnected(): void {
    if (this.#closed || this.#connected) return;
    this.#connected = true;
    if (this.#listeners.size) this.#schedule(this.#last ? this.#pollMs : this.#initialWaitMs);
  }

  /** Host disconnect / bridge error: keep last data, mark stale; the next successful refresh recovers. */
  markStale(message: string): void {
    if (this.#closed) return;
    this.#stale = String(message || '连接中断').slice(0, 200);
    for (const l of [...this.#listeners]) l.onStale(this.#stale);
  }

  read(): Promise<SnapshotUpdate> {
    if (this.#closed) return Promise.reject(new Error('已关闭'));
    if (!this.#connected) return Promise.reject(new Error('尚未连接宿主'));
    if (this.#inflight) return this.#inflight;
    const p = this.#call().finally(() => { if (this.#inflight === p) this.#inflight = null; });
    this.#inflight = p;
    return p.then((u) => { this.#accept(u); return u; });
  }

  subscribe(onUpdate: (u: SnapshotUpdate) => void, onStale: (message: string) => void): () => void {
    if (this.#closed) return () => {};
    const l: Listener = { onUpdate, onStale };
    this.#listeners.add(l);
    if (this.#last) onUpdate(this.#last);
    if (this.#stale) onStale(this.#stale);
    if (this.#connected && this.#timer === null) this.#schedule(this.#last ? this.#pollMs : this.#initialWaitMs);
    return () => {
      if (!this.#listeners.delete(l)) return;
      if (!this.#listeners.size) this.#clearTimer();
    };
  }

  stop(_runId: string, _requestId: string): Promise<StopAccepted> { return Promise.reject(new Error(READONLY_MSG)); }
  settings(_input: SettingsInput): Promise<void> { return Promise.reject(new Error(READONLY_MSG)); }

  /** Teardown: stops polling, aborts pending requests and drops every listener. Idempotent. */
  close(): void {
    if (this.#closed) return;
    this.#closed = true;
    this.#clearTimer();
    this.#listeners.clear();
    for (const c of this.#pending) c.abort(new Error('已关闭'));
    this.#pending.clear();
    this.#inflight = null;
  }

  #accept(u: SnapshotUpdate): void {
    if (this.#closed) return;
    this.#last = u;
    this.#stale = null;
    for (const l of [...this.#listeners]) l.onUpdate(u);
  }

  #call(): Promise<SnapshotUpdate> {
    return this.#tool(SNAPSHOT_TOOL, {}).then(parseToolResult);
  }

  async #control(name: string, args: Record<string, unknown>): Promise<unknown> {
    if (!this.#connected || this.#closed) throw new Error('宿主未连接，发送结果未知');
    const r = await this.#tool(name, args);
    if (r.isError) {
      const status = (r.structuredContent as { status?: unknown })?.status;
      throw new ControlActionError(status === 'rejected' || status === 'not_sent' ? status : 'unknown', '未取得有效回执，请查询原请求');
    }
    return r._meta?.receipt;
  }

  #tool(name: string, args: Record<string, unknown>): Promise<McpToolResult> {
    const ctl = new AbortController();
    this.#pending.add(ctl);
    let timer: ReturnType<typeof setTimeout> | undefined;
    const aborted = new Promise<never>((_, reject) => {
      const fail = () => reject(ctl.signal.reason instanceof Error ? ctl.signal.reason : new Error('请求已中止'));
      ctl.signal.addEventListener('abort', fail, { once: true });
      timer = setTimeout(() => ctl.abort(new Error(`请求超时（${Math.round(this.#timeoutMs / 1000)} 秒）`)), this.#timeoutMs);
    });
    aborted.catch(() => {});
    const req = Promise.resolve()
      .then(() => this.#bridge.callServerTool({ name, arguments: args }, { signal: ctl.signal, timeout: this.#timeoutMs }));
    return Promise.race([req, aborted])
      .then((r) => {
        if (this.#closed) throw new Error('已关闭');
        return r;
      })
      .finally(() => { clearTimeout(timer); this.#pending.delete(ctl); });
  }

  #poll(): void {
    this.#timer = null;
    if (this.#closed || !this.#connected || !this.#listeners.size) return;
    this.read()
      .catch((e: unknown) => { this.markStale(errText(e, '读取失败')); })
      .finally(() => { if (!this.#closed && this.#listeners.size && this.#timer === null) this.#schedule(this.#pollMs); });
  }

  #schedule(ms: number): void {
    this.#clearTimer();
    if (this.#closed) return;
    this.#timer = setTimeout(() => this.#poll(), ms);
  }

  #clearTimer(): void {
    if (this.#timer !== null) clearTimeout(this.#timer);
    this.#timer = null;
  }
}

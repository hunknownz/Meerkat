import { afterEach, expect, it, vi } from 'vitest';
import { McpTransport, parseToolResult } from '../mcp-transport';
import type { SnapshotUpdate } from '../transport';
import { snapshot } from './fixtures';

const result = () => ({ _meta: { snapshot: snapshot(), legacyActive: [] } });
afterEach(() => vi.useRealTimers());

it('renders the initial host result without a refetch and never sends writes', async () => {
  vi.useFakeTimers();
  const callServerTool = vi.fn(async (_params: { name: string; arguments: Record<string, unknown> }) => result());
  const t = new McpTransport({ callServerTool });
  const update = vi.fn();
  t.subscribe(update, vi.fn());
  t.pushToolResult(result());
  t.setConnected();
  expect(update).toHaveBeenCalledTimes(1);
  expect(callServerTool).not.toHaveBeenCalled();
  await expect(t.stop('run', 'request')).rejects.toThrow('只读');
  await expect(t.settings({ maxConcurrency: 1 })).rejects.toThrow('只读');
  expect(callServerTool).not.toHaveBeenCalled();
  await vi.advanceTimersByTimeAsync(4000);
  expect(callServerTool).toHaveBeenCalledTimes(1);
  expect(callServerTool.mock.calls[0]![0]).toEqual({ name: 'get_monitor_snapshot', arguments: {} });
  t.close();
  await vi.advanceTimersByTimeAsync(20000);
  expect(callServerTool).toHaveBeenCalledTimes(1);
});

it('rejects malformed host data, retains last snapshot and recovers with a full snapshot', async () => {
  const t = new McpTransport({ callServerTool: async () => result() });
  const update = vi.fn();
  const stale = vi.fn();
  const unsub = t.subscribe(update, stale);
  t.pushToolResult(result());
  t.pushToolResult({ _meta: { snapshot: { schemaVersion: 99 } } });
  expect(update).toHaveBeenCalledTimes(1);
  expect(stale).toHaveBeenCalledTimes(1);
  t.setConnected();
  await t.read();
  expect(update).toHaveBeenCalledTimes(2);
  unsub();
  t.close();
  expect(() => parseToolResult({ structuredContent: snapshot() })).toThrow('缺少快照');
  expect(() => parseToolResult({ isError: true, content: [{ type: 'text', text: 'Unavailable' }] })).toThrow('Unavailable');
});

it('coalesces reads, bounds a stalled bridge and suppresses late results after teardown', async () => {
  vi.useFakeTimers();
  let resolve!: (v: ReturnType<typeof result>) => void;
  const callServerTool = vi.fn(() => new Promise<ReturnType<typeof result>>(r => { resolve = r; }));
  const t = new McpTransport({ callServerTool });
  const update = vi.fn();
  t.subscribe(update, vi.fn());
  t.setConnected();
  const first = t.read();
  const second = t.read();
  const firstCheck = expect(first).rejects.toThrow('超时');
  const secondCheck = expect(second).rejects.toThrow('超时');
  await vi.advanceTimersByTimeAsync(3000);
  await Promise.all([firstCheck, secondCheck]);
  expect(callServerTool).toHaveBeenCalledTimes(1);
  t.close();
  resolve(result());
  await Promise.resolve();
  expect(update).not.toHaveBeenCalled();
  await expect(t.read()).rejects.toThrow('已关闭');
  await vi.advanceTimersByTimeAsync(20000);
  expect(callServerTool).toHaveBeenCalledTimes(1);
});

const revision = 'a'.repeat(64);
const revised = (s = snapshot(), rev = revision) => ({ _meta: { snapshot: s, snapshotRevision: rev, legacyActive: [] } });
const unchanged = (rev = revision) => ({ _meta: {
  snapshotUnchanged: true, snapshotRevision: rev, observedAt: '2025-01-01T00:11:00Z',
  controller: { state: 'running', heartbeatAt: '2025-01-01T00:10:59Z' },
} });

it('refreshes freshness with a small unchanged reply and reuses the validated history', async () => {
  const callServerTool = vi.fn(async (_params: { name: string; arguments: Record<string, unknown> }) => unchanged());
  const t = new McpTransport({ callServerTool });
  const updates: SnapshotUpdate[] = [];
  t.subscribe((u) => updates.push(u), vi.fn());
  t.pushToolResult(revised());
  t.setConnected();
  await t.read();
  expect(callServerTool.mock.calls[0]![0]).toEqual({ name: 'get_monitor_snapshot', arguments: { sinceRevision: revision } });
  expect(updates[1]!.snapshot.observedAt).toBe('2025-01-01T00:11:00Z');
  expect(updates[1]!.snapshot.controller?.heartbeatAt).toBe('2025-01-01T00:10:59Z');
  for (const field of ['tasks', 'runs', 'contexts', 'profiles', 'deliveries', 'reviews'] as const) {
    expect(updates[1]!.snapshot[field]).toBe(updates[0]!.snapshot[field]);
  }
  expect(updates[1]!.snapshot.runs[0]!.usage?.estimatedCostUsd).toBeNull();
  t.close();
});

it('rejects mismatched or malformed unchanged data and asks for full state next', async () => {
  for (const bad of [unchanged('b'.repeat(64)), { _meta: { ...unchanged()._meta, observedAt: 'bad' } },
    { _meta: { ...unchanged()._meta, controller: { state: 'unknown' } } }]) {
    const callServerTool = vi.fn().mockResolvedValueOnce(bad).mockResolvedValueOnce(revised(snapshot({ counts: { running: 0, queued: 0, unknown: 1 } }), 'b'.repeat(64)));
    const t = new McpTransport({ callServerTool });
    t.pushToolResult(revised());
    t.setConnected();
    await expect(t.read()).rejects.toThrow();
    const recovered = await t.read();
    expect(callServerTool.mock.calls[1]![0]).toEqual({ name: 'get_monitor_snapshot', arguments: {} });
    expect(recovered.snapshot.counts?.unknown).toBe(1);
    t.close();
  }
  const t = new McpTransport({ callServerTool: async () => unchanged() });
  t.setConnected();
  await expect(t.read()).rejects.toThrow('快照版本未确认');
  t.close();
});

it('slows idle refresh, suspends hidden polling, and refreshes immediately when visible', async () => {
  vi.useFakeTimers();
  const idle = snapshot({ counts: { running: 0, queued: 0, unknown: 4 }, controller: { state: 'idle', heartbeatAt: null } });
  const callServerTool = vi.fn(async () => revised(idle));
  const stale = vi.fn();
  const t = new McpTransport({ callServerTool });
  t.subscribe(vi.fn(), stale);
  t.pushToolResult(revised(idle));
  t.setConnected();
  await vi.advanceTimersByTimeAsync(14999);
  expect(callServerTool).not.toHaveBeenCalled();
  await vi.advanceTimersByTimeAsync(1);
  expect(callServerTool).toHaveBeenCalledTimes(1);
  t.setVisible(false);
  await vi.advanceTimersByTimeAsync(120000);
  expect(callServerTool).toHaveBeenCalledTimes(1);
  expect(stale).toHaveBeenLastCalledWith('面板已隐藏；显示后重新确认状态。');
  t.setVisible(true);
  expect(stale).toHaveBeenLastCalledWith('正在重新确认状态。');
  await vi.advanceTimersByTimeAsync(0);
  expect(callServerTool).toHaveBeenCalledTimes(2);
  t.pushToolResult(revised(snapshot(), 'b'.repeat(64)));
  await vi.advanceTimersByTimeAsync(4000);
  expect(callServerTool).toHaveBeenCalledTimes(3);
  t.close();
});

it('keeps only one validated history across hours of unchanged refreshes without any control write', async () => {
  vi.useFakeTimers();
  const original = snapshot();
  let last: typeof original | null = null;
  let updateCount = 0;
  const callServerTool = vi.fn(async (_params: { name: string; arguments: Record<string, unknown> }) => unchanged());
  const t = new McpTransport({ callServerTool });
  t.subscribe((u) => { last = u.snapshot; updateCount++; }, vi.fn());
  t.pushToolResult(revised(original));
  const history = last!.runs;
  t.setConnected();
  for (let i = 0; i < 3600; i++) await vi.advanceTimersByTimeAsync(4000);
  expect(callServerTool).toHaveBeenCalledTimes(3600);
  expect(updateCount).toBe(3601);
  expect(last!.runs).toBe(history);
  expect(callServerTool.mock.calls.every(([p]) => p.name === 'get_monitor_snapshot')).toBe(true);
  t.close();
  await vi.advanceTimersByTimeAsync(60000);
  expect(callServerTool).toHaveBeenCalledTimes(3600);
});

it('does not assume an unknown controller or count is idle', async () => {
  vi.useFakeTimers();
  for (const s of [
    snapshot({ counts: { running: 0, queued: 0, unknown: 4 }, controller: { state: 'unknown', heartbeatAt: null } }),
    snapshot({ counts: { running: 0, queued: 0, unknown: 4 }, controller: undefined }),
    snapshot({ counts: { running: null, queued: 0, unknown: 4 }, controller: { state: 'idle', heartbeatAt: null } }),
  ]) {
    const callServerTool = vi.fn(async () => revised(s));
    const t = new McpTransport({ callServerTool });
    t.subscribe(vi.fn(), vi.fn());
    t.pushToolResult(revised(s));
    t.setConnected();
    await vi.advanceTimersByTimeAsync(4000);
    expect(callServerTool).toHaveBeenCalledTimes(1);
    t.close();
  }
});

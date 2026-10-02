import { afterEach, expect, it, vi } from 'vitest';
import { McpTransport, parseToolResult } from '../mcp-transport';
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

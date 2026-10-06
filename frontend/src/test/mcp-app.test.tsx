import { act } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { snapshot } from './fixtures';

const host = vi.hoisted(() => ({
  call: vi.fn<(...args: unknown[]) => Promise<unknown>>(),
  close: vi.fn(async () => {}),
  apps: [] as { ontoolresult?: (result: unknown) => void; onteardown?: () => Promise<unknown> }[],
}));
vi.mock('@modelcontextprotocol/ext-apps', () => ({
  App: class {
    constructor() { host.apps.push(this); }
    ontoolresult?: (result: unknown) => void;
    onteardown?: () => Promise<unknown>;
    connect() { return Promise.resolve(); }
    getHostContext() { return { theme: 'light' }; }
    callServerTool(...args: unknown[]) { return host.call(...args); }
    close() { return host.close(); }
  },
}));
import { startMcpMonitor } from '../mcp-app';

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  host.apps.length = 0;
  host.call.mockReset();
  host.close.mockClear();
  document.body.replaceChildren();
});

it('suspends a hidden native panel before handshake, refreshes on visibility, and releases mounts', async () => {
  vi.useFakeTimers();
  const visible = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
  const add = vi.spyOn(document, 'addEventListener');
  const remove = vi.spyOn(document, 'removeEventListener');
  const addWindow = vi.spyOn(window, 'addEventListener');
  const removeWindow = vi.spyOn(window, 'removeEventListener');
  host.call.mockResolvedValue({ _meta: { snapshot: snapshot(), legacyActive: [] } });
  const el = document.createElement('div');
  document.body.append(el);
  for (let i = 0; i < 3; i++) {
    let monitor!: ReturnType<typeof startMcpMonitor>;
    await act(async () => { monitor = startMcpMonitor(el); });
    const root = el.shadowRoot!;
    expect(root.textContent).toContain('面板已隐藏');
    await act(async () => { await vi.advanceTimersByTimeAsync(60000); });
    expect(host.call).toHaveBeenCalledTimes(i);
    visible.mockReturnValue('visible');
    await act(async () => {
      document.dispatchEvent(new Event('visibilitychange'));
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(host.call).toHaveBeenCalledTimes(i + 1);
    expect(root.textContent).toContain('Fix parser');
    expect(root.textContent).toContain('已连接');
    await act(async () => { await host.apps[i]!.onteardown!(); });
    expect(root.childNodes).toHaveLength(0);
    // A later visibility event and an already scheduled poll cannot revive a destroyed panel.
    await act(async () => {
      document.dispatchEvent(new Event('visibilitychange'));
      await vi.advanceTimersByTimeAsync(60000);
      host.apps[i]!.ontoolresult?.({ _meta: { snapshot: snapshot() } });
      monitor.destroy();
    });
    expect(host.call).toHaveBeenCalledTimes(i + 1);
    expect(root.childNodes).toHaveLength(0);
    visible.mockReturnValue('hidden');
  }
  const handlers = add.mock.calls.filter(([name]) => name === 'visibilitychange').map(([, fn]) => fn);
  expect(handlers).toHaveLength(3);
  for (const fn of handlers) expect(remove).toHaveBeenCalledWith('visibilitychange', fn);
  for (const [, fn] of addWindow.mock.calls.filter(([name]) => name === 'pagehide')) {
    expect(removeWindow).toHaveBeenCalledWith('pagehide', fn);
  }
});

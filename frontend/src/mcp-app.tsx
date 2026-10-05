// MCP Apps entry: the shared Agents/Tasks/Usage monitor rendered inside an MCP host via the official
// ext-apps SDK bridge. Data and bounded human controls use host tools; no fetch/EventSource.
import { App as McpApp, type McpUiHostContext } from '@modelcontextprotocol/ext-apps';
import { mount, type MountHandle } from './mount';
import { McpTransport, type McpBridge, type McpToolResult } from './mcp-transport';
import { preferredTheme } from './theme';

type Theme = 'light' | 'dark';
const READONLY_NOTE = '可在 Agent 干预入口发送指令或停止运行；此宿主不能修改设置。';

export interface McpMonitor { destroy(): void }

/** Boots the monitor in `container`. Handlers are registered before the SDK `ui/initialize` handshake. */
export function startMcpMonitor(container: Element): McpMonitor {
  const app = new McpApp({ name: 'Meerkat', version: '0.4.0-beta.16' });
  const bridge: McpBridge = {
    callServerTool: (params, options) => app.callServerTool(params, options) as Promise<McpToolResult>,
  };
  const transport = new McpTransport(bridge);
  let theme: Theme = preferredTheme();
  let handle: MountHandle | null = null;
  let destroyed = false;

  const render = () => {
    if (destroyed) return;
    handle?.destroy();
    handle = mount(container, {}, { transport, readonly: true, readonlyNote: READONLY_NOTE, theme });
  };
  const applyContext = (ctx: McpUiHostContext | undefined) => {
    const next = ctx?.theme === 'dark' || ctx?.theme === 'light' ? ctx.theme : null;
    if (!next || next === theme || destroyed) return;
    theme = next;
    render(); // recreate with the host theme; the shared transport replays the last snapshot without a refetch
  };
  const destroy = () => {
    if (destroyed) return;
    destroyed = true;
    transport.close();
    handle?.destroy();
    handle = null;
    window.removeEventListener('pagehide', destroy);
  };

  app.ontoolresult = (result) => { if (!destroyed) transport.pushToolResult(result); };
  app.onhostcontextchanged = (ctx) => applyContext(ctx);
  app.onerror = (e) => { if (!destroyed) transport.markStale(e instanceof Error && e.message ? `宿主连接异常：${e.message}` : '宿主连接异常'); };
  app.onteardown = async () => {
    destroy();
    // Reply to ui/resource-teardown first, then drop the bridge.
    setTimeout(() => { void app.close().catch(() => {}); }, 0);
    return {};
  };
  window.addEventListener('pagehide', destroy);

  render();
  app.connect().then(
    () => {
      if (destroyed) { void app.close().catch(() => {}); return; }
      applyContext(app.getHostContext());
      transport.setConnected();
    },
    (e: unknown) => { transport.markStale(e instanceof Error && e.message ? `无法连接宿主：${e.message}` : '无法连接宿主'); },
  );

  return {
    destroy() {
      destroy();
      void app.close().catch(() => {});
    },
  };
}

const root = typeof document !== 'undefined' ? document.getElementById('root') : null;
if (root) startMcpMonitor(root);

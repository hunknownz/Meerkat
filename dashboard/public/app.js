// Standalone Meerkat dashboard bootstrap. Polls GET /api/workflow and hands
// the snapshot to the shared host-neutral UI factory (ui.js). Writes go only
// to the two bounded workflow endpoints with this server's session token.
import { createMeerkatUI } from './ui.js';

export const POLL_MS = 4000;
export const TIMEOUT_MS = 3000;
const MAX_TEXT = 500;
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const text = (v) => (typeof v === 'string' ? v.slice(0, MAX_TEXT) : '');

const MAX_LEGACY = 50;
// Optional link metadata, kept only when it is a non-empty bounded string.
const OPTIONAL = { runId: 64, taskId: 128, role: 40 };

/** Validates the independent Pi heartbeat list (GET /api/active agent shape). */
export function parseLegacy(list) {
  if (!Array.isArray(list)) throw new Error('malformed');
  return list.slice(0, MAX_LEGACY).map((a) => {
    if (!a || typeof a !== 'object') throw new Error('malformed');
    const out = { id: text(a.id), task: text(a.task), model: text(a.model), worktree: text(a.worktree), startedAt: text(a.startedAt) };
    for (const [k, max] of Object.entries(OPTIONAL)) {
      if (typeof a[k] === 'string' && a[k].trim()) out[k] = a[k].trim().slice(0, max);
    }
    return out;
  });
}

/** Validates GET /api/workflow; throws on anything malformed (never returns a fictitious empty state). */
export function parseWorkflow(payload) {
  if (!payload || typeof payload !== 'object' || payload.ok !== true) throw new Error('malformed');
  const { data, sessionToken } = payload;
  if (!data || typeof data !== 'object' || data.schemaVersion !== 1) throw new Error('malformed');
  for (const k of ['projects', 'contexts', 'tasks', 'runs', 'deliveries', 'reviews', 'profiles']) {
    if (data[k] !== undefined && !Array.isArray(data[k])) throw new Error('malformed');
  }
  if (typeof sessionToken !== 'string' || !sessionToken) throw new Error('malformed');
  return { data, legacyActive: parseLegacy(payload.legacyActive ?? []), sessionToken };
}

async function fetchJson(url, init = {}, outer = null) {
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), TIMEOUT_MS);
  const cancel = () => ctl.abort();
  if (outer) { if (outer.aborted) ctl.abort(); else outer.addEventListener('abort', cancel, { once: true }); }
  try {
    const res = await fetch(url, { cache: 'no-store', credentials: 'same-origin', ...init, signal: ctl.signal });
    let body = null;
    try { body = await res.json(); } catch { /* non-JSON */ }
    if (!res.ok || !body || body.ok !== true) throw new Error(text(body?.error) || `HTTP ${res.status}`);
    return body;
  } catch (e) {
    throw new Error(e?.name === 'AbortError' ? '请求超时' : (text(e?.message) || '网络错误'));
  } finally {
    clearTimeout(timer);
    outer?.removeEventListener('abort', cancel);
  }
}

/**
 * Starts polling into `root`; returns a stop function. Uses global fetch so
 * tests can stub it; `deps.createUI` lets tests replace the DOM factory.
 * After stop, no in-flight response may update the UI or schedule a retry.
 */
export function startDashboard(root, deps = {}) {
  const createUI = typeof deps.createUI === 'function' ? deps.createUI : createMeerkatUI;
  let token = '';
  let timer = 0;
  let inFlight = null;
  let stopped = false;
  const life = new AbortController();

  const write = (method, path, body) => {
    if (stopped) return Promise.reject(new Error('已关闭'));
    if (!token) return Promise.reject(new Error('尚未取得会话令牌，请先重新连接'));
    return fetchJson(path, { method, headers: { 'Content-Type': 'application/json', 'X-Meerkat-Token': token }, body: JSON.stringify(body) }, life.signal);
  };

  async function onAction(action) {
    if (stopped) throw new Error('已关闭');
    if (action?.type === 'reconnect') {
      const ok = await poll();
      if (!ok) throw new Error('重连失败');
      return;
    }
    if (action?.type === 'stop') {
      if (!UUID_RE.test(action.runId || '') || !UUID_RE.test(action.requestId || '')) throw new Error('无效的运行 ID');
      const res = await write('POST', `/api/workflow/runs/${encodeURIComponent(action.runId)}/stop`, { requestId: action.requestId });
      poll();
      return res;
    }
    if (action?.type === 'settings') {
      const res = await write('PUT', '/api/workflow/settings', action.input);
      poll();
      return res;
    }
    throw new Error('不支持的操作');
  }

  const ui = createUI(root, { onAction });

  // One request at a time; on failure the last snapshot is marked stale and
  // polling stops until the user reconnects manually.
  function poll() {
    if (stopped) return Promise.resolve(false);
    if (inFlight) return inFlight;
    clearTimeout(timer);
    inFlight = (async () => {
      try {
        const body = await fetchJson('/api/workflow', {}, life.signal);
        if (stopped) return false;
        const { data, legacyActive, sessionToken } = parseWorkflow(body);
        token = sessionToken;
        ui.update(data, legacyActive);
        if (!stopped) timer = setTimeout(poll, POLL_MS);
        return true;
      } catch (e) {
        token = '';
        if (stopped) return false;
        ui.setDisconnected(e?.message === 'malformed' ? '工作流数据格式无效' : text(e?.message) || '无法连接');
        return false;
      } finally {
        inFlight = null;
      }
    })();
    return inFlight;
  }

  poll();
  return () => {
    if (stopped) return;
    stopped = true;
    clearTimeout(timer);
    life.abort();
    ui.destroy();
  };
}

if (typeof document !== 'undefined') {
  const root = document.getElementById('meerkat-ui');
  if (root) startDashboard(root);
}

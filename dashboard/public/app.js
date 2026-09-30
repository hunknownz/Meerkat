// Read-only live monitor for running Pi agents. Polls GET /api/active.
// All rendering uses textContent; no actions, forms, or task records.

const POLL_MS = 4000;
const MAX_TEXT = 500;

/** Validates the /api/active payload; throws on anything malformed. */
export function parseActive(payload) {
  if (!payload || typeof payload !== 'object' || payload.ok !== true) throw new Error('malformed');
  const { count, agents } = payload;
  if (!Number.isInteger(count) || count < 0 || !Array.isArray(agents)) throw new Error('malformed');
  return {
    count,
    agents: agents.map((a) => {
      if (!a || typeof a !== 'object') throw new Error('malformed');
      return {
        id: text(a.id),
        task: text(a.task),
        model: text(a.model),
        worktree: text(a.worktree),
        startedAt: text(a.startedAt),
      };
    }),
  };
}

function text(v) {
  return typeof v === 'string' ? v.slice(0, MAX_TEXT) : '';
}

/** Formats elapsed time since an ISO timestamp, e.g. "1h 02m", "3m 05s". */
export function formatElapsed(startedAt, now = Date.now()) {
  const t = Date.parse(startedAt);
  if (!Number.isFinite(t)) return '—';
  const s = Math.max(0, Math.floor((now - t) / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const pad = (n) => String(n).padStart(2, '0');
  if (h) return `${h}h ${pad(m)}m`;
  if (m) return `${m}m ${pad(s % 60)}s`;
  return `${s}s`;
}

function el(tag, className, content) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (content !== undefined) node.textContent = content;
  return node;
}

function renderAgents(list, agents) {
  list.replaceChildren(
    ...agents.map((a) => {
      const li = el('li', 'agent');
      li.append(el('p', 'agent-task', a.task || '(未命名任务)'));
      const dl = el('dl', 'agent-meta');
      for (const [k, v] of [
        ['模型', a.model || '—'],
        ['工作树', a.worktree || '—'],
        ['已运行', formatElapsed(a.startedAt)],
      ]) dl.append(el('dt', '', k), el('dd', '', v));
      li.append(dl);
      return li;
    }),
  );
}

function startMonitor() {
  const status = document.getElementById('status');
  const count = document.getElementById('count');
  const list = document.getElementById('agents');
  const updated = document.getElementById('updated');

  const showError = (message) => {
    status.dataset.state = 'error';
    status.textContent = message;
    count.textContent = '运行状态未知';
    list.replaceChildren();
  };

  async function poll() {
    let res;
    try {
      res = await fetch('/api/active', { cache: 'no-store', headers: { accept: 'application/json' } });
    } catch {
      showError('无法连接状态服务，正在重试…');
      return;
    }
    if (!res.ok) {
      showError(`状态服务不可用（HTTP ${res.status}），正在重试…`);
      return;
    }
    let data;
    try {
      data = parseActive(await res.json());
    } catch {
      showError('状态数据格式无效，正在重试…');
      return;
    }
    status.dataset.state = 'ok';
    status.textContent = '实时';
    count.textContent = `${data.count} 个运行中`;
    renderAgents(list, data.agents);
    updated.textContent = `更新于 ${new Date().toLocaleTimeString()}`;
  }

  const loop = async () => {
    await poll();
    setTimeout(loop, POLL_MS);
  };
  loop();
}

if (typeof document !== 'undefined') startMonitor();

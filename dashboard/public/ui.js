// Meerkat live UI — host-neutral factory shared by the standalone dashboard
// (app.js) and the desktop overlay (ShadowRoot). It renders a workflow
// snapshot supplied by the host; it never fetches, and all DOM queries and
// listeners are scoped to the provided root element. Every source string is
// escaped and every link is validated before it is rendered.
// Layout, icons and copy are adapted from design/ui/app.js (the prototype).

const ROLE = { developer: '开发', reviewer: '检查', polisher: '最终精修', fixer: '定向修复' };
const ROLES = ['developer', 'reviewer', 'polisher'];
const PHASES = ['任务', '开发', '初次交付', '检查', '最终候选', '精修与复验'];
const TABS = [['overview', '概览'], ['context', '共享上下文'], ['delivery', '交付与检查'], ['runs', '运行记录']];
const FILTERS = [['all', '全部'], ['active', '进行中'], ['delivered', '已交付'], ['attention', '需处理']];
const ACTIVE_RUN = new Set(['queued', 'pending', 'starting', 'running', 'stopping', 'unknown']);
const RUN_LABEL = {
  queued: '排队', pending: '排队', starting: '启动中', running: '运行中', stopping: '停止中', unknown: '状态未知',
  succeeded: '已完成', completed: '已完成', failed: '失败', stopped: '已停止', cancelled: '已取消', timeout: '超时', blocked: '已阻塞',
};
const DELIVERY_LABEL = { first: '初次交付', final_candidate: '最终候选', delivered: '最终代码交付' };
const MAX_TEXT = 4000;
const MAX_EVENTS = 5;

// ---------------------------------------------------------------------------
// Pure helpers (exported for tests; no DOM access)
// ---------------------------------------------------------------------------
export const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const str = (v, max = 300) => (typeof v === 'string' ? v.slice(0, max) : (typeof v === 'number' && Number.isFinite(v) ? String(v) : ''));
const arr = (v) => (Array.isArray(v) ? v : []);
const isNum = (v) => typeof v === 'number' && Number.isFinite(v);
const num = (n) => n.toLocaleString('en-US');
const short = (s, n = 8) => str(s).slice(0, n);
const UNK = '<em class="unknown">未知 / 未返回</em>';

/** Parses an ISO timestamp; returns epoch ms or null. */
export function parseTime(iso) {
  if (typeof iso !== 'string' || !iso) return null;
  const t = Date.parse(iso);
  return Number.isFinite(t) ? t : null;
}

/** Whole seconds between ISO start and ISO end (or `now` when end is absent). Null when unknown. */
export function elapsedSeconds(start, end, now = Date.now()) {
  const a = parseTime(start);
  if (a === null) return null;
  const b = end ? parseTime(end) : now;
  if (b === null) return null;
  return Math.max(0, Math.floor((b - a) / 1000));
}

/** "2 小时 05 分" / "3 分 07 秒" / "12 秒"; null → "—". Works across days. */
export function formatDuration(sec) {
  if (!isNum(sec) || sec < 0) return '—';
  const s = Math.floor(sec);
  const pad = (n) => String(n).padStart(2, '0');
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (h >= 24) return `${Math.floor(h / 24)} 天 ${h % 24} 小时`;
  if (h) return `${h} 小时 ${pad(m)} 分`;
  if (m) return `${m} 分 ${pad(s % 60)} 秒`;
  return `${s} 秒`;
}

/** Local time for an ISO timestamp: "14:32" today, "10-01 14:32" otherwise, year when different. */
export function formatLocalTime(iso, now = Date.now()) {
  const t = parseTime(iso);
  if (t === null) return '—';
  const d = new Date(t);
  const n = new Date(now);
  const pad = (x) => String(x).padStart(2, '0');
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  if (d.toDateString() === n.toDateString()) return hm;
  const md = `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${hm}`;
  return d.getFullYear() === n.getFullYear() ? md : `${d.getFullYear()}-${md}`;
}

/** Returns a normalized https URL string, or null for anything else (javascript:, data:, http:, relative…). */
export function safeHref(url) {
  if (typeof url !== 'string' || url.length > 2048) return null;
  try {
    const u = new URL(url);
    return u.protocol === 'https:' && !u.username && !u.password ? u.href : null;
  } catch { return null; }
}

/** Model label recorded on the run itself (never the current settings). */
export function modelLabel(snap) {
  if (typeof snap === 'string') return snap.slice(0, 120);
  if (snap && typeof snap === 'object') {
    const model = str(snap.model, 120);
    const provider = str(snap.provider, 60);
    return model ? (provider ? `${provider}/${model}` : model) : str(snap.id || snap.profileId, 120);
  }
  return '';
}

const COUNTERS = ['input', 'output', 'cacheRead', 'cacheWrite'];

/**
 * Usage counters for one run: each counter is a number or null (unknown).
 * Accepts the core shape { tokens: {input,…}, usageCompleteness } and legacy flat
 * counters. `completeness` is 'complete' only when every counter is known and the
 * record does not declare itself partial/unknown.
 */
export function runUsage(run) {
  const u = run?.usage ?? run?.summary?.usage;
  if (!u || typeof u !== 'object') return null;
  const src = u.tokens && typeof u.tokens === 'object' ? u.tokens : u;
  const out = {};
  for (const k of COUNTERS) out[k] = isNum(src[k]) && src[k] >= 0 ? src[k] : null;
  if (!COUNTERS.some((k) => out[k] !== null)) return null;
  const declared = u.usageCompleteness;
  const declaredOk = declared === undefined || declared === 'complete';
  out.completeness = declaredOk && COUNTERS.every((k) => out[k] !== null) ? 'complete' : 'partial';
  return out;
}

/**
 * Fee for one run. Only a provider-reported USD amount counts as reported;
 * an explicitly estimated USD amount is flagged; anything else is unknown.
 * Returns { kind: 'reported'|'estimated'|'unknown', usd }.
 */
export function runCost(run) {
  const c = run?.cost ?? run?.usage?.cost ?? run?.summary?.cost ?? run?.summary?.usage?.cost;
  if (!c || typeof c !== 'object') return { kind: 'unknown', usd: null };
  const usd = isNum(c.total) ? c.total : (isNum(c.amount) ? c.amount : null);
  if (usd === null || usd < 0 || c.currency !== 'USD') return { kind: 'unknown', usd: null };
  if (c.estimated === true || c.source === 'estimated') return { kind: 'estimated', usd };
  if (c.source === 'provider' || c.reported === true) return { kind: 'reported', usd };
  return { kind: 'unknown', usd: null };
}

/** Aggregates usage over runs: known subtotals plus missing counts; fees split by kind. */
export function summarizeUsage(runs, now = Date.now()) {
  const s = { runs: runs.length, reported: 0, full: 0, missing: {}, known: {}, usd: 0, usdRuns: 0, estUsd: 0, estRuns: 0, unknownFee: 0, agentSeconds: 0, wallSeconds: null, unknownTime: 0 };
  for (const k of COUNTERS) { s.missing[k] = 0; s.known[k] = 0; }
  let first = null;
  let last = null;
  for (const r of runs) {
    const u = runUsage(r);
    if (u) s.reported += 1;
    for (const k of COUNTERS) {
      if (u && u[k] !== null) s.known[k] += u[k];
      else s.missing[k] += 1;
    }
    if (u?.completeness === 'complete') s.full += 1;
    const c = runCost(r);
    if (c.kind === 'reported') { s.usd += c.usd; s.usdRuns += 1; } else if (c.kind === 'estimated') { s.estUsd += c.usd; s.estRuns += 1; } else s.unknownFee += 1;
    const sec = elapsedSeconds(r.startedAt, r.endedAt, now);
    if (sec === null) { s.unknownTime += 1; continue; }
    s.agentSeconds += sec;
    const a = parseTime(r.startedAt);
    const b = r.endedAt ? parseTime(r.endedAt) : now;
    first = first === null ? a : Math.min(first, a);
    last = last === null ? b : Math.max(last, b);
  }
  // Wall time = union span of the runs (first start → last end), distinct from the per-run sum.
  if (first !== null) s.wallSeconds = Math.max(0, Math.floor((last - first) / 1000));
  return s;
}

const isActiveRun = (r) => ACTIVE_RUN.has(r?.state);
const MAX_INDEPENDENT = 50;
const count = (v) => (Number.isInteger(v) && v >= 0 ? v : null);

/**
 * Live run counts for the header. Independent Pi heartbeats whose runId is
 * already a snapshot run (in any state) are dropped so a managed job never
 * appears twice. Counts that are missing or malformed stay null (unknown),
 * never 0. Returns { managed, independent, total, queued, unknown, list }.
 */
export function liveRunSummary(snapshot, legacyActive = []) {
  const s = snapshot && typeof snapshot === 'object' ? snapshot : {};
  const ids = new Set();
  for (const r of arr(s.runs)) if (r && typeof r.id === 'string' && r.id) ids.add(r.id.toLowerCase());
  const list = arr(legacyActive)
    .filter((a) => a && typeof a === 'object' && !(typeof a.runId === 'string' && ids.has(a.runId.toLowerCase())))
    .slice(0, MAX_INDEPENDENT);
  const c = s.counts && typeof s.counts === 'object' && !Array.isArray(s.counts) ? s.counts : {};
  const managed = count(c.running);
  // `unknown` is optional in the snapshot; when absent derive it from runs, when malformed keep it unknown.
  const unknown = c.unknown === undefined ? arr(s.runs).filter((r) => r?.state === 'unknown').length : count(c.unknown);
  return { managed, independent: list.length, total: managed === null ? null : managed + list.length, queued: count(c.queued), unknown, list };
}
/** Task category for filters. Delivered means final code only. */
export function taskCategory(t) {
  const st = str(t?.state);
  if (st === 'delivered') return 'delivered';
  if (/block|fail|stop|unknown|error|attention|needs|timeout|cancel/.test(st)) return 'attention';
  return 'active';
}

// Explicit phase for the workflow's task states; fixing is a fix round inside the check loop.
const STATE_PHASE = {
  implementing: 1, first_delivery: 2, checking: 3, fixing: 3, final_candidate: 4, polishing: 5, rechecking: 5, delivered: 5,
};

/** Phase index (into PHASES) for a task, given its deliveries. */
export function phaseIndex(t, deliveries = []) {
  const st = str(t?.state);
  if (Object.hasOwn(STATE_PHASE, st)) return STATE_PHASE[st];
  if (st === 'delivered' || /polish|recheck/.test(st)) return 5;
  if (deliveries.some((d) => d.state === 'final_candidate')) return 4;
  if (/review|check/.test(st)) return 3;
  if (deliveries.length) return 2;
  if (/develop|fix|running|dev/.test(st)) return 1;
  return 0;
}

/** Bounded event text: type plus summary (or legacy message); never arguments or transcripts. */
export function eventText(e) {
  return [str(e?.type || e?.kind, 60), str(e?.summary || e?.message || e?.detail || e?.reason, 200)].filter(Boolean).join(' · ');
}
/** Event timestamp: core observedAt, legacy at/time. */
export const eventTime = (e) => e?.observedAt || e?.at || e?.time;

/** resumeRole is a recovery cursor; it is pending only once the task failed, stopped or is unknown. */
export const pendingResume = (t) => (t?.resumeRole && ['failed', 'stopped', 'unknown'].includes(t.state) ? t.resumeRole : null);

/** Short safe text for a structured value (string or {name,status,message…}); never dumps raw objects. */
function itemText(x) {
  if (typeof x === 'string') return x.slice(0, 500);
  if (!x || typeof x !== 'object') return str(x);
  const head = str(x.name || x.title || x.check || x.path || x.severity, 160);
  const body = str(x.status || x.result || x.message || x.summary || x.detail, 400);
  return [head, body].filter(Boolean).join(' · ');
}

// ---------------------------------------------------------------------------
// Static chrome (from design/ui/index.html; icons inlined so they follow text colour)
// ---------------------------------------------------------------------------
const LOGO = '<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor"><g transform="translate(2.179 1) scale(.03944194)"><g transform="translate(-475 829.791944) scale(.1 -.1)"><path d="M7212 8289c-377-63-605-456-473-817 29-81 100-200 140-236 21-19 21-19 21-1910 0-1678-2-1899-16-1951-30-118-108-216-213-268-66-32-66-32-993-35l-928-2v-350h928c892 0 932 1 1006 20 201 51 381 201 476 395 81 166 74-34 77 2222 3 2012 3 2012-39 2041-282 196-179 581 152 574 138-3 300-39 635-141 182-55 440-133 575-174 245-73 245-73 248-104 4-42-38-90-106-120-29-12-146-61-260-107-326-131-432-197-556-341-153-180-253-464-231-657 3-23 21-212 40-418 45-477 36-432 87-448 304-95 632-401 811-758 104-208 197-574 197-780 0-44 0-44 171-44h172l-6 138c-30 708-421 1357-1014 1684-73 40-73 40-73 82 0 23-11 158-25 300-29 300-30 329-10 416 29 122 89 225 190 326 102 102 129 118 509 293 120 56 237 116 259 133 166 132 197 435 55 538-24 17-102 48-201 80-89 28-364 117-612 197-668 218-818 251-993 222zM7650 4936c0-193 0-193 67-232 233-135 434-454 474-752 49-375-87-686-478-1092l-135-140h2152v350h-1380l46 91c314 626 116 1427-448 1817-78 53-263 152-285 152-10 0-13-45-13-194z"/></g></g></svg>';
const CLOSE = '<svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5"/></svg>';
const CHEV_R = '<svg class="chev-r" width="10" height="10" viewBox="0 0 10 10" aria-hidden="true"><path d="M3.5 2 6.5 5 3.5 8" fill="none" stroke="currentColor" stroke-width="1.4"/></svg>';
const CHROME = `
<header class="top">
  <div class="brand"><span class="logo" aria-hidden="true">${LOGO}</span><span role="heading" aria-level="1">Meerkat</span></div>
  <nav class="seg" data-ref="nav" aria-label="视图">
    <button type="button" data-view="agents" class="on" aria-current="page">Agents</button>
    <button type="button" data-view="tasks">Tasks</button>
    <button type="button" data-view="usage">Usage</button>
  </nav>
  <div class="actions">
    <button type="button" class="icon" data-act="theme" aria-label="切换到深色主题" title="切换主题">
      <svg class="i-moon" width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M13.5 9.6A5.6 5.6 0 0 1 6.4 2.5a5.6 5.6 0 1 0 7.1 7.1Z"/></svg>
      <svg class="i-sun" width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true"><circle cx="8" cy="8" r="3"/><path d="M8 1.5v1.6M8 12.9v1.6M1.5 8h1.6M12.9 8h1.6M3.4 3.4l1.1 1.1M11.5 11.5l1.1 1.1M3.4 12.6l1.1-1.1M11.5 4.5l1.1-1.1"/></svg>
    </button>
    <button type="button" class="icon" data-act="settings" aria-label="设置" title="设置" aria-haspopup="dialog">
      <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true"><circle cx="8" cy="8" r="2.2"/><path d="M8 1.6v1.7M8 12.7v1.7M14.4 8h-1.7M3.3 8H1.6M12.5 3.5l-1.2 1.2M4.7 11.3l-1.2 1.2M12.5 12.5l-1.2-1.2M4.7 4.7 3.5 3.5"/></svg>
    </button>
  </div>
</header>
<div class="subbar">
  <label class="select-pill"><span class="sr-only">项目</span>
    <select data-ref="project" aria-label="项目筛选"><option value="all">全部项目</option></select>
    <svg class="chev" width="9" height="9" viewBox="0 0 10 10" aria-hidden="true"><path d="M2 3.5 5 6.5 8 3.5" fill="none" stroke="currentColor" stroke-width="1.4"/></svg>
  </label>
  <span class="state-sum" data-ref="sum" aria-live="polite"></span>
  <span class="notice" data-ref="conn" role="status" aria-live="polite">正在加载…</span>
</div>
<main class="view">
  <section data-ref="view-agents" aria-label="Agents"></section>
  <section data-ref="view-tasks" aria-label="Tasks" hidden></section>
  <section data-ref="view-usage" aria-label="Usage" hidden></section>
</main>
<div class="scrim" data-ref="drawer-scrim" hidden>
  <aside class="drawer" data-ref="drawer" role="dialog" aria-modal="true" aria-labelledby="mk-drawer-title" tabindex="-1"></aside>
</div>
<div class="scrim center" data-ref="settings-scrim" hidden>
  <div class="sheet" data-ref="settings" role="dialog" aria-modal="true" aria-labelledby="mk-settings-title" tabindex="-1"></div>
</div>
<div class="toast" data-ref="toast" role="status" aria-live="polite"></div>`;

function uuid() {
  const c = globalThis.crypto;
  if (c?.randomUUID) return c.randomUUID();
  const b = c.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 15) | 64; b[8] = (b[8] & 63) | 128;
  const h = [...b].map((x) => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

/** Normalizes the snapshot into lookup tables; tolerates missing arrays. */
function indexSnapshot(snap) {
  const s = snap && typeof snap === 'object' ? snap : {};
  const by = (list, key) => { const m = new Map(); for (const x of list) { const k = x?.[key]; if (k == null) continue; if (!m.has(k)) m.set(k, []); m.get(k).push(x); } return m; };
  const tasks = arr(s.tasks).filter((t) => t && typeof t === 'object');
  const runs = arr(s.runs).filter((r) => r && typeof r === 'object');
  return {
    raw: s,
    projects: new Map(arr(s.projects).filter(Boolean).map((p) => [p.id, p])),
    contexts: arr(s.contexts).filter(Boolean),
    tasks,
    task: new Map(tasks.map((t) => [t.id, t])),
    runs,
    runsByTask: by(runs, 'taskId'),
    deliveriesByTask: by(arr(s.deliveries).filter(Boolean), 'taskId'),
    reviewsByTask: by(arr(s.reviews).filter(Boolean), 'taskId'),
    deliveries: arr(s.deliveries).filter(Boolean),
    profiles: arr(s.profiles).filter((p) => p && typeof p === 'object'),
    settings: s.settings && typeof s.settings === 'object' ? s.settings : null,
    counts: s.counts && typeof s.counts === 'object' ? s.counts : {},
    controller: s.controller && typeof s.controller === 'object' ? s.controller : { state: 'unknown' },
    observedAt: str(s.observedAt, 64),
  };
}

/**
 * Mounts the UI into `root` (its id becomes "meerkat-ui"; theme lives on it).
 * options.onAction(action) → Promise; actions: {type:'stop',runId,requestId},
 * {type:'settings',input}, {type:'reconnect'}. options.readonly disables writes
 * with an explanation (e.g. desktop host). Returns { update, setDisconnected, destroy }.
 */
export function createMeerkatUI(root, options = {}) {
  if (!root || typeof root.querySelector !== 'function') throw new TypeError('createMeerkatUI: root element required');
  const onAction = typeof options.onAction === 'function' ? options.onAction : async () => { throw new Error('no host action handler'); };
  const readonly = !!options.readonly || typeof options.onAction !== 'function';
  const readonlyNote = str(options.readonlyNote, 300) || '此宿主为只读视图；请使用 coordinator CLI 停止运行或修改设置。';
  const themeKey = options.themeKey === null ? null : (str(options.themeKey, 60) || 'meerkat-theme');

  const state = {
    view: 'agents', project: 'all', query: '', filter: 'all', expanded: new Set(),
    drawer: null, drawerTab: 'overview', returnFocus: null,
    loaded: false, disconnected: null, idx: indexSnapshot(null), live: liveRunSummary(null), legacy: [],
    stop: new Map(), // runId → { requestId, status: 'pending'|'acked'|'error', message }
    settingsMsg: '', settingsBusy: false, reconnecting: false,
  };
  const listeners = [];
  let toastTimer = 0;

  root.id = 'meerkat-ui';
  root.innerHTML = CHROME;
  const $ = (sel) => root.querySelector(sel);
  const ref = (name) => root.querySelector(`[data-ref="${name}"]`);
  const doc = () => root.getRootNode?.() || root.ownerDocument;
  const active = () => { const d = doc(); return d && d.activeElement; };
  const on = (type, fn) => { root.addEventListener(type, fn); listeners.push([type, fn]); };
  const now = () => (state.disconnected && state.idx.observedAt ? (parseTime(state.idx.observedAt) ?? Date.now()) : Date.now());

  function toast(msg) {
    const el = ref('toast');
    el.textContent = msg;
    el.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => el.classList.remove('show'), 2600);
  }

  // Re-render while keeping keyboard focus on the equivalent element.
  function keepFocus(fn) {
    const a = active();
    let sel = null;
    if (a && root.contains(a)) {
      for (const k of ['data-agent', 'data-open-task', 'data-stop', 'data-filter', 'data-tab', 'data-view', 'data-ref', 'data-act', 'data-setting']) {
        if (a.hasAttribute(k)) { sel = `[${k}="${CSS.escape(a.getAttribute(k))}"]`; break; }
      }
      if (!sel && a.id) sel = `#${CSS.escape(a.id)}`;
    }
    const caret = a && a.tagName === 'INPUT' && a.type === 'search' ? a.selectionStart : null;
    fn();
    if (sel) {
      const b = root.querySelector(sel);
      if (b && b !== a) { b.focus(); if (caret !== null && b.setSelectionRange) b.setSelectionRange(caret, caret); }
    }
  }

  // ---- lookups --------------------------------------------------------------
  const I = () => state.idx;
  const projName = (id) => str(I().projects.get(id)?.name) || str(id) || '未知项目';
  const inProject = (pid) => state.project === 'all' || state.project === pid;
  const taskOf = (r) => I().task.get(r.taskId);
  const ctxOf = (ref) => { const id = typeof ref === 'object' && ref ? ref.id : ref; return I().contexts.find((c) => c.id === id) || null; };
  const ctxLabel = (ref) => {
    const c = ctxOf(ref);
    if (c) return `v${str(c.version)} · ${short(c.digest, 12)}`;
    if (ref && typeof ref === 'object') return [ref.version != null ? `v${str(ref.version)}` : '', short(ref.digest || ref.id, 12)].filter(Boolean).join(' · ');
    return short(ref, 12) || '—';
  };
  const issueText = (t) => (t?.issueRef ? str(t.issueRef.title, 120) || 'Issue' : '无 Issue');
  const issueLink = (t, cls = '') => {
    const href = safeHref(t?.issueRef?.url);
    return href ? `<a class="${cls}" href="${esc(href)}" target="_blank" rel="noopener noreferrer">${esc(issueText(t))}</a>` : esc(issueText(t));
  };
  const runLabel = (st) => RUN_LABEL[st] || str(st) || '未知';
  const roleLabel = (r) => ROLE[r] || str(r) || '—';
  const lastEvent = (r) => { const e = arr(r.events); return e.length ? e[e.length - 1] : null; };
  const timeEl = (iso) => (parseTime(iso) === null ? '<time>—</time>' : `<time datetime="${esc(iso)}" title="${esc(new Date(parseTime(iso)).toLocaleString())}">${esc(formatLocalTime(iso, now()))}</time>`);
  const since = (start, end) => {
    const sec = elapsedSeconds(start, end, now());
    // Live spans tick every second unless the snapshot is stale.
    return end || state.disconnected || sec === null ? esc(formatDuration(sec)) : `<span data-since="${esc(start)}">${esc(formatDuration(sec))}</span>`;
  };
  const managedActive = () => I().runs.filter((r) => isActiveRun(r) && inProject(taskOf(r)?.projectId));

  // ---- header summary --------------------------------------------------------
  function renderSummary() {
    const el = ref('sum');
    const conn = ref('conn');
    conn.classList.toggle('live', state.loaded && !state.disconnected);
    conn.classList.toggle('stale', !!state.disconnected);
    if (state.disconnected) {
      conn.textContent = state.loaded ? `已断连 · 快照 ${formatLocalTime(I().observedAt)} 已过期` : '未连接';
      el.innerHTML = '运行数 <b>未知</b>';
      return;
    }
    if (!state.loaded) { conn.textContent = '正在加载…'; el.textContent = ''; return; }
    conn.textContent = `实时 · 更新于 ${formatLocalTime(I().observedAt)}`;
    const limit = I().settings && Number.isInteger(I().settings.maxConcurrency) ? ` · 工作流并发上限 ${I().settings.maxConcurrency}` : '';
    el.innerHTML = `${liveCountText((v) => `<b>${v === null ? '未知' : v}</b>`)}${limit}`;
  }

  // "N 运行中（工作流 M · 独立 K）· Q 排队 · U 状态未知"; unknown values stay 未知, never 0.
  function liveCountText(fmt) {
    const l = state.live;
    const total = l.total === null ? `运行数 ${fmt(null)}` : `${fmt(l.total)} 运行中`;
    const unknown = l.unknown === 0 ? '' : ` · ${fmt(l.unknown)} 状态未知`;
    return `${total}（工作流 ${fmt(l.managed)} · 独立 ${fmt(l.independent)}）· ${fmt(l.queued)} 排队${unknown}`;
  }

  function renderProjects() {
    const sel = ref('project');
    const ids = [...I().projects.keys()];
    if (state.project !== 'all' && !ids.includes(state.project)) state.project = 'all';
    sel.innerHTML = `<option value="all">全部项目</option>${ids.map((id) => `<option value="${esc(id)}"${id === state.project ? ' selected' : ''}>${esc(projName(id))}</option>`).join('')}`;
  }

  // ---- Agents view -----------------------------------------------------------
  function stopControl(r) {
    if (r.state !== 'running' || state.disconnected) return '';
    if (readonly) return `<span class="k small">${esc(readonlyNote)}</span>`;
    const s = state.stop.get(r.id);
    if (s?.status === 'acked') return '<button type="button" class="btn" disabled>已请求停止</button><span class="k small">停止请求已送达，不代表进程已停止；以运行状态为准。</span>';
    const label = s?.status === 'pending' ? '正在发送…' : '请求停止';
    const err = s?.status === 'error' ? `<span class="err small" role="alert">停止请求失败：${esc(s.message)}（可重试，使用同一请求 ID）</span>` : '';
    return `<button type="button" class="btn" data-stop="${esc(r.id)}"${s?.status === 'pending' ? ' disabled' : ''}>${label}</button>${err}`;
  }

  function agentRow(r) {
    const t = taskOf(r) || {};
    const stale = !!state.disconnected;
    const key = str(r.id, 64);
    const open = state.expanded.has(key);
    const dot = stale || r.state === 'unknown' ? 'stale' : (r.state === 'running' ? 'running' : (/block|fail/.test(r.state) ? 'blocked' : 'waiting'));
    const waiting = r.state === 'queued' || r.state === 'pending';
    const ev = lastEvent(r);
    const events = arr(r.events).slice(-MAX_EVENTS).reverse();
    const deliv = arr(I().deliveriesByTask.get(r.taskId)).slice(-1)[0];
    const elapsed = waiting ? since(r.createdAt || r.updatedAt, null) : since(r.startedAt, r.endedAt);
    return `
    <div class="agent-row${stale ? ' stale' : ''}">
      <button type="button" class="row-btn" data-agent="${esc(key)}" aria-expanded="${open}" aria-controls="mk-more-${esc(key)}">
        <span class="dot ${dot}" title="${esc(stale ? '快照（已过期）' : runLabel(r.state))}"></span>
        <span class="who">
          <span class="line1"><span class="role">${esc(str(r.agentId, 40) || '—')} · ${esc(roleLabel(r.role))}</span><span class="task">${esc(str(t.title, 200) || '未知任务')}</span></span>
          <span class="sub">${esc(projName(t.projectId))} · ${esc(issueText(t))}${ev ? ` · <span class="act">${esc(eventText(ev))}</span> · ${esc(formatLocalTime(eventTime(ev), now()))}` : ''}${stale ? ' · 快照' : ''}</span>
        </span>
        <span class="fields">
          <span class="field"><span class="v">${esc(modelLabel(r.modelSnapshot) || '模型未记录')}</span></span>
          <span class="field"><span class="v">${esc(runLabel(r.state))}</span></span>
          <span class="field f-elapsed" title="${waiting ? '已等待' : '已用时'}"><span class="k">${waiting ? '等待' : '用时'}</span><span class="v">${elapsed}</span></span>
        </span>${CHEV_R}
      </button>
      <div class="agent-more" id="mk-more-${esc(key)}" ${open ? '' : 'hidden'}>
        <div>
          <h4>最近 ${events.length} 条结构化事件${stale ? '（快照）' : ''}</h4>
          ${events.length ? `<ul class="events">${events.map((e) => `<li>${timeEl(eventTime(e))}<span class="ek">${esc(str(e.type || e.kind, 60))}</span><span>${esc(str(e.summary || e.message || e.detail || e.reason, 200))}</span></li>`).join('')}</ul>` : '<p class="k small">尚无事件。</p>'}
          ${r.state === 'unknown' ? '<p class="local-note mt">状态未知：控制器心跳过期或重启后未能核实，不会自动重放。</p>' : ''}
        </div>
        <div>
          <h4>上下文</h4>
          <dl class="kv">
            <dt>工作目录</dt><dd><code>${esc(str(t.worktree, 500) || '—')}</code></dd>
            <dt>仓库</dt><dd><code>${esc(str(t.repository, 500) || '—')}</code></dd>
            <dt>上下文</dt><dd>${esc(ctxLabel(r.contextRef || t.contextRef))}</dd>
            <dt>开始</dt><dd>${timeEl(r.startedAt)}</dd>
            <dt>最近交付</dt><dd>${deliv ? `${esc(DELIVERY_LABEL[deliv.state] || str(deliv.state))} · <code>${esc(short(deliv.candidateSha, 10))}</code>` : '尚无'}</dd>
          </dl>
          <div class="inline-actions">
            ${t.id ? `<button type="button" class="btn" data-open-task="${esc(t.id)}">打开任务</button>` : ''}
            ${stopControl(r)}
          </div>
        </div>
      </div>
    </div>`;
  }

  function independentRows() {
    const list = state.legacy;
    if (!list.length) return '';
    return `
      <div class="sec-h"><b>独立运行，尚未关联任务</b><span>${list.length} 个 · 直接启动的 Pi，暂无任务上下文 / 阶段信息</span></div>
      <div class="list independent">${list.map((a) => {
        const path = str(a.worktree, 500);
        const meta = [a.role ? roleLabel(str(a.role, 40)) : '', a.runId ? `运行 ${short(a.runId)}` : ''].filter(Boolean).map(esc).join(' · ');
        return `<div class="row">
        <span class="badge">独立</span>
        <span class="who"><span class="name">${esc(str(a.task, 300) || '未命名')}</span>
        <span class="sub">${esc(str(a.model, 120) || '模型未记录')}${meta ? ` · ${meta}` : ''} · 开始 ${timeEl(a.startedAt)}</span>
        ${path ? `<span class="sub path" title="${esc(path)}"><code>${esc(path)}</code></span>` : ''}</span>
        <span class="num"><b>${state.disconnected ? esc(formatDuration(elapsedSeconds(a.startedAt, null, now()))) : since(a.startedAt, null)}</b><small>用时</small></span>
      </div>`;
      }).join('')}</div>`;
  }

  function renderAgents() {
    const sec = ref('view-agents');
    const stale = !!state.disconnected;
    let banner = '';
    if (stale) {
      banner = `<div class="stale-banner" role="status"><span><b>已断连</b> · ${esc(state.disconnected)}。运行数未知。${state.loaded ? `下方为 ${esc(formatLocalTime(I().observedAt))} 的最近快照，已过期，不代表当前状态。` : '尚未取得任何快照。'}不会自动无限重连。</span><button type="button" class="btn" data-act="reconnect"${state.reconnecting ? ' disabled' : ''}>${state.reconnecting ? '正在重连…' : '重新连接'}</button></div>`;
    }
    if (!state.loaded) { sec.innerHTML = `${banner}<div class="list"><div class="empty">${stale ? '无法读取工作流状态。' : '正在读取工作流状态…'}</div></div>`; return; }
    const c = I().controller;
    const ctl = { running: '运行中', idle: '空闲', unknown: '未知' }[c.state] || '未知';
    const runs = managedActive();
    const headCount = stale ? '运行数未知' : liveCountText((v) => (v === null ? '未知' : String(v)));
    const list = runs.length ? runs.map(agentRow).join('') : `<div class="empty">当前没有工作流运行${state.legacy.length ? '（独立运行见下方）' : ''}。<br>${I().tasks.length ? '新任务分派后会出现在这里；当前没有排队记录。' : '还没有工作流任务。用 coordinator CLI 准备任务后会出现在这里。'}</div>`;
    const dl = I().deliveries.filter((d) => inProject(I().task.get(d.taskId)?.projectId))
      .sort((a, b) => (parseTime(b.updatedAt || b.createdAt) ?? 0) - (parseTime(a.updatedAt || a.createdAt) ?? 0)).slice(0, 5);
    const dRows = dl.length ? dl.map((d) => {
      const t = I().task.get(d.taskId) || {};
      const cls = d.state === 'delivered' ? 'green' : (d.state === 'final_candidate' ? 'amber' : 'accent');
      const gaps = arr(d.knownGaps).length;
      return `<button type="button" class="row deliv-row" data-open-task="${esc(t.id || '')}">
        <span class="who"><span class="name">${esc(str(t.title, 200) || '未知任务')}</span>
        <span class="sub">${esc(issueText(t))} · <code>${esc(short(d.candidateSha, 10) || '—')}</code>${gaps ? ` · 已知缺口 ${gaps}` : ''}${d.updatedAt || d.createdAt ? ` · ${esc(formatLocalTime(d.updatedAt || d.createdAt, now()))}` : ''}</span></span>
        <span class="badge res ${cls}">${esc(DELIVERY_LABEL[d.state] || str(d.state) || '未知')}</span>
      </button>`;
    }).join('') : '<div class="empty">暂无交付。</div>';
    sec.innerHTML = `
      ${banner}
      <div class="host"><span class="badge accent">宿主</span><span><b>Codex · 外部协调</b> — 由你所在的 Codex 会话协调，不计入本地运行数。本地控制器：${esc(ctl)}${c.heartbeatAt ? `（心跳 ${esc(formatLocalTime(c.heartbeatAt, now()))}）` : ''}</span></div>
      <div class="sec-h"><b>本地 Pi 实例</b><span>${esc(headCount)}</span><span class="end">${stale ? `快照 ${esc(formatLocalTime(I().observedAt))}` : `更新于 ${esc(formatLocalTime(I().observedAt))}`}</span></div>
      <div class="sec-h"><b>工作流运行</b><span>由工作流任务分派的 Pi</span></div>
      <div class="list">${list}</div>
      ${independentRows()}
      <div class="sec-h"><b>最近交付</b><span>最终代码交付仅指代码，不代表已发布或客户验收</span></div>
      <div class="list">${dRows}</div>`;
  }

  // ---- Tasks view ------------------------------------------------------------
  const latestRun = (t) => arr(I().runsByTask.get(t.id)).slice(-1)[0];
  function renderTasks() {
    const sec = ref('view-tasks');
    if (!state.loaded) { sec.innerHTML = `<div class="list"><div class="empty">${state.disconnected ? '无法读取任务。' : '正在读取任务…'}</div></div>`; return; }
    const scoped = I().tasks.filter((t) => inProject(t.projectId));
    const q = state.query.trim().toLowerCase();
    const matched = scoped.filter((t) => !q || [t.title, t.goal, t.id, issueText(t), projName(t.projectId), latestRun(t)?.agentId].map((x) => str(x, 500)).join(' ').toLowerCase().includes(q));
    const inFilter = (t, f) => f === 'all' || taskCategory(t) === f;
    const shown = matched.filter((t) => inFilter(t, state.filter));
    const count = (f) => matched.filter((t) => inFilter(t, f)).length;
    const rows = shown.length ? shown.map((t) => {
      const r = latestRun(t);
      const cat = taskCategory(t);
      return `<button type="button" class="row task-row" data-open-task="${esc(t.id)}">
        <span class="who"><span class="name">${esc(str(t.title, 200) || '未命名任务')}</span>
        <span class="sub"><code>${esc(short(t.id))}</code> · ${esc(projName(t.projectId))} · ${esc(issueText(t))} · 更新 ${esc(formatLocalTime(t.updatedAt, now()))}</span></span>
        <span class="meta"><span class="badge ${cat === 'delivered' ? 'green' : (cat === 'attention' ? 'red' : '')}">${esc(str(t.state, 40) || '未知')}</span>${r ? `<span class="badge">${esc(roleLabel(r.role))} · ${esc(runLabel(r.state))}</span><span class="owner">${esc(str(r.agentId, 40))}</span>` : ''}</span>
      </button>`;
    }).join('') : `<div class="empty">${I().tasks.length ? '没有匹配的任务。' : '还没有工作流任务。'}</div>`;
    sec.innerHTML = `
      <div class="toolbar">
        <label class="sr-only" for="mk-task-search">搜索任务</label>
        <input id="mk-task-search" class="search" type="search" placeholder="搜索标题、Issue、Agent…" value="${esc(state.query)}" autocomplete="off">
        <div class="mini-seg" role="group" aria-label="任务筛选">
          ${FILTERS.map(([k, l]) => `<button type="button" data-filter="${k}" aria-pressed="${state.filter === k}">${l}<span class="n">${count(k)}</span></button>`).join('')}
        </div>
      </div>
      <div class="list">${rows}</div>`;
  }

  // ---- Usage view ------------------------------------------------------------
  const tokTxt = (u, r) => (u ? COUNTERS.map((k) => `${{ input: '入', output: '出', cacheRead: '缓存读', cacheWrite: '缓存写' }[k]} ${u[k] === null ? '未返回' : num(u[k])}`).join(' · ') : (r.endedAt ? '用量未返回' : '运行中，尚未返回用量'));
  const usd = (v) => `US$${v.toFixed(v < 1 ? 4 : 2)}`;
  const feeTxt = (c) => (c.kind === 'reported' ? usd(c.usd) : (c.kind === 'estimated' ? `≈${usd(c.usd)}（估算）` : '未知'));
  function ioCell(s) {
    const missing = s.missing.input + s.missing.output;
    return s.reported ? `${missing ? '≥ ' : ''}${num(s.known.input + s.known.output)}` : '未知';
  }
  function renderUsage() {
    const sec = ref('view-usage');
    if (!state.loaded) { sec.innerHTML = `<div class="list"><div class="empty">${state.disconnected ? '无法读取用量。' : '正在读取用量…'}</div></div>`; return; }
    const t0 = now();
    const runs = I().runs.filter((r) => inProject(taskOf(r)?.projectId) && parseTime(r.startedAt) !== null);
    const queued = I().runs.filter((r) => inProject(taskOf(r)?.projectId) && parseTime(r.startedAt) === null).length;
    if (!runs.length) { sec.innerHTML = `<div class="list"><div class="empty">暂无已开始的运行记录${queued ? `（${queued} 条排队中，不计入用量）` : ''}。</div></div>`; return; }
    const s = summarizeUsage(runs, t0);
    const completed = runs.filter((r) => r.endedAt).length;
    const groups = new Map();
    for (const r of runs) { const k = `${str(r.role)}|${modelLabel(r.modelSnapshot) || '模型未记录'}`; if (!groups.has(k)) groups.set(k, []); groups.get(k).push(r); }
    const groupRows = [...groups].map(([k, rs]) => {
      const [role, model] = k.split('|');
      const g = summarizeUsage(rs, t0);
      return `<div class="row stat-row">
        <span class="who"><span class="name">${esc(roleLabel(role))} · ${esc(model)}</span>
        <span class="sub">${rs.length} 次运行 · 用量完整 ${g.full}/${rs.length} · Agent 耗时合计 ${esc(formatDuration(g.agentSeconds))}</span></span>
        <span class="num"><b>${ioCell(g)}</b><small>输入 + 输出（已上报）</small></span>
        <span class="num"><b>${g.missing.cacheRead < rs.length ? num(g.known.cacheRead) : '未知'} / ${g.missing.cacheWrite < rs.length ? num(g.known.cacheWrite) : '未知'}</b><small>缓存 读 / 写</small></span>
        <span class="num"><b>${g.usdRuns ? usd(g.usd) : '未知'}</b><small>${g.usdRuns}/${rs.length} 次返回 USD 费用</small></span>
      </div>`;
    }).join('');

    // Flow metrics, only from records that exist; denominators shown.
    const tasks = [...new Set(runs.map((r) => r.taskId))].map((id) => I().task.get(id)).filter(Boolean);
    const reviewed = tasks.filter((t) => arr(I().reviewsByTask.get(t.id)).length);
    const passWord = /^(pass|passed|approve|approved|ok|lgtm|no[_-]?findings)$/i;
    const firstPass = reviewed.filter((t) => passWord.test(str(arr(I().reviewsByTask.get(t.id))[0].verdict))).length;
    const fixRuns = reviewed.reduce((a, t) => a + Math.max(0, arr(I().runsByTask.get(t.id)).filter((r) => r.role === 'developer' || r.role === 'fixer').length - 1), 0);
    const reviewSec = summarizeUsage(runs.filter((r) => r.role === 'reviewer'), t0).agentSeconds;
    const fixCap = I().settings && Number.isInteger(I().settings.maxFixRounds) ? `（未来运行上限 ${I().settings.maxFixRounds} 轮）` : '';

    const runRows = runs.map((r) => {
      const u = runUsage(r);
      const c = runCost(r);
      const full = u?.completeness === 'complete';
      return `<div class="row stat-row">
        <span class="who"><span class="name"><code>${esc(short(r.id))}</code> · ${esc(str(r.agentId, 40))} · ${esc(roleLabel(r.role))} · ${esc(runLabel(r.state))}</span>
        <span class="sub">${esc(modelLabel(r.modelSnapshot) || '模型未记录')} · ${esc(tokTxt(u, r))}</span></span>
        <span class="num"><b>${since(r.startedAt, r.endedAt)}${r.endedAt ? '' : '+'}</b><small>${esc(formatLocalTime(r.startedAt, t0))}–${r.endedAt ? esc(formatLocalTime(r.endedAt, t0)) : '进行中'}</small></span>
        <span class="num"><b>${esc(feeTxt(c))}</b><small>${full ? '用量完整' : (u ? '用量部分' : '用量未返回')}</small></span>
      </div>`;
    }).join('');

    const subtotalMissing = s.missing.input + s.missing.output;
    sec.innerHTML = `
      ${state.disconnected ? '<div class="stale-banner" role="status"><span><b>快照</b> · 已断连，以下用量为最近快照，不是实时数据。</span></div>' : ''}
      <div class="sec-h"><b>用量</b><span>来自运行记录；未返回的值显示为未知，不按 0 计、不估算</span></div>
      <div class="stats">
        <div class="kpi"><b>${runs.length}</b><span>已开始运行</span><small>完成 ${completed} · 进行中 ${runs.length - completed}${queued ? ` · 排队 ${queued} 不计入` : ''}</small></div>
        <div class="kpi"><b>${ioCell(s)}</b><span>输入 + 输出 tokens</span><small>${s.reported}/${runs.length} 次有上报${subtotalMissing ? `，缺 ${subtotalMissing} 项，非完整总量` : ''}</small></div>
        <div class="kpi"><b>${s.missing.cacheRead < runs.length ? num(s.known.cacheRead) : '未知'} / ${s.missing.cacheWrite < runs.length ? num(s.known.cacheWrite) : '未知'}</b><span>缓存 读 / 写</span><small>上报 ${runs.length - s.missing.cacheRead} / ${runs.length - s.missing.cacheWrite} 次</small></div>
        <div class="kpi"><b>${s.usdRuns ? usd(s.usd) : '未知'}</b><span>服务商返回费用（USD）</span><small>${s.usdRuns}/${runs.length} 次返回${s.estRuns ? ` · 另有 ${s.estRuns} 次估算 ≈${usd(s.estUsd)} 未计入` : ''} · ${s.unknownFee} 次未知</small></div>
        <div class="kpi"><b>${esc(formatDuration(s.wallSeconds))}</b><span>墙钟时间</span><small>Agent 耗时合计 ${esc(formatDuration(s.agentSeconds))}${s.unknownTime ? ` · ${s.unknownTime} 次时间未知` : ''}</small></div>
      </div>
      <div class="sec-h"><b>按角色 / 模型</b><span>“≥” 表示有运行未返回用量；缓存读 / 写单独列出，不计入输入 + 输出小计</span></div>
      <div class="list">${groupRows}</div>
      <div class="sec-h"><b>流程指标</b><span>仅基于已有检查记录</span></div>
      <div class="list">
        <div class="row stat-row"><span class="who"><span class="name">初次交付一次通过</span><span class="sub">首次检查结论为通过的任务 / 已有检查记录的任务</span></span><span class="num"><b>${reviewed.length ? `${firstPass} / ${reviewed.length}` : '—'}</b><small>任务</small></span></div>
        <div class="row stat-row"><span class="who"><span class="name">定向修复次数</span><span class="sub">首轮之后的开发运行数 / 已检查任务数${esc(fixCap)}</span></span><span class="num"><b>${reviewed.length ? `${fixRuns} / ${reviewed.length}` : '—'}</b><small>次 / 任务</small></span></div>
        <div class="row stat-row"><span class="who"><span class="name">检查耗时占比</span><span class="sub">检查运行耗时 / Agent 耗时合计</span></span><span class="num"><b>${esc(formatDuration(reviewSec))} / ${esc(formatDuration(s.agentSeconds))}</b><small>${s.agentSeconds ? `${Math.round((reviewSec / s.agentSeconds) * 100)}%` : '—'}</small></span></div>
      </div>
      <div class="sec-h"><b>逐次运行</b><span>${runs.length} 次</span></div>
      <div class="list">${runRows}</div>`;
  }

  // ---- Task detail drawer ----------------------------------------------------
  const listOr = (items, empty, cls = '') => (arr(items).length ? `<ul class="${cls}">${arr(items).slice(0, 100).map((x) => `<li>${esc(itemText(x))}</li>`).join('')}</ul>` : `<p class="k">${empty}</p>`);
  function summaryFields(sum) {
    // Only bounded structural metadata from the run summary; never raw text blobs.
    if (!sum || typeof sum !== 'object') return '';
    const keys = [['outcome', '结果'], ['status', '状态'], ['verdict', '结论'], ['candidateSha', '候选'], ['change', '变更'], ['reason', '原因'], ['error', '错误']];
    return keys.filter(([k]) => str(sum[k])).map(([k, l]) => `<dt>${l}</dt><dd>${k === 'candidateSha' ? `<code>${esc(short(sum[k], 12))}</code>` : esc(str(sum[k], 300))}</dd>`).join('');
  }

  function drawerBody(t) {
    const runs = arr(I().runsByTask.get(t.id));
    const deliveries = arr(I().deliveriesByTask.get(t.id));
    const reviews = arr(I().reviewsByTask.get(t.id));
    if (state.drawerTab === 'overview') {
      const b = t.budget && typeof t.budget === 'object' ? t.budget : null;
      const deps = arr(t.dependencies).map((id) => { const d = I().task.get(id); return d ? `${str(d.title, 120)}（${str(d.state, 40)}）` : short(id); });
      return `
        <div class="card"><h3>目标</h3><p>${esc(str(t.goal, MAX_TEXT) || '—')}</p></div>
        <div class="card"><h3>范围</h3>${listOr(t.scope, '未声明范围。', 'files')}</div>
        <div class="card"><h3>可观察的验收标准</h3>${listOr(t.acceptance, '未声明验收标准。')}</div>
        <div class="card"><h3>状态</h3><dl class="concl">
          <dt>任务状态</dt><dd>${esc(str(t.state, 60) || '未知')}${pendingResume(t) ? ` · 待恢复：${esc(roleLabel(pendingResume(t)))}` : ''}</dd>
          <dt>依赖</dt><dd>${deps.length ? esc(deps.join('；')) : '无'}</dd>
          <dt>预算</dt><dd>${b ? `${isNum(b.maxTokens) ? `${num(b.maxTokens)} tokens` : 'tokens 未设'} · ${isNum(b.maxWallSeconds) ? esc(formatDuration(b.maxWallSeconds)) : '时限未设'} · 修复 ${isNum(b.maxFixRounds) ? b.maxFixRounds : '—'} 轮` : '未记录'}</dd>
          <dt>创建 / 更新</dt><dd>${timeEl(t.createdAt)} / ${timeEl(t.updatedAt)}</dd>
          <dt>工作目录</dt><dd><code>${esc(str(t.worktree, 500) || '—')}</code></dd></dl></div>`;
    }
    if (state.drawerTab === 'context') {
      const c = ctxOf(t.contextRef);
      const versions = I().contexts.filter((x) => x.projectId === t.projectId).sort((a, b) => (Number(a.version) || 0) - (Number(b.version) || 0));
      const used = [...new Set(runs.map((r) => ctxLabel(r.contextRef)))];
      if (!c) return `<div class="card"><p class="k">未找到任务引用的共享上下文（${esc(ctxLabel(t.contextRef))}）。</p></div>`;
      return `
        <div class="card"><h3>共享上下文 v${esc(str(c.version))} · 不可变</h3>
          <dl class="concl"><dt>摘要</dt><dd><code>${esc(str(c.digest, 80))}</code></dd><dt>创建</dt><dd>${timeEl(c.createdAt)}</dd></dl>
          <p class="ctx-text mt">${esc(str(c.text, MAX_TEXT))}${str(c.text, MAX_TEXT + 1).length > MAX_TEXT ? '…' : ''}</p></div>
        <div class="card"><h3>来源</h3>${arr(c.sources).length ? `<ul>${arr(c.sources).slice(0, 50).map((s) => { const href = safeHref(s?.url); const title = str(s?.title, 200) || str(s?.url, 200) || '来源'; return `<li>${href ? `<a href="${esc(href)}" target="_blank" rel="noopener noreferrer">${esc(title)}</a>` : esc(title)}${s?.hash ? ` · <code>${esc(short(s.hash, 12))}</code>` : ''}</li>`; }).join('')}</ul>` : '<p class="k">无来源记录。</p>'}</div>
        <div class="card"><h3>项目上下文版本</h3><ul>${versions.map((v) => `<li>v${esc(str(v.version))} · <code>${esc(short(v.digest, 12))}</code> · ${timeEl(v.createdAt)}${v.id === c.id ? ' · 本任务' : ''}</li>`).join('')}</ul></div>
        <div class="card"><h3>运行使用的上下文版本</h3>${used.length ? `<ul>${used.map((x) => `<li>${esc(x)}</li>`).join('')}</ul>` : '<p class="k">尚无运行接收。</p>'}</div>`;
    }
    if (state.drawerTab === 'delivery') {
      if (!deliveries.length && !reviews.length) return '<div class="card"><p class="k">尚无交付。开发完成后会在此显示基线、候选与检查结论。</p></div>';
      const polish = runs.filter((r) => r.role === 'polisher');
      return `
        <div class="card"><h3>版本</h3><dl class="concl">
          <dt>基线</dt><dd><code>${esc(str(t.baselineSha, 40) || '—')}</code></dd>
          <dt>当前候选</dt><dd><code>${esc(str(t.candidateSha, 40) || '—')}</code></dd>
          <dt>仓库</dt><dd><code>${esc(str(t.repository, 500) || '—')}</code></dd></dl></div>
        ${deliveries.map((d) => `<div class="card"><h3>${esc(DELIVERY_LABEL[d.state] || str(d.state) || '交付')}</h3><dl class="concl">
          <dt>候选</dt><dd><code>${esc(str(d.candidateSha, 40) || '—')}</code></dd>
          <dt>上下文</dt><dd>${esc(ctxLabel(d.contextRef))}</dd>
          <dt>运行</dt><dd>${arr(d.runIds).map((id) => `<code>${esc(short(id))}</code>`).join(' ') || '—'}</dd></dl>
          <h3 class="mt">检查</h3>${listOr(d.checks, '未记录检查。')}
          <h3 class="mt">已知缺口</h3>${listOr(d.knownGaps, '未报告缺口。')}</div>`).join('')}
        ${reviews.map((rv) => `<div class="card"><h3>审查 · ${esc(str(rv.verdict, 60) || '无结论')}</h3><dl class="concl">
          <dt>审查候选</dt><dd><code>${esc(str(rv.candidateSha, 40) || '—')}</code>${t.candidateSha && rv.candidateSha && rv.candidateSha !== t.candidateSha ? ' <span class="badge amber">非当前候选</span>' : ''}</dd>
          <dt>上下文摘要</dt><dd><code>${esc(short(rv.contextDigest, 12) || '—')}</code></dd></dl>
          <h3 class="mt">发现</h3>${listOr(rv.findings, '无发现。')}
          <h3 class="mt">检查</h3>${listOr(rv.checks, '未记录检查。')}</div>`).join('')}
        ${polish.length ? `<div class="card"><h3>精修与复验</h3>${polish.map((r) => `<dl class="concl"><dt>精修运行</dt><dd><code>${esc(short(r.id))}</code> · ${esc(runLabel(r.state))}</dd>${summaryFields(r.summary)}</dl>`).join('')}<p class="k mt small">精修后必须再经检查（复验）才成为最终代码交付。</p></div>` : ''}
        <p class="k small">“最终代码交付”只代表代码，不代表已发布、Preview 已验证或客户验收。</p>`;
    }
    if (!runs.length) return '<div class="card"><p class="k">尚无运行记录。</p></div>';
    return `<div class="card"><div class="run-table">${runs.map((r) => {
      const u = runUsage(r);
      return `<div class="run">
        <span><span class="rid">${esc(short(r.id))}</span> · <span class="rt">${esc(str(r.agentId, 40))} · ${esc(roleLabel(r.role))}</span></span>
        <span class="rr">${esc(runLabel(r.state))}</span>
        <span class="rm">${esc(modelLabel(r.modelSnapshot) || '模型未记录')} · ${timeEl(r.startedAt)}–${r.endedAt ? timeEl(r.endedAt) : '进行中'} · ${r.startedAt ? `${since(r.startedAt, r.endedAt)}${r.endedAt ? '' : '+'}` : '未开始'} · 用量 ${u ? esc(tokTxt(u, r)) : UNK} · 费用 ${runCost(r).kind === 'unknown' ? UNK : esc(feeTxt(runCost(r)))}</span>
        ${r.summary ? `<dl class="concl">${summaryFields(r.summary)}</dl>` : ''}
      </div>`;
    }).join('')}</div></div>`;
  }

  function renderDrawer() {
    const t = I().task.get(state.drawer);
    const box = ref('drawer');
    if (!t) { box.innerHTML = `<div class="dlg-h"><div><h2 id="mk-drawer-title">任务不存在</h2></div><button type="button" class="icon" data-act="drawer-close" aria-label="关闭任务详情">${CLOSE}</button></div><div class="dlg-b"><p class="k">该任务已不在快照中。</p></div>`; return; }
    const deliveries = arr(I().deliveriesByTask.get(t.id));
    const p = phaseIndex(t, deliveries);
    const cat = taskCategory(t);
    const done = t.state === 'delivered';
    const phases = PHASES.map((name, i) => {
      let cls = i < p || (done && i === p) ? 'done' : (i === p ? 'cur' : '');
      if (cat === 'attention' && i === p) cls = 'block';
      return `<li class="${cls}"${i === p ? ' aria-current="step"' : ''}>${name}</li>`;
    }).join('');
    const finalState = done ? '<span class="badge green">最终代码交付</span><span>精修已复验；是否发布或客户验收不在此判断</span>'
      : cat === 'attention' ? `<span class="badge red">${esc(str(t.state, 40))}</span><span>需要 Codex 处理</span>`
        : `<span class="badge">${PHASES[p]}</span><span>${esc(str(t.state, 60))}</span>`;
    box.innerHTML = `
      <div class="dlg-h">
        <div>
          <h2 id="mk-drawer-title">${esc(str(t.title, 200) || '未命名任务')}</h2>
          <div class="sub"><span><code>${esc(short(t.id))}</code> · ${esc(projName(t.projectId))}</span> ${issueLink(t, 'issue-chip')}</div>
        </div>
        <button type="button" class="icon" data-act="drawer-close" aria-label="关闭任务详情">${CLOSE}</button>
      </div>
      <div class="dlg-b">
        ${state.disconnected ? '<p class="local-note">快照（已过期），不代表当前状态。</p>' : ''}
        <ol class="phases" aria-label="阶段">${phases}</ol>
        <div class="final-state">${finalState}</div>
        <div class="mini-seg tabs" role="tablist" aria-label="任务详情分区">
          ${TABS.map(([k, l]) => `<button type="button" role="tab" id="mk-tab-${k}" data-tab="${k}" aria-selected="${state.drawerTab === k}" aria-controls="mk-tabpanel" tabindex="${state.drawerTab === k ? 0 : -1}">${l}</button>`).join('')}
        </div>
        <div id="mk-tabpanel" role="tabpanel" aria-labelledby="mk-tab-${state.drawerTab}">${drawerBody(t)}</div>
      </div>`;
  }

  function openTask(id, opener) {
    state.drawer = id;
    state.drawerTab = 'overview';
    state.returnFocus = opener || active();
    renderDrawer();
    ref('drawer-scrim').hidden = false;
    $('[data-act="drawer-close"]').focus();
  }

  function closeDialog(which) {
    const scrim = ref(`${which}-scrim`);
    if (scrim.hidden) return;
    scrim.hidden = true;
    if (which === 'drawer') state.drawer = null;
    const back = state.returnFocus;
    state.returnFocus = null;
    if (back && root.contains(back)) back.focus();
    else if (back?.dataset?.openTask) root.querySelector(`[data-open-task="${CSS.escape(back.dataset.openTask)}"]`)?.focus();
  }

  // ---- Settings sheet --------------------------------------------------------
  function renderSettings() {
    const s = I().settings;
    const pid = state.project;
    const profiles = I().profiles;
    const disabled = readonly || !s || !!state.disconnected || state.settingsBusy;
    const why = readonly ? readonlyNote : (!s ? '尚无工作流设置记录（未运行过 coordinator），无法修改。' : (state.disconnected ? '已断连，无法保存设置。' : ''));
    const dis = disabled ? ' disabled' : '';
    const cur = s?.defaultProfiles?.[pid] || {};
    const roleRows = pid === 'all'
      ? '<div class="set-row"><span class="lbl">选择具体项目后可设置默认配置<small>只能从该项目已注册的配置 ID 中选择</small></span></div>'
      : ROLES.map((role) => {
        const opts = profiles.filter((p) => p.projectId === pid && p.role === role);
        const label = (p) => `${str(p.id, 128)} · ${[str(p.provider, 40), str(p.model, 80)].filter(Boolean).join('/')}`;
        return `<div class="set-row"><span class="lbl">${ROLE[role]}${role === 'polisher' ? '<small>最强配置；精修后必须复验</small>' : ''}</span>
          <span class="ctl">${opts.length ? `<select data-profile="${role}" aria-label="${ROLE[role]} 默认配置"${dis}><option value="">（保持当前）</option>${opts.map((p) => `<option value="${esc(p.id)}"${cur[role] === p.id ? ' selected' : ''}>${esc(label(p))}</option>`).join('')}</select>` : '<span class="k small">该项目没有已注册的配置</span>'}</span></div>`;
      }).join('');
    const conc = Number.isInteger(s?.maxConcurrency) ? s.maxConcurrency : 2;
    const rounds = Number.isInteger(s?.maxFixRounds) ? s.maxFixRounds : 2;
    ref('settings').innerHTML = `
      <div class="dlg-h"><div><h2 id="mk-settings-title">设置</h2><div class="sub">只影响之后新开始的运行；正在运行的模型与配置不会改变。</div></div>
        <button type="button" class="icon" data-act="settings-close" aria-label="关闭设置">${CLOSE}</button></div>
      <div class="dlg-b">
        ${why ? `<p class="local-note">${esc(why)}</p>` : ''}
        <div class="sec-h"><b>调度</b></div>
        <div class="list">
          <div class="set-row"><label class="lbl" for="mk-set-conc">本地并发<small>同时运行的 Pi 实例数（1–4）</small></label><span class="ctl"><select id="mk-set-conc" data-setting="maxConcurrency"${dis}>${[1, 2, 3, 4].map((v) => `<option${v === conc ? ' selected' : ''}>${v}</option>`).join('')}</select></span></div>
          <div class="set-row"><label class="lbl" for="mk-set-rounds">最大修复轮数<small>达到后停止并交给 Codex（0–2）</small></label><span class="ctl"><select id="mk-set-rounds" data-setting="maxFixRounds"${dis}>${[0, 1, 2].map((v) => `<option${v === rounds ? ' selected' : ''}>${v}</option>`).join('')}</select></span></div>
        </div>
        <div class="sec-h"><b>默认角色配置</b><span>${pid === 'all' ? '' : esc(projName(pid))}</span></div>
        <div class="list">${roleRows}</div>
        <div class="inline-actions mt"><button type="button" class="btn primary" data-act="settings-save"${dis}>${state.settingsBusy ? '正在保存…' : '保存（下次运行生效）'}</button></div>
        <p class="applied" data-ref="applied" role="status" aria-live="polite">${esc(state.settingsMsg)}</p>
      </div>`;
  }

  function openSettings() {
    state.returnFocus = $('[data-act="settings"]');
    state.settingsMsg = '';
    renderSettings();
    ref('settings-scrim').hidden = false;
    $('[data-act="settings-close"]').focus();
  }

  async function saveSettings() {
    const input = {};
    for (const el of ref('settings').querySelectorAll('[data-setting]')) input[el.dataset.setting] = Number(el.value);
    if (state.project !== 'all') {
      const map = {};
      for (const el of ref('settings').querySelectorAll('[data-profile]')) if (el.value) map[el.dataset.profile] = el.value;
      if (Object.keys(map).length) input.defaultProfiles = { [state.project]: map };
    }
    state.settingsBusy = true;
    state.settingsMsg = '';
    keepFocus(renderSettings);
    try {
      await onAction({ type: 'settings', input });
      state.settingsMsg = '已保存：下一次新运行生效，当前运行不受影响。';
    } catch (e) {
      state.settingsMsg = `保存失败：${str(e?.message, 200) || '未知错误'}`;
    }
    state.settingsBusy = false;
    if (!ref('settings-scrim').hidden) { keepFocus(renderSettings); ref('applied')?.classList.toggle('err', state.settingsMsg.startsWith('保存失败')); }
  }

  async function requestStop(runId) {
    const prev = state.stop.get(runId);
    if (prev?.status === 'pending' || prev?.status === 'acked') return;
    // One requestId per stop attempt for this run; retries reuse it so the backend can dedupe.
    const entry = { requestId: prev?.requestId || uuid(), status: 'pending', message: '' };
    state.stop.set(runId, entry);
    keepFocus(render);
    try {
      await onAction({ type: 'stop', runId, requestId: entry.requestId });
      entry.status = 'acked';
      toast('停止请求已送达；进程是否停止以运行状态为准');
    } catch (e) {
      entry.status = 'error';
      entry.message = str(e?.message, 200) || '未知错误';
    }
    keepFocus(render);
  }

  async function reconnect() {
    if (state.reconnecting) return;
    state.reconnecting = true;
    keepFocus(render);
    try { await onAction({ type: 'reconnect' }); } catch (e) { if (state.disconnected) state.disconnected = str(e?.message, 200) || state.disconnected; }
    state.reconnecting = false;
    keepFocus(render);
  }

  // ---- Render & events -------------------------------------------------------
  function render() {
    renderSummary();
    for (const v of ['agents', 'tasks', 'usage']) ref(`view-${v}`).hidden = state.view !== v;
    for (const b of ref('nav').querySelectorAll('button')) {
      const isOn = b.dataset.view === state.view;
      b.classList.toggle('on', isOn);
      if (isOn) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current');
    }
    if (state.view === 'agents') renderAgents();
    if (state.view === 'tasks') renderTasks();
    if (state.view === 'usage') renderUsage();
    if (state.drawer) renderDrawer();
  }

  function setTheme(theme) {
    root.dataset.theme = theme;
    $('[data-act="theme"]').setAttribute('aria-label', theme === 'dark' ? '切换到浅色主题' : '切换到深色主题');
    if (themeKey) { try { globalThis.localStorage?.setItem(themeKey, theme); } catch { /* storage unavailable */ } }
  }

  on('click', (e) => {
    const t = e.target;
    if (!(t instanceof Element)) return;
    const nav = t.closest('[data-view]');
    if (nav) { state.view = nav.dataset.view; keepFocus(render); return; }
    const act = t.closest('[data-act]')?.dataset.act;
    if (act === 'theme') { setTheme(root.dataset.theme === 'dark' ? 'light' : 'dark'); return; }
    if (act === 'settings') { openSettings(); return; }
    if (act === 'drawer-close') { closeDialog('drawer'); return; }
    if (act === 'settings-close') { closeDialog('settings'); return; }
    if (act === 'settings-save') { saveSettings(); return; }
    if (act === 'reconnect') { reconnect(); return; }
    if (t === ref('drawer-scrim')) { closeDialog('drawer'); return; }
    if (t === ref('settings-scrim')) { closeDialog('settings'); return; }
    const openT = t.closest('[data-open-task]');
    if (openT && openT.dataset.openTask) { openTask(openT.dataset.openTask, openT); return; }
    const stop = t.closest('[data-stop]');
    if (stop) { requestStop(stop.dataset.stop); return; }
    const ag = t.closest('[data-agent]');
    if (ag) {
      const id = ag.dataset.agent;
      if (state.expanded.has(id)) state.expanded.delete(id); else state.expanded.add(id);
      keepFocus(render);
      return;
    }
    const f = t.closest('[data-filter]');
    if (f) { state.filter = f.dataset.filter; keepFocus(render); return; }
    const tab = t.closest('[data-tab]');
    if (tab) { state.drawerTab = tab.dataset.tab; renderDrawer(); $(`#mk-tab-${state.drawerTab}`).focus(); }
  });

  on('input', (e) => {
    if (e.target.id === 'mk-task-search') { state.query = e.target.value; keepFocus(renderTasks); }
  });

  on('change', (e) => {
    if (e.target === ref('project')) { state.project = e.target.value; keepFocus(render); if (!ref('settings-scrim').hidden) renderSettings(); }
  });

  on('keydown', (e) => {
    const tab = e.target.closest?.('[role="tab"]');
    if (tab && (e.key === 'ArrowRight' || e.key === 'ArrowLeft' || e.key === 'Home' || e.key === 'End')) {
      const i = TABS.findIndex(([k]) => k === state.drawerTab);
      const n = e.key === 'Home' ? 0 : e.key === 'End' ? TABS.length - 1 : (i + (e.key === 'ArrowRight' ? 1 : TABS.length - 1)) % TABS.length;
      state.drawerTab = TABS[n][0];
      renderDrawer();
      $(`#mk-tab-${state.drawerTab}`).focus();
      e.preventDefault();
      return;
    }
    const open = !ref('settings-scrim').hidden ? 'settings' : (!ref('drawer-scrim').hidden ? 'drawer' : null);
    if (!open) return;
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeDialog(open); return; }
    if (e.key === 'Tab') {
      const box = ref(open);
      const items = [...box.querySelectorAll('button, select, input, [href], [tabindex]:not([tabindex="-1"])')].filter((x) => !x.disabled && x.getClientRects().length);
      if (!items.length) { e.preventDefault(); box.focus(); return; }
      const first = items[0];
      const last = items[items.length - 1];
      const a = active();
      if (e.shiftKey && (a === first || !box.contains(a))) { last.focus(); e.preventDefault(); } else if (!e.shiftKey && (a === last || !box.contains(a))) { first.focus(); e.preventDefault(); }
    }
  });

  // Running durations tick locally between polls (never while the snapshot is stale).
  const ticker = setInterval(() => {
    if (state.disconnected) return;
    for (const el of root.querySelectorAll('[data-since]')) el.textContent = formatDuration(elapsedSeconds(el.dataset.since, null));
  }, 1000);

  let saved = options.theme === 'dark' || options.theme === 'light' ? options.theme : 'light';
  if (!options.theme && themeKey) { try { saved = globalThis.localStorage?.getItem(themeKey) === 'dark' ? 'dark' : 'light'; } catch { /* storage unavailable */ } }
  setTheme(saved);
  render();

  return {
    /** Renders a fresh snapshot (core readWorkflow shape) plus the separate legacy single-run list. */
    update(snapshot, legacyActive = []) {
      state.idx = indexSnapshot(snapshot);
      state.live = liveRunSummary(snapshot, legacyActive);
      state.legacy = state.live.list;
      state.loaded = true;
      state.disconnected = null;
      for (const [id] of state.stop) { const r = state.idx.runs.find((x) => x.id === id); if (!r || !isActiveRun(r)) state.stop.delete(id); }
      renderProjects();
      keepFocus(render);
    },
    /** Marks the last snapshot stale (run count unknown) and offers a manual reconnect. */
    setDisconnected(message) {
      state.disconnected = str(message, 200) || '无法连接';
      keepFocus(render);
    },
    destroy() {
      clearInterval(ticker);
      clearTimeout(toastTimer);
      for (const [type, fn] of listeners) root.removeEventListener(type, fn);
      listeners.length = 0;
      root.innerHTML = '';
    },
  };
}

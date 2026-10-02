import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import type { LegacyActive, Run, SettingsInput, Snapshot, Task } from './generated/workflow';
import {
  DELIVERY_LABEL, ROLES, ROLE_LABEL, agentLabel, checkView, type CheckEntry, dedupLegacy, formatDuration, formatTime, isActiveRun, lastEvent,
  modelLabel, num, roleLabel, runLabel, runTokens, safeHttpsUrl, short, summarizeUsage, taskCategory, taskLabel,
} from './model';
import { newRequestId } from './transport';

/** Host actions. In readonly hosts stop/settings are disabled; reconnect may still be offered. */
export interface AppActions {
  readonly: boolean;
  readonlyNote?: string;
  stop(runId: string, requestId: string): Promise<unknown>;
  settings(input: SettingsInput): Promise<unknown>;
  reconnect?(): Promise<unknown>;
}

export interface AppProps {
  snapshot: Snapshot | null;
  legacyActive: LegacyActive[];
  connected: boolean;
  /** Non-null while the last snapshot is stale (disconnected / failed). */
  stale: string | null;
  actions: AppActions;
  initialTheme?: 'light' | 'dark';
  now?: () => number;
}

type View = 'agents' | 'tasks' | 'usage';
type StopEntry = { requestId: string; status: 'pending' | 'accepted' | 'error'; message: string };

const DEFAULT_RO_NOTE = '此宿主为只读视图；请使用 coordinator CLI 停止运行或修改设置。';

function Logo() {
  return (
    <svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true">
      <g transform="translate(2.179 1) scale(.03944194)"><g transform="translate(-475 829.791944) scale(.1 -.1)">
        <path d="M7212 8289c-377-63-605-456-473-817 29-81 100-200 140-236 21-19 21-19 21-1910 0-1678-2-1899-16-1951-30-118-108-216-213-268-66-32-66-32-993-35l-928-2v-350h928c892 0 932 1 1006 20 201 51 381 201 476 395 81 166 74-34 77 2222 3 2012 3 2012-39 2041-282 196-179 581 152 574 138-3 300-39 635-141 182-55 440-133 575-174 245-73 245-73 248-104 4-42-38-90-106-120-29-12-146-61-260-107-326-131-432-197-556-341-153-180-253-464-231-657 3-23 21-212 40-418 45-477 36-432 87-448 304-95 632-401 811-758 104-208 197-574 197-780 0-44 0-44 171-44h172l-6 138c-30 708-421 1357-1014 1684-73 40-73 40-73 82 0 23-11 158-25 300-29 300-30 329-10 416 29 122 89 225 190 326 102 102 129 118 509 293 120 56 237 116 259 133 166 132 197 435 55 538-24 17-102 48-201 80-89 28-364 117-612 197-668 218-818 251-993 222zM7650 4936c0-193 0-193 67-232 233-135 434-454 474-752 49-375-87-686-478-1092l-135-140h2152v350h-1380l46 91c314 626 116 1427-448 1817-78 53-263 152-285 152-10 0-13-45-13-194z" />
      </g></g>
    </svg>
  );
}
const Close = () => <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" strokeWidth="1.5" /></svg>;

function IssueLink({ task }: { task: Task }) {
  const url = safeHttpsUrl(task.issueRef?.url);
  const label = task.issueRef?.title || (url ? 'Issue' : '无 Issue');
  return url ? <a className="link" href={url} target="_blank" rel="noopener noreferrer">{label}</a> : <span>{label}</span>;
}

export function App({ snapshot, legacyActive, connected, stale, actions, initialTheme, now = Date.now }: AppProps) {
  const [view, setView] = useState<View>('agents');
  const [project, setProject] = useState('all');
  const [theme, setTheme] = useState<'light' | 'dark'>(initialTheme ?? 'light');
  const [openTask, setOpenTask] = useState<string | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [stops, setStops] = useState<Record<string, StopEntry>>({});
  const [reconnecting, setReconnecting] = useState(false);
  const mounted = useRef(true);
  useEffect(() => () => { mounted.current = false; }, []);

  const writable = !actions.readonly && !stale && connected && !!snapshot;
  const whyDisabled = actions.readonly ? actions.readonlyNote || DEFAULT_RO_NOTE : stale ? '已断连，快照已过期，写操作已禁用。' : '';

  const idx = useMemo(() => {
    const s = snapshot;
    const task = new Map((s?.tasks ?? []).map((t) => [t.id, t]));
    const proj = new Map((s?.projects ?? []).map((p) => [p.id, p]));
    return { task, proj };
  }, [snapshot]);
  const inProject = (pid: string | undefined) => project === 'all' || pid === project;
  const taskOf = (r: Run) => idx.task.get(r.taskId);
  const independent = useMemo(() => dedupLegacy(snapshot, legacyActive), [snapshot, legacyActive]);

  const requestStop = useCallback(async (runId: string) => {
    const prev = stops[runId];
    if (prev && prev.status !== 'error') return;
    const entry: StopEntry = { requestId: prev?.requestId ?? newRequestId(), status: 'pending', message: '' };
    setStops((m) => ({ ...m, [runId]: entry }));
    try {
      await actions.stop(runId, entry.requestId);
      if (mounted.current) setStops((m) => ({ ...m, [runId]: { ...entry, status: 'accepted' } }));
    } catch (e) {
      if (mounted.current) setStops((m) => ({ ...m, [runId]: { ...entry, status: 'error', message: e instanceof Error ? e.message.slice(0, 200) : '未知错误' } }));
    }
  }, [actions, stops]);

  const reconnect = async () => {
    if (!actions.reconnect || reconnecting) return;
    setReconnecting(true);
    try { await actions.reconnect(); } catch { /* stale banner keeps the message */ }
    if (mounted.current) setReconnecting(false);
  };

  useEffect(() => {
    if (!openTask && !settingsOpen) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') { setOpenTask(null); setSettingsOpen(false); } };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [openTask, settingsOpen]);

  const runs = snapshot?.runs ?? [];
  const activeRuns = runs.filter((r) => isActiveRun(r) && inProject(taskOf(r)?.projectId));
  const counts = snapshot?.counts;
  const countText = (v: number | null | undefined) => (stale || v === null || v === undefined ? '未知' : String(v));

  const banner = stale ? (
    <div className="stale-banner" role="status">
      <span><b>已断连</b> · {stale}。运行数未知。{snapshot ? `下方为 ${formatTime(snapshot.observedAt)} 的最近快照，已过期，不代表当前状态。` : '尚未取得任何快照。'}</span>
      {actions.reconnect ? <button type="button" className="btn" onClick={reconnect} disabled={reconnecting}>{reconnecting ? '正在重连…' : '重新连接'}</button> : null}
    </div>
  ) : null;

  function stopControl(r: Run): ReactNode {
    const s = stops[r.id];
    if (r.state === 'stopped') return <span className="badge">已停止</span>;
    if (s?.status === 'accepted' || r.stopRequested) return <span className="badge amber" role="status">停止请求已接受 · 等待运行停止</span>;
    if (!['queued', 'pending', 'starting', 'running'].includes(r.state)) return null;
    return (
      <>
        <button type="button" className="btn" disabled={!writable || s?.status === 'pending'} title={whyDisabled || undefined} onClick={() => void requestStop(r.id)}>
          {s?.status === 'pending' ? '正在请求停止…' : s?.status === 'error' ? '重试停止' : '停止运行'}
        </button>
        {s?.status === 'error' ? <span className="local-note" role="alert">停止失败：{s.message}</span> : null}
        {!writable && whyDisabled ? <span className="k small">{whyDisabled}</span> : null}
      </>
    );
  }

  function agentRow(r: Run) {
    const t = taskOf(r);
    const ev = lastEvent(r);
    const a = agentLabel(r.agentId);
    const open = expanded === r.id;
    const dot = stale || r.state === 'unknown' ? 'stale' : r.state === 'running' ? 'running' : /block|fail/.test(r.state) ? 'blocked' : 'waiting';
    return (
      <div className={`agent-row${stale ? ' stale' : ''}`} key={r.id} data-testid="agent-row">
        <button type="button" className="row-btn" aria-expanded={open} aria-controls={`mk-more-${r.id}`} onClick={() => setExpanded(open ? null : r.id)}>
          <span className={`dot ${dot}`} title={stale ? '快照（已过期）' : runLabel(r.state)} />
          <span className="who">
            <span className="line1"><span className="role">{a.label || '—'} · {roleLabel(r.role)}</span><span className="task">{t?.title || '未知任务'}</span></span>
            <span className="sub">
              {r.executor || '执行器未记录'}{r.stage ? ` · 阶段 ${r.stage}` : ''}
              {ev ? <> · <span className="act">{ev.summary || ev.type}</span> · {formatTime(ev.observedAt)}</> : ' · 尚无事件'}
              {stale ? ' · 快照' : ''}
            </span>
          </span>
          <span className="fields">
            <span className="field"><span className="v">{modelLabel(r)}</span></span>
            <span className="field"><span className="v">{runLabel(r.state)}</span></span>
            <span className="field"><span className="k">开始</span><span className="v">{formatTime(r.startedAt)}</span></span>
          </span>
        </button>
        <div className="agent-more" id={`mk-more-${r.id}`} hidden={!open}>
          <div>
            <h4>最近事件{stale ? '（快照）' : ''}</h4>
            {r.events?.length ? (
              <ul className="events">{r.events.slice(-5).reverse().map((e, i) => <li key={i}><time>{formatTime(e.observedAt).split(' ')[1] ?? '—'}</time><span className="ek">{e.type}</span><span>{e.summary ?? ''}</span></li>)}</ul>
            ) : <p className="k small">尚无事件。</p>}
            {r.state === 'unknown' ? <p className="local-note mt">状态未知：调度器心跳过期或重启后未能核实，不会自动重放。</p> : null}
          </div>
          <div>
            <h4>运行</h4>
            <dl className="kv">
              {a.legacy ? <><dt>历史 ID</dt><dd><code>{a.legacy}</code></dd></> : null}
              <dt>运行 ID</dt><dd><code>{r.id}</code></dd>
              <dt>上下文</dt><dd><code>{short((r.contextRef ?? t?.contextRef)?.digest, 12) || '—'}</code></dd>
            </dl>
            <div className="inline-actions">
              {t ? <button type="button" className="btn" onClick={() => setOpenTask(t.id)}>打开任务</button> : null}
              {stopControl(r)}
            </div>
          </div>
        </div>
      </div>
    );
  }

  function AgentsView() {
    if (!snapshot) return <>{banner}<div className="list"><div className="empty">{stale ? '无法读取工作流状态。' : '正在读取工作流状态…'}</div></div></>;
    const ctl = { running: '运行中', idle: '空闲', unknown: '未知' }[snapshot.controller?.state ?? 'unknown'];
    const dl = snapshot.deliveries.filter((d) => inProject(idx.task.get(d.taskId)?.projectId)).slice(-5).reverse();
    return (
      <>
        {banner}
        <div className="host"><span className="badge">宿主</span><span><b>Codex · 协调者</b> — 本地调度器：{ctl}{snapshot.controller?.heartbeatAt ? `（心跳 ${formatTime(snapshot.controller.heartbeatAt)}）` : ''}</span></div>
        <div className="sec-h"><b>工作流运行</b><span>运行中 {countText(counts?.running)} · 排队 {countText(counts?.queued)} · 未知 {countText(counts?.unknown)}</span><span className="end">{stale ? '快照' : '更新于'} {formatTime(snapshot.observedAt)}</span></div>
        <div className="list">{activeRuns.length ? activeRuns.map(agentRow) : <div className="empty">当前没有工作流运行。</div>}</div>
        {independent.length ? (
          <>
            <div className="sec-h"><b>独立运行，尚未关联任务</b><span>{independent.length} 个</span></div>
            <div className="list independent">{independent.slice(0, 50).map((a, i) => (
              <div className="row" key={a.runId ?? i} data-testid="independent-row">
                <span className="badge">独立</span>
                <span className="who"><span className="name">{a.task || '未命名'}</span><span className="sub">{a.model || '模型未记录'}{a.role ? ` · ${roleLabel(a.role)}` : ''} · 开始 {formatTime(a.startedAt)}</span></span>
              </div>
            ))}</div>
          </>
        ) : null}
        <div className="sec-h"><b>最近交付</b><span>仅本地代码提交</span></div>
        <div className="list">{dl.length ? dl.map((d) => {
          const t = idx.task.get(d.taskId);
          return (
            <button type="button" className="row deliv-row" key={d.id} onClick={() => t && setOpenTask(t.id)}>
              <span className="who"><span className="name">{t?.title || '未知任务'}</span><span className="sub"><code>{short(d.candidateSha, 10)}</code>{d.knownGaps?.length ? ` · 已知缺口 ${d.knownGaps.length}` : ''}</span></span>
              <span className="badge res">{DELIVERY_LABEL[d.state] ?? d.state}</span>
            </button>
          );
        }) : <div className="empty">暂无交付。</div>}</div>
      </>
    );
  }

  function TasksView() {
    if (!snapshot) return <div className="list"><div className="empty">{stale ? '无法读取任务。' : '正在读取任务…'}</div></div>;
    const tasks = snapshot.tasks.filter((t) => inProject(t.projectId));
    return (
      <>{banner}<div className="list">{tasks.length ? tasks.map((t) => {
        const cat = taskCategory(t);
        const last = runs.filter((r) => r.taskId === t.id).slice(-1)[0];
        return (
          <button type="button" className="row task-row" key={t.id} onClick={() => setOpenTask(t.id)}>
            <span className="who"><span className="name">{t.title || '未命名任务'}</span><span className="sub"><code>{short(t.id)}</code> · {idx.proj.get(t.projectId)?.name ?? t.projectId} · 更新 {formatTime(t.updatedAt)}</span></span>
            <span className="meta"><span className={`badge${cat === 'attention' ? ' red' : ''}`}>{taskLabel(t.state)}</span>{last ? <span className="badge">{roleLabel(last.role)} · {runLabel(last.state)}</span> : null}</span>
          </button>
        );
      }) : <div className="empty">还没有工作流任务。</div>}</div></>
    );
  }

  function UsageView() {
    if (!snapshot) return <div className="list"><div className="empty">{stale ? '无法读取用量。' : '正在读取用量…'}</div></div>;
    const started = runs.filter((r) => r.startedAt && inProject(taskOf(r)?.projectId));
    if (!started.length) return <div className="list"><div className="empty">暂无已开始的运行记录。</div></div>;
    const s = summarizeUsage(started, now());
    const cell = (k: 'input' | 'output' | 'cacheRead' | 'cacheWrite' | 'total') =>
      s.missing[k] === s.runs ? '未知' : `${s.missing[k] ? '≥ ' : ''}${num(s.known[k])}`;
    return (
      <>
        {banner}
        <div className="stats" data-testid="usage-stats">
          <div className="kpi"><b>{cell('input')}</b><span>输入</span></div>
          <div className="kpi"><b>{cell('output')}</b><span>输出</span></div>
          <div className="kpi"><b>{cell('cacheRead')} / {cell('cacheWrite')}</b><span>缓存 读 / 写</span></div>
          <div className="kpi"><b>{s.costRuns ? `≥ US$${s.costUsd.toFixed(4)}` : '未知'}</b><span>费用</span><small>{s.costRuns}/{s.runs} 次返回费用</small></div>
          <div className="kpi"><b>{formatDuration(s.wallSeconds)}</b><span>墙钟时间</span><small>Agent 耗时合计 {formatDuration(s.agentSeconds)}</small></div>
        </div>
        <p className="k small mt">仅汇总已上报的类别；未返回的计数显示为未知，不按 0 计。墙钟时间为首次开始到最后结束，Agent 耗时为各运行时长之和。</p>
        <div className="sec-h"><b>运行明细</b><span>{started.length} 次运行</span></div>
        <div className="list">{started.map((r) => {
          const t = runTokens(r);
          const v = (x: number | null) => (x === null ? '未知' : num(x));
          const c = r.usage?.estimatedCostUsd;
          return (
            <div className="row stat-row" key={r.id}>
              <span className="who"><span className="name">{agentLabel(r.agentId).label || '—'} · {roleLabel(r.role)} · {modelLabel(r)}</span>
                <span className="sub">入 {v(t.input)} · 出 {v(t.output)} · 缓存读 {v(t.cacheRead)} · 缓存写 {v(t.cacheWrite)} · 合计 {v(t.total)}</span></span>
              <span className="num"><b>{typeof c === 'number' ? `US$${c.toFixed(4)}` : '未知'}</b><small>费用</small></span>
            </div>
          );
        })}</div>
      </>
    );
  }

  function TaskDrawer({ task }: { task: Task }) {
    const tRuns = runs.filter((r) => r.taskId === task.id);
    const deliveries = snapshot?.deliveries.filter((d) => d.taskId === task.id) ?? [];
    const reviews = snapshot?.reviews.filter((d) => d.taskId === task.id) ?? [];
    const checksOf = (c: unknown) => (Array.isArray(c) ? c : c && typeof c === 'object' ? [c] : []).filter((x): x is CheckEntry => !!x && typeof x === 'object').map(checkView);
    return (
      <div className="scrim" onClick={(e) => { if (e.target === e.currentTarget) setOpenTask(null); }}>
        <aside className="drawer" role="dialog" aria-modal="true" aria-labelledby="mk-drawer-title" tabIndex={-1} ref={(el) => el?.focus()}>
          <div className="dlg-h"><div><h2 id="mk-drawer-title">{task.title || '未命名任务'}</h2><div className="sub"><code>{task.id}</code><span>{taskLabel(task.state)}</span><IssueLink task={task} /></div></div>
            <button type="button" className="icon" aria-label="关闭" onClick={() => setOpenTask(null)}><Close /></button></div>
          <div className="dlg-b">
            <div className="card"><h3>概览</h3><dl className="concl">
              <dt>目标</dt><dd>{task.goal || '—'}</dd>
              <dt>基线 SHA</dt><dd><code>{task.baselineSha ?? '—'}</code></dd>
              <dt>候选 SHA</dt><dd><code>{task.candidateSha ?? '—'}</code></dd>
              <dt>上下文</dt><dd><code>{task.contextRef ? `v${task.contextRef.version} · ${task.contextRef.digest}` : '—'}</code></dd>
            </dl><p className="boundary">交付仅表示本地代码提交（候选 SHA）；发布、独立 QA 与客户验收是独立环节，此处不代表已完成。</p></div>
            <div className="card"><h3>角色流程</h3><ol className="phases" style={{ gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' }}>
              {ROLES.map((role) => {
                const rs = tRuns.filter((r) => r.role === role);
                const last = rs[rs.length - 1];
                return <li key={role} className={last ? (isActiveRun(last) ? 'cur' : 'done') : ''}>{ROLE_LABEL[role]}<br />{last ? runLabel(last.state) : '未开始'}</li>;
              })}
            </ol></div>
            {deliveries.map((d) => (
              <div className="card" key={d.id}><h3>{DELIVERY_LABEL[d.state] ?? d.state}</h3><dl className="concl">
                <dt>候选 SHA</dt><dd><code>{d.candidateSha}</code></dd>
                <dt>上下文</dt><dd><code>{d.contextRef?.digest ?? '—'}</code></dd>
                <dt>检查</dt><dd>{checksOf(d.checks).length ? checksOf(d.checks).map((c, i) => <div key={i} data-check={c.kind}>{c.kind === 'harness' ? <>{c.label} → {c.outcome}</> : <>{c.kind === 'reported' ? 'Agent 报告: ' : ''}<code>{c.label}</code> → {c.outcome}</>}</div>) : '未记录'}</dd>
                <dt>已知缺口</dt><dd>{d.knownGaps?.length ? <ul>{d.knownGaps.map((g, i) => <li key={i}>{g}</li>)}</ul> : '无'}</dd>
              </dl></div>
            ))}
            {reviews.map((r) => (
              <div className="card" key={r.id}><h3>审查结论</h3><dl className="concl">
                <dt>结论</dt><dd>{r.verdict}</dd><dt>候选 SHA</dt><dd><code>{r.candidateSha ?? '—'}</code></dd><dt>上下文摘要</dt><dd><code>{r.contextDigest ?? '—'}</code></dd>
              </dl></div>
            ))}
            <div className="card"><h3>历史</h3>{tRuns.length ? <div className="run-table">{tRuns.map((r) => (
              <div className="run" key={r.id}><span className="rt">{roleLabel(r.role)} · {agentLabel(r.agentId).label || '—'}</span><span className="rr">{runLabel(r.state)}</span>
                <span className="rm"><span className="rid">{short(r.id)}</span> · {modelLabel(r)} · {formatTime(r.startedAt)} → {r.endedAt ? formatTime(r.endedAt) : '—'}</span></div>
            ))}</div> : <p className="k">尚无运行。</p>}</div>
          </div>
        </aside>
      </div>
    );
  }

  const task = openTask ? idx.task.get(openTask) : undefined;
  return (
    <div id="meerkat-ui" data-theme={theme}>
      <header className="top">
        <div className="brand"><span className="logo"><Logo /></span><span role="heading" aria-level={1}>Meerkat</span></div>
        <nav className="seg" aria-label="视图">
          {(['agents', 'tasks', 'usage'] as const).map((v) => (
            <button type="button" key={v} className={view === v ? 'on' : ''} aria-current={view === v ? 'page' : undefined} onClick={() => setView(v)}>{v[0]!.toUpperCase() + v.slice(1)}</button>
          ))}
        </nav>
        <div className="actions">
          <button type="button" className="icon" aria-label={theme === 'dark' ? '切换到浅色主题' : '切换到深色主题'} onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}>
            {theme === 'dark'
              ? <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><circle cx="8" cy="8" r="3" /><path d="M8 1.5v1.6M8 12.9v1.6M1.5 8h1.6M12.9 8h1.6" /></svg>
              : <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true"><path d="M13.5 9.6A5.6 5.6 0 0 1 6.4 2.5a5.6 5.6 0 1 0 7.1 7.1Z" /></svg>}
          </button>
          <button type="button" className="icon" aria-label="设置" aria-haspopup="dialog" onClick={() => setSettingsOpen(true)}>
            <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true"><circle cx="8" cy="8" r="2.2" /><path d="M8 1.6v1.7M8 12.7v1.7M14.4 8h-1.7M3.3 8H1.6M12.5 3.5l-1.2 1.2M4.7 11.3l-1.2 1.2M12.5 12.5l-1.2-1.2M4.7 4.7 3.5 3.5" /></svg>
          </button>
        </div>
      </header>
      <div className="subbar">
        <label className="select-pill"><span className="sr-only">项目</span>
          <select aria-label="项目筛选" value={project} onChange={(e) => setProject(e.target.value)}>
            <option value="all">全部项目</option>
            {snapshot?.projects.map((p) => <option key={p.id} value={p.id}>{p.name || p.id}</option>)}
          </select>
        </label>
        <span className="state-sum">运行中 <b>{countText(counts?.running)}</b> · 独立 <b>{stale ? '未知' : independent.length}</b></span>
        <span className={`notice${stale ? ' stale' : ''}`} role="status" aria-live="polite">{stale ? '已断连 · 快照已过期' : connected ? '已连接' : '正在连接…'}</span>
      </div>
      <main className="view">
        <section aria-label={view}>{view === 'agents' ? AgentsView() : view === 'tasks' ? TasksView() : UsageView()}</section>
      </main>
      {task ? TaskDrawer({ task }) : null}
      {settingsOpen ? <SettingsSheet snapshot={snapshot} project={project} disabledReason={writable ? '' : whyDisabled || '尚未连接。'} onSave={actions.settings} onClose={() => setSettingsOpen(false)} /> : null}
    </div>
  );
}

function SettingsSheet({ snapshot, project, disabledReason, onSave, onClose }: {
  snapshot: Snapshot | null; project: string; disabledReason: string; onSave: (i: SettingsInput) => Promise<unknown>; onClose: () => void;
}) {
  const s = snapshot?.settings ?? null;
  const [conc, setConc] = useState(s?.maxConcurrency ?? 2);
  const [rounds, setRounds] = useState(s?.maxFixRounds ?? 2);
  const [profiles, setProfiles] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState('');
  const [busy, setBusy] = useState(false);
  const disabled = !!disabledReason || !s || busy;
  const save = async () => {
    const input: SettingsInput = { maxConcurrency: conc, maxFixRounds: rounds };
    const map = Object.fromEntries(Object.entries(profiles).filter(([, v]) => v));
    if (project !== 'all' && Object.keys(map).length) input.defaultProfiles = { [project]: map };
    setBusy(true);
    try { await onSave(input); setMsg('已保存：下一次新运行生效，当前运行不受影响。'); } catch (e) { setMsg(`保存失败：${e instanceof Error ? e.message.slice(0, 200) : '未知错误'}`); }
    setBusy(false);
  };
  return (
    <div className="scrim center" onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <div className="sheet" role="dialog" aria-modal="true" aria-labelledby="mk-settings-title" tabIndex={-1}>
        <div className="dlg-h"><div><h2 id="mk-settings-title">设置</h2><div className="sub">只影响之后新开始的运行；正在运行的模型与配置不会改变。</div></div>
          <button type="button" className="icon" aria-label="关闭设置" onClick={onClose} autoFocus><Close /></button></div>
        <div className="dlg-b">
          {disabledReason || !s ? <p className="local-note">{disabledReason || '尚无工作流设置记录，无法修改。'}</p> : null}
          <div className="sec-h"><b>调度</b></div>
          <div className="list">
            <div className="set-row"><label className="lbl" htmlFor="mk-set-conc">本地并发<small>同时运行的 Agent 实例数（1–4）</small></label>
              <span className="ctl"><select id="mk-set-conc" value={conc} disabled={disabled} onChange={(e) => setConc(Number(e.target.value))}>{[1, 2, 3, 4].map((v) => <option key={v} value={v}>{v}</option>)}</select></span></div>
            <div className="set-row"><label className="lbl" htmlFor="mk-set-rounds">最大修复轮数<small>达到后停止并交给 Codex（0–2）</small></label>
              <span className="ctl"><select id="mk-set-rounds" value={rounds} disabled={disabled} onChange={(e) => setRounds(Number(e.target.value))}>{[0, 1, 2].map((v) => <option key={v} value={v}>{v}</option>)}</select></span></div>
          </div>
          <div className="sec-h"><b>默认角色配置</b></div>
          <div className="list">{project === 'all'
            ? <div className="set-row"><span className="lbl">选择具体项目后可设置默认配置<small>只能从该项目已注册的配置 ID 中选择</small></span></div>
            : ROLES.map((role) => {
              const opts = (snapshot?.profiles ?? []).filter((p) => p.projectId === project && p.role === role);
              const cur = s?.defaultProfiles?.[project]?.[role] ?? '';
              return (
                <div className="set-row" key={role}><span className="lbl">{ROLE_LABEL[role]}</span><span className="ctl">{opts.length
                  ? <select aria-label={`${ROLE_LABEL[role]} 默认配置`} disabled={disabled} value={profiles[role] ?? cur} onChange={(e) => setProfiles({ ...profiles, [role]: e.target.value })}>
                    <option value="">（保持当前）</option>{opts.map((p) => <option key={p.id} value={p.id}>{p.id} · {[p.provider, p.model].filter(Boolean).join('/')}</option>)}</select>
                  : <span className="k small">该项目没有已注册的配置</span>}</span></div>
              );
            })}</div>
          <div className="inline-actions mt"><button type="button" className="btn primary" disabled={disabled} onClick={() => void save()}>{busy ? '正在保存…' : '保存（下次运行生效）'}</button></div>
          <p className={`applied${msg.startsWith('保存失败') ? ' err' : ''}`} role="status" aria-live="polite">{msg}</p>
        </div>
      </div>
    </div>
  );
}

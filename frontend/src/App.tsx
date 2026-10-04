import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import type { LegacyActive, Run, SettingsInput, Snapshot, Task } from './generated/workflow';
import {
  DELIVERY_LABEL, ROLES, ROLE_LABEL, agentLabel, checkView, type CheckEntry, dedupLegacy, formatDuration, formatTime, isActiveRun, lastEvent,
  eventLabel, isWrappingUp, modelLabel, num, roleLabel, runLabel, runTokens, safeHttpsUrl, short, summarizeUsage, taskCategory, taskLabel,
} from './model';
import { newRequestId, type InterventionActions } from './transport';
import { Intervention } from './Intervention';

/** Host actions. In readonly hosts stop/settings are disabled; reconnect may still be offered. */
export interface AppActions {
  readonly: boolean;
  readonlyNote?: string;
  intervention?: InterventionActions;
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

function Disclosure({ title, children }: { title: string; children: ReactNode }) {
  return <details className="disclosure"><summary>{title}</summary><div className="disclosure-body">{children}</div></details>;
}

const briefModel = (r: Run) => r.modelSnapshot?.model?.split('/').filter(Boolean).at(-1) || '模型未记录';

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
  // Match the service: running/unknown count Runs; queued counts Tasks.
  // An absent or unknown server count remains unknown, even in a filtered view.
  const counts = project === 'all' ? snapshot?.counts : snapshot?.counts && {
    running: snapshot.counts.running == null ? null : runs.filter(r => r.state === 'running' && inProject(taskOf(r)?.projectId)).length,
    queued: snapshot.counts.queued == null ? null : snapshot.tasks.filter(t => t.state === 'queued' && inProject(t.projectId)).length,
    unknown: snapshot.counts.unknown == null ? null : runs.filter(r => r.state === 'unknown' && inProject(taskOf(r)?.projectId)).length,
  };
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
    const ev = [...(r.events ?? [])].reverse().find(e => e.type !== 'state') ?? lastEvent(r);
    const a = agentLabel(r.agentId);
    const open = expanded === r.id;
    const dot = stale || r.state === 'unknown' ? 'stale' : r.state === 'running' ? 'running' : /block|fail/.test(r.state) ? 'blocked' : 'waiting';
    return (
      <div className={`agent-row${stale ? ' stale' : ''}`} key={r.id} data-testid="agent-row">
        <button type="button" className="row-btn" aria-expanded={open} aria-controls={`mk-more-${r.id}`} onClick={() => setExpanded(open ? null : r.id)}>
          <span className={`dot ${dot}`} title={stale ? '快照（已过期）' : runLabel(r.state)} />
          <span className="who">
            <span className="task">{t?.title || '未知任务'}</span>
            <span className="identity">{a.label || '—'} · {roleLabel(r.role)} · {briefModel(r)}</span>
            <span className="sub">
              {ev ? <><span className="act">{eventLabel(ev)}</span> · {formatTime(ev.observedAt)}</> : '尚无事件'}
              {stale ? ' · 快照' : ''}
            </span>
          </span>
          <span className={`run-state${/unknown|block|fail/.test(r.state) ? ' attention' : ''}`}>{isWrappingUp(r) ? '收尾中' : runLabel(r.state)}</span>
        </button>
        <div id={`mk-more-${r.id}`} hidden={!open}>
          <div className="agent-tools">
            {t ? <button type="button" className="btn" onClick={() => setOpenTask(t.id)}>打开任务</button> : null}
            {!actions.intervention ? stopControl(r) : null}
          </div>
          {actions.intervention && open ? <Intervention run={r} sessionId={t?.sessions?.find(s => s.activeRunId === r.id && s.state === 'running')?.id} actions={actions.intervention} disabled={!!stale || !connected || r.state === 'unknown'} receipts={t?.controlReceipts ?? []} /> : null}
          <details className="run-detail"><summary>运行详情与最近事件</summary><div className="agent-more">
          <div>
            <h4>最近事件{stale ? '（快照）' : ''}</h4>
            {r.events?.length ? (
              <ul className="events">{r.events.slice(-5).reverse().map((e, i) => <li key={i}><time>{formatTime(e.observedAt).split(' ')[1] ?? '—'}</time><span className="ek">{e.type}</span><span>{eventLabel(e)}</span></li>)}</ul>
            ) : <p className="k small">尚无事件。</p>}
            {r.state === 'unknown' ? <p className="local-note mt">状态未知：调度器心跳过期或重启后未能核实，不会自动重放。</p> : null}
          </div>
          <div>
            <h4>运行</h4>
            <dl className="kv">
              <dt>模型</dt><dd>{modelLabel(r)}</dd>
              <dt>执行器</dt><dd>{r.executor || '未记录'}{r.stage ? ` · 阶段 ${r.stage}` : ''}</dd>
              <dt>开始</dt><dd>{formatTime(r.startedAt)}</dd>
              {a.legacy ? <><dt>历史 ID</dt><dd><code>{a.legacy}</code></dd></> : null}
              <dt>运行 ID</dt><dd><code>{r.id}</code></dd>
              <dt>上下文</dt><dd><code>{short((r.contextRef ?? t?.contextRef)?.digest, 12) || '—'}</code></dd>
            </dl>
          </div>
          </div></details>
        </div>
      </div>
    );
  }

  function AgentsView() {
    if (!snapshot) return <>{banner}<div className="list"><div className="empty">{stale ? '无法读取工作流状态。' : '正在读取工作流状态…'}</div></div></>;
    const dl = snapshot.deliveries.filter((d) => inProject(idx.task.get(d.taskId)?.projectId)).slice(-5).reverse();
    return (
      <>
        {banner}
        <div className="sec-h"><b>Agents</b><span>当前任务与最近动作</span></div>
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
            <span className="who"><span className="name">{t.title || '未命名任务'}</span><span className="sub">{idx.proj.get(t.projectId)?.name ?? t.projectId}{last ? ` · ${roleLabel(last.role)} · ${runLabel(last.state)}` : ''} · 更新 {formatTime(t.updatedAt)}</span></span>
            <span className={`run-state${cat === 'attention' ? ' attention' : ''}`}>{taskLabel(t.state)}</span>
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
          <div className="kpi"><b>{cell('total')}</b><span>总 token</span><small>{s.runs - s.missing.total}/{s.runs} 次返回总量</small></div>
          <div className="kpi"><b>{s.costRuns ? `${s.costRuns < s.runs ? '≥ ' : ''}US$${s.costUsd.toFixed(4)}` : '未知'}</b><span>费用</span><small>{s.costRuns}/{s.runs} 次返回费用</small></div>
          <div className="kpi"><b>{s.agentTimeUnknown === s.runs ? '未知' : `${s.agentTimeUnknown ? '≥ ' : ''}${formatDuration(s.agentSeconds)}`}</b><span>Agent 耗时</span><small>各运行耗时合计{s.agentTimeUnknown ? ` · ${s.agentTimeUnknown} 次未知` : ''}</small></div>
        </div>
        <Disclosure title="分类用量与统计口径">
          <dl className="concl"><dt>输入</dt><dd>{cell('input')}</dd><dt>输出</dt><dd>{cell('output')}</dd>
            <dt>缓存读</dt><dd>{cell('cacheRead')}</dd><dt>缓存写</dt><dd>{cell('cacheWrite')}</dd>
            <dt>墙钟时间</dt><dd>{formatDuration(s.wallSeconds)}</dd></dl>
          <p className="boundary">仅汇总已上报的类别；≥ 表示已知下限，未知不按 0 计。墙钟时间为首次开始到最后结束，Agent 耗时为各运行时长之和。</p>
        </Disclosure>
        <div className="sec-h"><b>运行明细</b><span>{started.length} 次运行</span></div>
        <div className="list">{started.map((r) => {
          const t = runTokens(r);
          const v = (x: number | null) => (x === null ? '未知' : num(x));
          const c = r.usage?.estimatedCostUsd;
          return (
            <details className="usage-run" key={r.id}>
              <summary className="row stat-row">
                <span className="who"><span className="name">{taskOf(r)?.title || '未知任务'}</span>
                  <span className="sub">{agentLabel(r.agentId).label || '—'} · {roleLabel(r.role)} · {briefModel(r)}</span></span>
                <span className="num"><b>{v(t.total)}</b><small>token</small></span>
                <span className="num"><b>{typeof c === 'number' ? `US$${c.toFixed(4)}` : '未知'}</b><small>费用</small></span>
              </summary>
              <dl className="kv usage-breakdown"><dt>输入 / 输出</dt><dd>{v(t.input)} / {v(t.output)}</dd><dt>缓存读 / 写</dt><dd>{v(t.cacheRead)} / {v(t.cacheWrite)}</dd>
                <dt>模型</dt><dd>{modelLabel(r)}</dd><dt>运行 ID</dt><dd><code>{r.id}</code></dd></dl>
            </details>
          );
        })}</div>
      </>
    );
  }

  function TaskDrawer({ task }: { task: Task }) {
    const tRuns = runs.filter((r) => r.taskId === task.id);
    const deliveries = snapshot?.deliveries.filter((d) => d.taskId === task.id) ?? [];
    const reviews = snapshot?.reviews.filter((d) => d.taskId === task.id) ?? [];
    const currentDelivery = [...deliveries].reverse().find(d => d.candidateSha === task.candidateSha);
    const currentReviews = currentDelivery && task.contextRef ? reviews.filter(r => r.candidateSha === task.candidateSha && r.contextDigest === task.contextRef?.digest) : [];
    const olderDeliveries = deliveries.filter(d => d.id !== currentDelivery?.id);
    const olderReviews = reviews.filter(r => !currentReviews.includes(r));
    const checksOf = (c: unknown) => (Array.isArray(c) ? c : c && typeof c === 'object' ? [c] : []).filter((x): x is CheckEntry => !!x && typeof x === 'object').map(checkView);
    const currentChecks = checksOf(currentDelivery?.checks);
    const harnessChecks = currentChecks.filter(c => c.kind === 'harness');
    const checkSummary = [
      harnessChecks.length ? `本地核对 ${harnessChecks.filter(c => c.outcome === '通过').length}/${harnessChecks.length} 项通过` : '',
      currentChecks.some(c => c.kind === 'reported') ? `Agent 报告 ${currentChecks.filter(c => c.kind === 'reported').length} 项` : '',
      currentChecks.some(c => c.kind === 'legacy') ? `历史检查 ${currentChecks.filter(c => c.kind === 'legacy').length} 项` : '',
    ].filter(Boolean).join(' · ') || '未记录';
    return (
      <div className="scrim" onClick={(e) => { if (e.target === e.currentTarget) setOpenTask(null); }}>
        <aside className="drawer" role="dialog" aria-modal="true" aria-labelledby="mk-drawer-title" tabIndex={-1} ref={(el) => el?.focus()}>
          <div className="dlg-h"><div><h2 id="mk-drawer-title">{task.title || '未命名任务'}</h2><div className="sub"><span>{taskLabel(task.state)}</span><IssueLink task={task} /></div></div>
            <button type="button" className="icon" aria-label="关闭" onClick={() => setOpenTask(null)}><Close /></button></div>
          <div className="dlg-b">
            <section className="detail-section"><h3>当前进度</h3><dl className="concl">
              {task.progress ? <>
                <dt>执行</dt><dd>{({ready:'待开始',queued:'排队中',running:'运行中',wrapping_up:'收尾中',paused:'已暂停',ended:'已结束',failed:'失败',blocked:'被阻塞',unknown:'未知'})[task.progress.execution]}</dd>
                <dt>阶段</dt><dd>{({none:'尚未开始',development:'开发',review:'审查',fix:'修复',polish:'精修',recheck:'复审',complete:'流程完成'})[task.progress.phase]}</dd>
                <dt>交付</dt><dd>{({none:'尚无候选',candidate:'候选待检查',reviewed:'当前候选已通过审查',local_delivery:'最终本地交付'})[task.progress.delivery]}</dd>
              </> : null}
              {task.state === 'queued' ? <><dt>等待原因</dt><dd>{({project_concurrency:'项目并发已满',provider_concurrency:'模型服务并发已满',concurrency:'本地并发已满',worktree:'工作目录正在使用',dependencies:'等待依赖任务',dispatch_queue:'等待调度'} as Record<string,string>)[task.stateReason ?? ''] ?? '等待调度核对'}</dd></> : null}
              {task.stateReason && task.state !== 'queued' ? <><dt>当前限制</dt><dd>{({request_budget_unverifiable:'请求用量无法核实',dispatch_unknown:'执行结果无法核实',budget_exhausted:'预算已耗尽'} as Record<string,string>)[task.stateReason] ?? task.stateReason}</dd></> : null}
              <dt>目标</dt><dd>{task.goal || '—'}</dd>
              <dt>候选 SHA</dt><dd><code>{task.candidateSha ?? '—'}</code></dd>
            </dl><p className="boundary">仅本地代码交付；发布、独立 QA 与客户验收需另行确认。</p></section>
            <section className="detail-section"><h3>角色流程</h3><ol className="phases" style={{ gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' }}>
              {ROLES.map((role) => {
                const rs = tRuns.filter((r) => r.role === role);
                const last = rs[rs.length - 1];
                return <li key={role} className={last ? (isActiveRun(last) ? 'cur' : 'done') : ''}>{ROLE_LABEL[role]}<br />{last ? runLabel(last.state) : '未开始'}</li>;
              })}
            </ol></section>
            {currentDelivery ? <section className="detail-section"><h3>{DELIVERY_LABEL[currentDelivery.state] ?? currentDelivery.state}</h3><dl className="concl">
              <dt>检查</dt><dd>{checkSummary}{harnessChecks.filter(c => c.outcome !== '通过').map((c, i) => <p className="detail-alert" key={i}>{c.label} → {c.outcome}</p>)}</dd>
              <dt>审查</dt><dd>{currentReviews.length ? currentReviews.map(r => <div key={r.id}>{r.verdict}</div>) : '当前候选尚无匹配的审查记录'}</dd>
              <dt>已知缺口</dt><dd>{currentDelivery.knownGaps?.length ? <ul>{currentDelivery.knownGaps.map((g, i) => <li key={i}>{g}</li>)}</ul> : '无'}</dd>
            </dl>{currentChecks.length ? <Disclosure title="检查明细">{currentChecks.map((c, i) => <div key={i} data-check={c.kind}>{c.kind === 'harness' ? <>{c.label} → {c.outcome}</> : <>{c.kind === 'reported' ? 'Agent 报告: ' : ''}<code>{c.label}</code> → {c.outcome}</>}</div>)}</Disclosure> : null}</section> : null}
            {task.budgetEvidence?.warning || task.budgetEvidence?.overrun || task.budgetEvidence?.unknownRequests ? <p className="detail-alert" role="status">{[
              task.budgetEvidence.warning ? '已达到 token 提示阈值' : '', task.budgetEvidence.overrun ? '已确认超额' : '',
              task.budgetEvidence.unknownRequests ? `请求结果未知 ${task.budgetEvidence.unknownRequests} 次；预留占用 ${num(task.budgetEvidence.reservedTokens)} token` : '',
            ].filter(Boolean).join(' · ')}</p> : null}
            <Disclosure title="版本与上下文"><dl className="concl">
              <dt>任务 ID</dt><dd><code>{task.id}</code></dd><dt>基线 SHA</dt><dd><code>{task.baselineSha ?? '—'}</code></dd>
              <dt>上下文</dt><dd><code>{task.contextRef ? `v${task.contextRef.version} · ${task.contextRef.digest}` : '—'}</code></dd>
              {currentDelivery ? <><dt>交付上下文</dt><dd><code>{currentDelivery.contextRef?.digest ?? '—'}</code></dd></> : null}
              {currentReviews.map(r => <div className="review-version" key={r.id}><dt>审查版本</dt><dd><code>{r.candidateSha ?? '—'}</code></dd><dt>审查上下文</dt><dd><code>{r.contextDigest ?? '—'}</code></dd></div>)}
            </dl></Disclosure>
            {task.budget ? <Disclosure title="任务预算与账本"><dl className="concl">
              <dt>{task.budget.mode === 'monitor' ? 'token 提示阈值' : '原始预算'}</dt><dd>{num(task.budget.maxTokens)} token · {formatDuration(task.budget.maxWallSeconds)} · 最多 {task.budget.maxFixRounds} 轮修复{task.budget.mode === 'monitor' ? '；任务累计 token 仅监测，不因达到阈值停止' : ''}</dd>
              {task.budgetAuthorization ? <>
                <dt>当前已授权</dt><dd>{task.budget.mode === 'monitor' ? '无任务 token 硬上限 · ' : `${num(task.budgetAuthorization.authorizedTokens)} token · `}{formatDuration(task.budgetAuthorization.authorizedWallSeconds)}</dd>
                <dt>累计追加</dt><dd>{num(task.budgetAuthorization.addedTokens)} token · {formatDuration(task.budgetAuthorization.addedWallSeconds)} · 预算修订 {task.budgetAuthorization.revision}</dd>
              </> : null}
              {task.budgetEvidence ? <>
                <dt>请求账本</dt><dd>{num(task.budgetEvidence.confirmedTokens)} 已结算 · {num(task.budgetEvidence.reservedTokens)} 预留占用</dd>
                <dt>{task.budget.mode === 'monitor' ? '监测状态' : '可申请额度'}</dt><dd>{task.budget.mode === 'monitor' ? (task.budgetEvidence.warning ? '已达到 token 提示阈值' : '仅监测；每次运行的配置上限仍生效') : `${task.budgetEvidence.availableTokens == null ? '未知' : num(task.budgetEvidence.availableTokens)}（预留口径）`}</dd>
                <dt>请求状态</dt><dd>{task.budgetEvidence.requests} 次 · 在途 {task.budgetEvidence.pendingRequests} · 结果未知 {task.budgetEvidence.unknownRequests}{task.budgetEvidence.overrun ? ' · 已确认超额' : ''}</dd>
              </> : <><dt>请求账本</dt><dd>此任务尚无逐次请求记录</dd></>}
              {task.budget.stageReserves ? <><dt>阶段预留</dt><dd>每次审查 {num(task.budget.stageReserves.reviewTokens)} · 每轮修复 {num(task.budget.stageReserves.fixTokens)} · 精修 {num(task.budget.stageReserves.polishTokens)}</dd>
                <dt>提前收尾</dt><dd>{[
                  task.budget.stageReserves.wrapUpTokens > 0 ? `剩余 ${num(task.budget.stageReserves.wrapUpTokens)} token` : '',
                  task.budget.stageReserves.wrapUpSeconds > 0 ? `剩余 ${formatDuration(task.budget.stageReserves.wrapUpSeconds)}` : '',
                ].filter(Boolean).join(' 或 ') || '未启用'}；原截止时间不变</dd></> : null}
            </dl>{task.budgetAuthorization?.decisions.map(d=><div className="run" key={d.requestId}>
              <span className="rt">修订 {d.revision} · +{num(d.addTokens)} token · +{formatDuration(d.addWallSeconds)}</span>
              <span className="rm">{d.reason} · {formatTime(d.createdAt)} · <code>{short(d.requestId)}</code></span>
            </div>)}{task.budgetAuthorization ? <p className="k small">追加额度保留已用 token 和耗时；任务仍需显式续跑。此面板仅展示预算决策。</p> : null}</Disclosure> : null}
            {task.state === 'unknown' || task.recoveryEvidence?.some(r => r.state !== 'settled') ? <section className="detail-section"><h3>中断恢复</h3>
              {task.recoveryEvidence?.filter(r => r.state !== 'settled').map(r => <div className="run" key={r.runId}>
                <span className="rt">{roleLabel(r.role)} · {r.state === 'pending' ? '完成证据已保存，待核对' : '已恢复已完成步骤'}</span>
                <span className="rm">Run <code>{short(r.runId)}</code> · SHA <code>{short(r.candidateSha)}</code> · {formatTime(r.recoveredAt ?? r.recordedAt)}</span>
              </div>)}
              <p className="k small">{task.recoveryEvidence?.some(r => r.state === 'pending') ? '需核对进程、会话、代码和用量后显式恢复；保存证据不会自动续跑。' : task.state === 'unknown' ? '缺少可恢复的完成证据。保留未知状态和已有修改，由协调者继续核对。' : '恢复保留原来的用量和交付版本；后续工作单独续跑。'}</p>
              <p className="k small">此面板只显示恢复记录。</p>
            </section> : null}
            {task.controlReceipts?.some(c => c.state === 'unknown' || c.runState === 'unknown') ? <p className="detail-alert" role="status">控制回执或运行结果未知，需核对原请求。</p> : null}
            {task.controlReceipts?.length ? <Disclosure title="控制回执"><div className="run-table">{task.controlReceipts.map(c => (
              <div className="run" key={c.requestId}>
                <span className="rt">{c.kind === 'wrap_up' ? '收尾' : c.kind === 'instruction' ? '指令' : '停止'} · {{accepted:'已保存',sending:'发送中',acknowledged:'执行器已接收',rejected:'已拒绝',unknown:'结果未知',processed:'已处理'}[c.state]}</span>
                <span className="rm">请求 <code>{short(c.requestId)}</code> · Run <code>{short(c.runId)}</code>{c.sessionId ? <> · Session <code>{short(c.sessionId)}</code></> : null} · {formatTime(c.updatedAt)}</span>
                <span className="rm">{c.disposition ? `协议回执：${c.disposition === 'queued' ? '已入队' : '已处理'} · ` : ''}实际运行：{runLabel(c.runState)}{c.outcome ? ` · 结束结果：${runLabel(c.outcome)}` : ' · 结束结果尚未确认'}</span>
                {c.reason ? <span className="rm">{{run_ended_before_send:'运行已结束，指令未发送',run_interrupted:'运行中断',contract_changed:'冻结任务或配置已变更',executor_refused:'执行器拒绝指令',wrap_up_already_requested:'本轮已请求收尾，未重复发送',protocol_reply_unknown:'协议回执未确认',controller_interrupted:'服务中断，保留未知且不重发',unsupported_control:'执行器不支持此指令'}[c.reason]}</span> : null}
              </div>
            ))}</div><p className="k small">已保存表示指令已记录；执行器已接收表示入队或处理。任务交付和进程退出需独立核实。面板只显示回执。</p></Disclosure> : null}
            {task.sessions?.some(s => s.state === 'unknown') ? <p className="detail-alert" role="status">执行会话身份未知。</p> : null}
            {task.sessions?.length ? <Disclosure title="执行会话"><div className="run-table">{task.sessions.map((s) => (
              <div className="run" key={s.id}><span className="rt">{roleLabel(s.role)} · {s.executor}</span><span className="rr">{s.state === 'idle' ? '已核实空闲' : s.state === 'running' ? '执行中' : '身份未知'}</span>
                <span className="rm"><code>{short(s.id)}</code> · HEAD <code>{short(s.lastSha)}</code>{s.activeRunId ? <> · Run <code>{short(s.activeRunId)}</code></> : null}</span></div>
            ))}</div><p className="k small">会话空闲不代表任务已完成。</p></Disclosure> : null}
            {task.checkpoints?.length ? <Disclosure title="保存的进度"><div className="run-table">{task.checkpoints.map((cp)=>(
              <div className="run" key={cp.id}><span className="rt">{roleLabel(cp.role)} · {cp.fileCount} 个文件变更</span>
                <span className="rr">{cp.state==='saved'?'检查点已保存':'已用于续跑'}</span>
                <span className="rm"><code>{short(cp.id)}</code> · HEAD <code>{short(cp.headSha)}</code> · {formatTime(cp.createdAt)}{cp.resumedRunId?<> · Run <code>{short(cp.resumedRunId)}</code></>:null}</span></div>
            ))}</div><p className="k small">保存进度不代表交付。显式恢复前会核对文件、暂存区、会话和剩余额度；面板保持只读。</p></Disclosure>:null}
            {olderDeliveries.length || olderReviews.length ? <Disclosure title="其他交付与审查记录">{olderDeliveries.map((d) => (
              <section className="detail-section" key={d.id}><h3>{DELIVERY_LABEL[d.state] ?? d.state}</h3><dl className="concl">
                <dt>候选 SHA</dt><dd><code>{d.candidateSha}</code></dd>
                <dt>上下文</dt><dd><code>{d.contextRef?.digest ?? '—'}</code></dd>
                <dt>检查</dt><dd>{checksOf(d.checks).length ? checksOf(d.checks).map((c, i) => <div key={i} data-check={c.kind}>{c.kind === 'harness' ? <>{c.label} → {c.outcome}</> : <>{c.kind === 'reported' ? 'Agent 报告: ' : ''}<code>{c.label}</code> → {c.outcome}</>}</div>) : '未记录'}</dd>
                <dt>已知缺口</dt><dd>{d.knownGaps?.length ? <ul>{d.knownGaps.map((g, i) => <li key={i}>{g}</li>)}</ul> : '无'}</dd>
              </dl></section>
            ))}
            {olderReviews.map((r) => (
              <section className="detail-section" key={r.id}><h3>审查结论</h3><dl className="concl">
                <dt>结论</dt><dd>{r.verdict}</dd><dt>候选 SHA</dt><dd><code>{r.candidateSha ?? '—'}</code></dd><dt>上下文摘要</dt><dd><code>{r.contextDigest ?? '—'}</code></dd>
              </dl></section>
            ))}</Disclosure> : null}
            <Disclosure title="运行历史">{tRuns.length ? <div className="run-table">{tRuns.map((r) => (
              <div className="run" key={r.id}><span className="rt">{roleLabel(r.role)} · {agentLabel(r.agentId).label || '—'}</span><span className="rr">{runLabel(r.state)}</span>
                <span className="rm"><span className="rid">{short(r.id)}</span> · {modelLabel(r)} · {formatTime(r.startedAt)} → {r.endedAt ? formatTime(r.endedAt) : '—'}</span></div>
            ))}</div> : <p className="k">尚无运行。</p>}</Disclosure>
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
        <span className={`notice${stale ? ' stale' : ''}`} role="status" aria-live="polite">{stale ? '已断连 · 快照已过期' : connected ? '已连接' : '正在连接…'}</span>
      </div>
      <details className="health">
        <summary><span className="state-sum">运行中 <b>{countText(counts?.running)}</b> · 排队 <b>{countText(counts?.queued)}</b> · 未知 <b>{countText(counts?.unknown)}</b>{independent.length || stale ? <> · 独立 <b>{stale ? '未知' : independent.length}</b></> : null}</span></summary>
        <div className="health-body">Codex · 协调者 · 本地调度器：{stale ? '未知' : ({running:'运行中',idle:'空闲',unknown:'未知'})[snapshot?.controller?.state ?? 'unknown']}<br />心跳 {formatTime(snapshot?.controller?.heartbeatAt)} · {stale ? '快照' : '更新于'} {formatTime(snapshot?.observedAt)}</div>
      </details>
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
  const [projectCaps, setProjectCaps] = useState<Record<string, number>>({ ...s?.projectConcurrency });
  const [providerCaps, setProviderCaps] = useState<Record<string, number>>({ ...s?.providerConcurrency });
  const [profiles, setProfiles] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState('');
  const [busy, setBusy] = useState(false);
  const disabled = !!disabledReason || !s || busy;
  const save = async () => {
    const input: SettingsInput = { maxConcurrency: conc, maxFixRounds: rounds, projectConcurrency: projectCaps, providerConcurrency: providerCaps };
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
          <div className="sec-h"><b>项目与模型服务并发</b></div>
          <div className="list">{[
            ...(snapshot?.projects ?? []).map(p => ({ key:p.id, label:p.name || p.id, kind:'项目', caps:projectCaps, set:setProjectCaps })),
            ...[...new Set((snapshot?.profiles ?? []).map(p => p.provider).filter((p):p is string => !!p))].sort().map(p => ({ key:p, label:p, kind:'服务', caps:providerCaps, set:setProviderCaps })),
          ].map(p => <div className="set-row" key={`${p.kind}:${p.key}`}><label className="lbl" htmlFor={`mk-cap-${p.kind}-${p.key}`}>{p.label}<small>{p.kind}同时占用的任务数；受本地并发总上限约束</small></label>
            <span className="ctl"><select id={`mk-cap-${p.kind}-${p.key}`} value={p.caps[p.key] ?? ''} disabled={disabled} onChange={e => { const next={...p.caps}; if(e.target.value) next[p.key]=Number(e.target.value); else delete next[p.key]; p.set(next); }}>
              <option value="">跟随总上限</option>{[1,2,3,4].map(v => <option key={v} value={v}>{v}</option>)}
            </select></span></div>)}</div>
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

import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App, type AppActions } from '../App';
import { RUN_A, RUN_B, snapshot } from './fixtures';
import type { ControlReceipt } from '../generated/workflow';
import { validateEnvelope } from '../generated/validate';

afterEach(() => { cleanup(); sessionStorage.clear(); });
const actions = (over: Partial<AppActions> = {}): AppActions => ({ readonly: false, stop: vi.fn(async () => ({})), settings: vi.fn(async () => ({})), ...over });
const view = (name: string) => fireEvent.click(screen.getByRole('button', { name }));

describe('App', () => {
  it('retains an expanded ended Run through a pending reply and a read-only receipt lookup', async () => {
    const s = snapshot();
    s.tasks[0]!.sessions = [{id:RUN_B,role:'developer',executor:'pi',state:'running',activeRunId:RUN_A,lastSha:'a'.repeat(40),updatedAt:s.observedAt!}];
    const receipt = (id: string): ControlReceipt => ({requestId:id,taskId:'t1',runId:RUN_A,sessionId:RUN_B,kind:'instruction',state:'acknowledged',disposition:'queued',reason:null,createdAt:s.observedAt!,updatedAt:s.observedAt!,runState:'succeeded',outcome:'succeeded'});
    let finish!: (r: ControlReceipt) => void;
    const send = vi.fn(() => new Promise<ControlReceipt>(resolve => { finish = resolve; }));
    const lookup = vi.fn(async (id: string) => receipt(id));
    const intervention = {send,receipt:lookup,followUp:vi.fn(),pause:vi.fn(),stop:vi.fn()};
    const a = actions({readonly:true,intervention});
    const v = render(<App snapshot={s} legacyActive={[]} connected stale={null} actions={a} />);
    fireEvent.click(screen.getByTestId('agent-row').querySelector('.row-btn')!);
    fireEvent.change(screen.getByLabelText('指令'),{target:{value:'clarify receipt semantics'}});
    fireEvent.click(screen.getByRole('button',{name:'发送指令'}));
    const id = sessionStorage.getItem(`meerkat-control:${RUN_A}:instruction`)!;
    const ended = {...s,runs:s.runs.map(r=>r.id===RUN_A?{...r,state:'succeeded',endedAt:s.observedAt}:r),counts:{running:0,queued:0,unknown:0},tasks:s.tasks.map(t=>({...t,state:'delivered',sessions:[]}))};
    v.rerender(<App snapshot={ended} legacyActive={[]} connected stale={null} actions={a} />);
    expect(screen.getByText(/指令 · 正在提交/)).toBeTruthy();
    expect(v.container.querySelector('.state-sum')!.textContent).toBe('运行中 0 · 排队 0 · 未知 0');
    for (const name of ['发送指令','暂停并保留进度','停止运行']) expect((screen.getByRole('button',{name}) as HTMLButtonElement).disabled).toBe(true);
    await act(async () => { finish(receipt(id)); });
    expect(screen.getByText('本次运行已结束，可查询原回执；不能发送新指令。')).toBeTruthy();
    fireEvent.click(screen.getByRole('button',{name:'查询回执'}));
    await screen.findByText('查询完成，回执无变化。');
    expect(lookup).toHaveBeenCalledWith(id); expect(send).toHaveBeenCalledTimes(1);
    expect(intervention.stop).not.toHaveBeenCalled(); expect(intervention.pause).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('agent-row').querySelector('.row-btn')!);
    expect(screen.queryByTestId('agent-row')).toBeNull();
    expect(screen.getByText('当前没有工作流运行。')).toBeTruthy();
  });

  it('labels follow-up and pause receipts in task details without calling them stop requests', () => {
    const s = snapshot();
    s.tasks[0]!.controlReceipts = (['instruction','follow_up','pause','stop','wrap_up'] as const).map((kind,i)=>({requestId:`receipt-${i}`,taskId:'t1',runId:RUN_A,sessionId:RUN_B,kind,state:'acknowledged',disposition:'queued',reason:null,createdAt:s.observedAt!,updatedAt:s.observedAt!,runState:'running',outcome:null}));
    render(<App snapshot={s} legacyActive={[]} connected stale={null} actions={actions({readonly:true})} />);
    view('Tasks'); fireEvent.click(screen.getByText('Fix parser'));
    const labels = [...screen.getByText('控制回执',{exact:true}).closest('details')!.querySelectorAll('.rt')].map(e=>e.textContent);
    expect(labels).toEqual(['指令 · 执行器已接收','后续指令 · 执行器已接收','暂停 · 执行器已接收','停止 · 执行器已接收','收尾 · 执行器已接收']);
  });

  it('shows monitor thresholds without inventing a remaining task cap', () => {
    const s=snapshot(); s.tasks[0]!.budget={mode:'monitor',maxTokens:100,maxWallSeconds:600,maxFixRounds:1};
    s.tasks[0]!.budgetEvidence={mode:'monitor',warning:true,authorizedTokens:100,availableTokens:null,confirmedTokens:150,reservedTokens:200,requests:2,pendingRequests:0,unknownRequests:1,overrun:false};
    expect(validateEnvelope({ok:true,data:s,legacyActive:[],sessionToken:'host'})).toBe(true);
    render(<App snapshot={s} legacyActive={[]} connected stale={null} actions={actions({readonly:true})} />);
    view('Tasks'); fireEvent.click(screen.getByText('Fix parser'));
    const text=screen.getByRole('dialog').textContent!;
    expect(text).toContain('已达到 token 提示阈值'); expect(text).toContain('结果未知 1'); expect(text).not.toContain('可申请额度');
  });

  it('saves concurrency maps including explicit clears and refuses malformed snapshots', async () => {
    const s=snapshot(); s.settings!.projectConcurrency={p1:1}; s.settings!.providerConcurrency={prov:2};
    expect(validateEnvelope({ok:true,data:s,legacyActive:[],sessionToken:'host'})).toBe(true);
    const settings=vi.fn(async()=>({}));
    render(<App snapshot={s} legacyActive={[]} connected stale={null} actions={actions({settings})} />);
    fireEvent.click(screen.getByRole('button',{name:'设置'}));
    fireEvent.change(screen.getByLabelText(/Example/),{target:{value:''}});
    fireEvent.change(screen.getByLabelText(/prov/),{target:{value:'1'}});
    fireEvent.click(screen.getByRole('button',{name:/保存/}));
    await waitFor(()=>expect(settings).toHaveBeenCalledWith(expect.objectContaining({projectConcurrency:{},providerConcurrency:{prov:1}})));
    s.settings!.projectConcurrency={p1:0};
    expect(validateEnvelope({ok:true,data:s,legacyActive:[],sessionToken:'host'})).toBe(false);
  });

  it('separates a durable control acknowledgement from Run completion and keeps authority private', () => {
    const s = snapshot();
    s.tasks[0]!.controlReceipts = [{requestId:RUN_A,taskId:'t1',runId:RUN_A,sessionId:RUN_A,kind:'wrap_up',state:'acknowledged',disposition:'queued',reason:null,createdAt:'2025-01-01T00:00:00Z',updatedAt:'2025-01-01T00:01:00Z',runState:'running',outcome:null},
      {requestId:'unknown-control',taskId:'t1',runId:RUN_A,sessionId:RUN_A,kind:'wrap_up',state:'unknown',disposition:null,reason:'controller_interrupted',createdAt:'2025-01-01T00:00:00Z',updatedAt:'2025-01-01T00:02:00Z',runState:'unknown',outcome:'unknown'}];
    expect(validateEnvelope({ok:true,data:s,legacyActive:[],sessionToken:'host'})).toBe(true);
    render(<App snapshot={s} legacyActive={[]} connected stale={null} actions={actions({readonly:true})} />);
    view('Tasks');fireEvent.click(screen.getByText('Fix parser'));
    const dialog = screen.getByRole('dialog');const text = dialog.textContent!;
    expect(text).toContain('收尾 · 执行器已接收');expect(text).toContain('协议回执：已入队');
    expect(text).toContain('实际运行：运行中');expect(text).toContain('结束结果尚未确认');
    expect(text).toContain('服务中断，保留未知且不重发');
    expect([...dialog.querySelectorAll('button')].some(b => /收尾|重发/.test(b.textContent ?? ''))).toBe(false);
    s.tasks[0]!.controlReceipts[0]={...s.tasks[0]!.controlReceipts[0]!,...{authorizationRef:'private'}};
    expect(validateEnvelope({ok:true,data:s,legacyActive:[],sessionToken:'host'})).toBe(false);
  });
  it('shows original and added allowances separately, without a grant button or private references', () => {
    const s=snapshot();s.tasks[0]!.budget={maxTokens:100,maxWallSeconds:600,maxFixRounds:1};
    s.tasks[0]!.budgetAuthorization={taskId:s.tasks[0]!.id,originalTokens:100,originalWallSeconds:600,addedTokens:950,addedWallSeconds:100,authorizedTokens:1050,authorizedWallSeconds:700,revision:1,decisions:[{requestId:'decision-1',revision:1,addTokens:950,addWallSeconds:100,reason:'Finish original scope',createdAt:'2025-01-01T00:10:00Z'}]};
    expect(validateEnvelope({ok:true,data:s,legacyActive:[],sessionToken:'host'})).toBe(true);
    render(<App snapshot={s} legacyActive={[]} connected stale={null} actions={actions({readonly:true})} />);
    view('Tasks');fireEvent.click(screen.getByText('Fix parser'));
    const text=screen.getByRole('dialog').textContent!;
    expect(text).toContain('原始预算100');expect(text).toContain('当前已授权1,050');expect(text).toContain('Finish original scope');expect(text).toContain('保留已用 token');
    expect(screen.queryByRole('button',{name:/追加|授权|续跑/})).toBeNull();
    s.tasks[0]!.budgetAuthorization.decisions[0]={...s.tasks[0]!.budgetAuthorization.decisions[0]!,...{authorizationRef:'private'}};
    expect(validateEnvelope({ok:true,data:s,legacyActive:[],sessionToken:'host'})).toBe(false);
  });
  it('shows saved progress without claiming delivery or exposing a resume control', () => {
    const s=snapshot();s.tasks[0]!.state='paused';
    s.tasks[0]!.checkpoints=[{id:'checkpoint-1',runId:RUN_A,role:'developer',headSha:'a'.repeat(40),fileCount:2,state:'saved',resumedRunId:null,createdAt:'2025-01-01T00:10:00Z'}];
    expect(validateEnvelope({ok:true,data:s,legacyActive:[],sessionToken:'host'})).toBe(true);
    render(<App snapshot={s} legacyActive={[]} connected stale={null} actions={actions({readonly:true})} />);
    view('Tasks');fireEvent.click(screen.getByText('Fix parser'));
    const text=screen.getByRole('dialog').textContent!;
    expect(text).toContain('已暂停');expect(text).toContain('检查点已保存');expect(text).toContain('2 个文件变更');
    expect(text).toContain('保存进度不代表交付');expect(text).toContain('暂存区');
    expect(screen.queryByRole('button',{name:/恢复|续跑/})).toBeNull();
    s.tasks[0]!.checkpoints[0]={...s.tasks[0]!.checkpoints[0]!,...{fileRef:'/private/checkpoint'}};
    expect(validateEnvelope({ok:true,data:s,legacyActive:[],sessionToken:'host'})).toBe(false);
  });
  it('shows wrap-up without claiming completion, and unknown budget evidence stays unknown', () => {
    const s = snapshot();
    s.runs[0]!.events = [{ type: 'budget', summary: 'wrap_up_accepted' }];
    s.tasks[0]!.budget = { maxTokens: 1000, maxWallSeconds: 600, maxFixRounds: 1 };
    s.tasks[0]!.budgetEvidence = { authorizedTokens: 1000, availableTokens: null, confirmedTokens: 80,
      reservedTokens: 600, requests: 2, pendingRequests: 0, unknownRequests: 1, overrun: false };
    s.tasks[0]!.sessions = [{ id: 'ss1', role: 'developer', executor: 'pi', state: 'unknown', activeRunId: null,
      lastSha: 'a'.repeat(40), updatedAt: '2025-01-01T00:10:00Z' }];
    render(<App snapshot={s} legacyActive={[]} connected stale={null} actions={actions({ readonly: true })} />);
    expect(screen.getByText('收尾中')).toBeTruthy();
    expect(screen.getAllByText('收尾请求已接受，等待执行结束').length).toBeGreaterThan(0);
    view('Tasks');
    fireEvent.click(screen.getByText('Fix parser'));
    const text = screen.getByRole('dialog').textContent!;
    expect(text).toContain('未知（预留口径）');
    expect(text).toContain('结果未知 1');
    expect(text).toContain('身份未知');
    expect(text).not.toContain('可申请额度0');
  });
  it('shows agent rows with Agent-NN for historical Pi-NN and custom IDs unchanged; dedups legacy runs', () => {
    render(<App snapshot={snapshot()} legacyActive={[{ runId: RUN_A.toUpperCase(), task: 'dup' }, { runId: 'x', task: 'Standalone' }]} connected stale={null} actions={actions()} />);
    const rows = screen.getAllByTestId('agent-row');
    expect(rows).toHaveLength(1);
    expect(rows[0]!.textContent).toContain('Agent-07 · 开发');
    expect(rows[0]!.textContent).toContain('ran tests');
    fireEvent.click(rows[0]!.querySelector('.row-btn')!);
    expect(rows[0]!.textContent).toContain('阶段 edit');
    expect(screen.getAllByTestId('independent-row')).toHaveLength(1);
    expect(screen.queryByText('dup')).toBeNull();
    view('Usage');
    expect(screen.getByText(/reviewer-x/)).toBeTruthy();
  });

  it('shows stop acceptance separately from a stopped run and reuses the requestId', async () => {
    const stop = vi.fn(async () => ({ accepted: true }));
    render(<App snapshot={snapshot()} legacyActive={[]} connected stale={null} actions={actions({ stop })} />);
    fireEvent.click(screen.getAllByTestId('agent-row')[0]!.querySelector('.row-btn')!);
    fireEvent.click(screen.getByRole('button', { name: '停止运行' }));
    await screen.findByText(/停止请求已接受/);
    expect(stop).toHaveBeenCalledWith(RUN_A, expect.stringMatching(/^[0-9a-f-]{36}$/));
    expect(screen.getAllByText('运行中').length).toBeGreaterThan(0); // run itself not shown as stopped
  });

  it('disables writes when stale or readonly and shows unknown counts', () => {
    const { rerender } = render(<App snapshot={snapshot()} legacyActive={[]} connected={false} stale="实时连接中断" actions={actions()} />);
    expect(screen.getByText(/已断连/, { selector: 'b' })).toBeTruthy();
    fireEvent.click(screen.getAllByTestId('agent-row')[0]!.querySelector('.row-btn')!);
    expect((screen.getByRole('button', { name: '停止运行' }) as HTMLButtonElement).disabled).toBe(true);
    rerender(<App snapshot={snapshot()} legacyActive={[]} connected stale={null} actions={actions({ readonly: true })} />);
    expect((screen.getByRole('button', { name: '停止运行' }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: '设置' }));
    expect((screen.getByRole('button', { name: /保存/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('keeps unknown usage unknown and separates wall vs agent time', () => {
    render(<App snapshot={snapshot()} legacyActive={[]} connected stale={null} actions={actions()} now={() => Date.parse('2025-01-01T00:10:00Z')} />);
    view('Usage');
    const stats = screen.getByTestId('usage-stats').textContent!;
    expect(stats).toContain('未知总 token'); // no Run reported a total, despite a known input
    expect(stats).toContain('未知费用');
    expect(stats).toContain('15分Agent 耗时');
    const detail = screen.getByText('分类用量与统计口径').closest('details')!;
    expect(detail.open).toBe(false);
    expect(detail.textContent).toContain('输入≥ 100');
    expect(detail.textContent).toContain('输出未知');
    expect(detail.textContent).toContain('墙钟时间10分');
  });

  it('task details show exact SHA, context digest, checks and gaps; unsafe links are not rendered', () => {
    render(<App snapshot={snapshot()} legacyActive={[]} connected stale={null} actions={actions()} />);
    view('Tasks');
    fireEvent.click(screen.getByText('Fix parser'));
    const dlg = screen.getByRole('dialog');
    expect(dlg.textContent).toContain('b'.repeat(40));
    expect(dlg.textContent).toContain('sha256:ctx');
    expect(dlg.textContent).toContain('npm test');
    expect(dlg.textContent).toContain('no e2e');
    expect(dlg.textContent).toContain('交付候选');
    expect(dlg.querySelector('a')).toBeNull();
    act(() => { window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })); });
    return waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('keeps full versions and budget evidence accessible while foregrounding the candidate', () => {
    const s = snapshot();
    s.tasks[0]!.budget = {maxTokens:1000,maxWallSeconds:600,maxFixRounds:1};
    s.tasks[0]!.budgetEvidence = {authorizedTokens:1000,availableTokens:null,confirmedTokens:0,reservedTokens:500,requests:1,pendingRequests:0,unknownRequests:1,overrun:false};
    render(<App snapshot={s} legacyActive={[]} connected stale={null} actions={actions()} />);
    view('Tasks'); fireEvent.click(screen.getByText('Fix parser'));
    const d = screen.getByRole('dialog');
    const versions = screen.getByText('版本与上下文').closest('details')!;
    const budget = screen.getByText('任务预算与账本').closest('details')!;
    expect(versions.open).toBe(false); expect(budget.open).toBe(false);
    expect(versions.textContent).toContain('a'.repeat(40)); expect(versions.textContent).toContain('sha256:ctx');
    expect(budget.textContent).toContain('未知（预留口径）');
    expect(screen.getByText(/请求结果未知 1 次/).closest('details')).toBeNull();
    expect((d.querySelector('[data-check]')!.closest('details') as HTMLDetailsElement).open).toBe(false);
    expect(screen.getByText('历史检查 1 项').closest('details')).toBeNull();
    expect(d.textContent!.indexOf('no e2e')).toBeLessThan(d.textContent!.indexOf('任务预算与账本'));
  });

  it('does not present a review of an older SHA as the current candidate review', () => {
    const s = snapshot(); s.reviews[0]!.candidateSha = 'c'.repeat(40);
    render(<App snapshot={s} legacyActive={[]} connected stale={null} actions={actions()} />);
    view('Tasks'); fireEvent.click(screen.getByText('Fix parser'));
    expect(screen.getByText('当前候选尚无匹配的审查记录')).toBeTruthy();
    const older = screen.getByText('其他交付与审查记录').closest('details')!;
    expect(older.open).toBe(false); expect(older.textContent).toContain('c'.repeat(40)); expect(older.textContent).toContain('pass');
  });

  it('uses the selected project for Run counts and queued Task counts', () => {
    const s = snapshot(); s.projects.push({id:'p2',name:'Other'});
    s.tasks.push({id:'t2',projectId:'p2',title:'Other queued task',state:'queued'});
    s.runs.push({...s.runs[0]!,id:'other-run',taskId:'t2',state:'unknown'});
    s.counts = {running:1,queued:1,unknown:1};
    const v = render(<App snapshot={s} legacyActive={[]} connected stale={null} actions={actions()} />);
    fireEvent.change(screen.getByLabelText('项目筛选'),{target:{value:'p1'}});
    expect(v.container.querySelector('.state-sum')!.textContent).toBe('运行中 1 · 排队 0 · 未知 0');
    fireEvent.change(screen.getByLabelText('项目筛选'),{target:{value:'p2'}});
    expect(v.container.querySelector('.state-sum')!.textContent).toBe('运行中 0 · 排队 1 · 未知 1');
    expect(screen.getAllByTestId('agent-row')).toHaveLength(1);
    v.rerender(<App snapshot={{...s,counts:{running:null,queued:null,unknown:null}}} legacyActive={[]} connected stale={null} actions={actions()} />);
    expect(v.container.querySelector('.state-sum')!.textContent).toBe('运行中 未知 · 排队 未知 · 未知 未知');
  });

  it('renders named harness checks, Agent-reported evidence, unknown values and legacy checks', () => {
    const checks = [
      { name: 'git_head_matches_candidate', status: 'pass' }, { name: 'worktree_clean', status: 'pass' },
      { name: 'changed_paths_in_scope', status: 'pass' }, { name: 'context_digest_matches', status: 'pass' }, { name: 'review_verdict', status: 'pass' },
      { name: 'reported', status: 'reported', command: 'wc -w docs/pilot.md', result: '141 words (<150)' },
      { name: 'reported', status: 'reported', command: 'npm test' },
      { name: 'custom_probe', status: 'flaky' }, { name: 'mystery' },
      { command: 'go test ./...', exitCode: 1 }, { command: 'npm run lint', result: 'ok' }, { command: 'make' },
    ];
    const s = snapshot();
    s.deliveries = [{ ...s.deliveries[0]!, checks }];
    render(<App snapshot={s} legacyActive={[]} connected stale={null} actions={actions({ readonly: true })} />);
    view('Tasks');
    fireEvent.click(screen.getByText('Fix parser'));
    const rows = [...screen.getByRole('dialog').querySelectorAll('[data-check]')].map((e) => [e.getAttribute('data-check'), e.textContent]);
    expect(rows).toEqual([
      ['harness', 'HEAD 与候选一致 → 通过'], ['harness', '工作区干净 → 通过'], ['harness', '改动路径在范围内 → 通过'],
      ['harness', '上下文摘要一致 → 通过'], ['harness', '审查结论 → 通过'],
      ['reported', 'Agent 报告: wc -w docs/pilot.md → 141 words (<150)'], ['reported', 'Agent 报告: npm test → 未知'],
      ['harness', 'custom_probe → flaky'], ['harness', 'mystery → 未知'],
      ['legacy', 'go test ./... → exit 1'], ['legacy', 'npm run lint → ok'], ['legacy', 'make → 未知'],
    ]);
    expect(screen.getByRole('dialog').textContent).not.toContain('— → 未知');
  });

  it('renders readable empty and error states', () => {
    render(<App snapshot={null} legacyActive={[]} connected={false} stale="HTTP 500" actions={actions()} />);
    expect(screen.getByText('无法读取工作流状态。')).toBeTruthy();
  });

  it('shows interrupted completion evidence without granting recovery controls', () => {
    const s = snapshot();
    s.tasks[0]!.state = 'unknown';
    s.tasks[0]!.recoveryEvidence = [{ runId: RUN_A, role: 'developer', state: 'pending', candidateSha: 'b'.repeat(40), recordedAt: '2026-10-03T00:00:00Z', requestId: null, recoveredAt: null }];
    render(<App snapshot={s} legacyActive={[]} connected stale={null} actions={actions({ readonly: true })} />);
    view('Tasks'); fireEvent.click(screen.getByText('Fix parser'));
    const dialog = screen.getByRole('dialog');
    expect(dialog.textContent).toContain('完成证据已保存，待核对');
    expect(dialog.textContent).toContain('保存证据不会自动续跑');
    expect([...dialog.querySelectorAll('button')].some(b => /恢复|续跑/.test(b.textContent ?? ''))).toBe(false);
  });
});

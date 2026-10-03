import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App, type AppActions } from '../App';
import { RUN_A, snapshot } from './fixtures';
import { validateEnvelope } from '../generated/validate';

afterEach(cleanup);
const actions = (over: Partial<AppActions> = {}): AppActions => ({ readonly: false, stop: vi.fn(async () => ({})), settings: vi.fn(async () => ({})), ...over });
const view = (name: string) => fireEvent.click(screen.getByRole('button', { name }));

describe('App', () => {
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
    expect(stats).toContain('≥ 100'); // input known for one of two runs
    expect(stats).toContain('未知输出');
    expect(stats).toContain('未知费用');
    expect(stats).toContain('10分墙钟时间');
    expect(stats).toContain('Agent 耗时合计 15分');
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
});

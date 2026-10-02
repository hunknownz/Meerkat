import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { App, type AppActions } from '../App';
import { RUN_A, snapshot } from './fixtures';

afterEach(cleanup);
const actions = (over: Partial<AppActions> = {}): AppActions => ({ readonly: false, stop: vi.fn(async () => ({})), settings: vi.fn(async () => ({})), ...over });
const view = (name: string) => fireEvent.click(screen.getByRole('button', { name }));

describe('App', () => {
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

  it('renders readable empty and error states', () => {
    render(<App snapshot={null} legacyActive={[]} connected={false} stale="HTTP 500" actions={actions()} />);
    expect(screen.getByText('无法读取工作流状态。')).toBeTruthy();
  });
});

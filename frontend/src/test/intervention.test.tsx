import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { Intervention } from '../Intervention';
import { App } from '../App';
import { McpTransport } from '../mcp-transport';
import type { ControlReceipt } from '../generated/workflow';
import type { InterventionActions } from '../transport';
import { RUN_A, RUN_B, snapshot } from './fixtures';

afterEach(() => { cleanup(); sessionStorage.clear(); });
const rc = (id = RUN_B): ControlReceipt => ({requestId:id,taskId:'t1',runId:RUN_A,sessionId:RUN_B,kind:'instruction',state:'acknowledged',disposition:'queued',reason:null,createdAt:'2026-10-04T00:00:00Z',updatedAt:'2026-10-04T00:00:00Z',runState:'running',outcome:null});
const actions = (): InterventionActions => ({ send: vi.fn(async input => rc(input.requestId)), followUp: vi.fn(async input=>({...rc(input.requestId),kind:'follow_up' as const})), pause: vi.fn(async input=>({...rc(input.requestId),kind:'pause' as const})), receipt: vi.fn(async id => rc(id)), stop: vi.fn(async (_run, id) => ({...rc(id),kind:'stop' as const,state:'accepted' as const,disposition:null})) });

it('sends to the same session once, displays queue acknowledgement and retains request ID only', async () => {
  const a=actions(); const run=snapshot().runs[0]!;
  const {rerender}=render(<Intervention run={run} sessionId={RUN_B} actions={a} disabled={false} receipts={[]} />);
  fireEvent.change(screen.getByLabelText('指令'),{target:{value:'PRIVATE_DIRECTION'}});
  fireEvent.click(screen.getByRole('button',{name:'发送指令'}));fireEvent.click(screen.getByRole('button',{name:'发送指令'}));
  await screen.findByText(/指令 · 已排队/);
  expect(a.send).toHaveBeenCalledTimes(1);expect(a.send).toHaveBeenCalledWith({runId:RUN_A,sessionId:RUN_B,requestId:expect.any(String),message:'PRIVATE_DIRECTION'});
  expect(screen.getByLabelText('指令').getAttribute('disabled')).toBeNull();
  expect(sessionStorage.getItem(`meerkat-control:${RUN_A}:instruction`)).toMatch(/^[0-9a-f-]{36}$/);
  expect(JSON.stringify(sessionStorage)).not.toContain('PRIVATE_DIRECTION');
  rerender(<Intervention run={run} sessionId={RUN_B} actions={a} disabled receipts={[]} />);
  fireEvent.change(screen.getByLabelText('指令'),{target:{value:'new'}});
  expect((screen.getByRole('button',{name:'发送指令'}) as HTMLButtonElement).disabled).toBe(true);
});

it('lost replies disable new sends until a read confirms the original UUID, including reopen', async () => {
  const a=actions();a.send=vi.fn(async()=>{throw new Error('timeout')});const run=snapshot().runs[0]!;
  const v=render(<Intervention run={run} sessionId={RUN_B} actions={a} disabled={false} receipts={[]} />);
  fireEvent.change(screen.getByLabelText('指令'),{target:{value:'direction'}});fireEvent.click(screen.getByRole('button',{name:'发送指令'}));
  await screen.findByText(/指令 · 结果未知/);const id=sessionStorage.getItem(`meerkat-control:${RUN_A}:instruction`)!;
  expect((screen.getByRole('button',{name:'发送指令'}) as HTMLButtonElement).disabled).toBe(true);v.unmount();
  render(<Intervention run={run} sessionId={RUN_B} actions={a} disabled={false} receipts={[]} />);
  fireEvent.click(screen.getByRole('button',{name:'查询回执'}));await screen.findByText(/指令 · 已排队/);
  expect(a.receipt).toHaveBeenCalledWith(id);expect(a.send).toHaveBeenCalledTimes(1);
});

it('can stop after an uncertain instruction and never claims exit', async () => {
  const a=actions();a.send=vi.fn(async()=>{throw new Error('timeout')});
  render(<Intervention run={snapshot().runs[0]!} sessionId={RUN_B} actions={a} disabled={false} receipts={[]} />);
  fireEvent.change(screen.getByLabelText('指令'),{target:{value:'direction'}});fireEvent.click(screen.getByRole('button',{name:'发送指令'}));await screen.findByText(/指令 · 结果未知/);
  fireEvent.click(screen.getByRole('button',{name:'停止运行'}));await screen.findByText(/停止请求已接受/);
  expect(a.stop).toHaveBeenCalledTimes(1);expect(screen.queryByText('已停止')).toBeNull();
});

it('snapshot refresh preserves typed input in the actual App; readonly settings still allow bounded controls', async () => {
  const s=snapshot();s.tasks[0]!.sessions=[{id:RUN_B,role:'developer',executor:'pi',state:'running',activeRunId:RUN_A,lastSha:'a'.repeat(40),updatedAt:s.observedAt!}];
  const a={readonly:true,intervention:actions(),stop:vi.fn(),settings:vi.fn()};
  const v=render(<App snapshot={s} legacyActive={[]} connected stale={null} actions={a} />);
  fireEvent.click(screen.getAllByTestId('agent-row')[0]!.querySelector('.row-btn')!);fireEvent.change(screen.getByLabelText('指令'),{target:{value:'keep typed input'}});
  v.rerender(<App snapshot={{...s,observedAt:'2026-10-04T00:10:00Z'}} legacyActive={[]} connected stale={null} actions={a} />);
  expect((screen.getByLabelText('指令') as HTMLTextAreaElement).value).toBe('keep typed input');
  expect((screen.getByRole('button',{name:'发送指令'}) as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(screen.getByRole('button',{name:'设置'}));expect((screen.getByRole('button',{name:/保存/}) as HTMLButtonElement).disabled).toBe(true);
});

it('MCP controls use app-only host tools and query a lost reply without replaying', async () => {
  const callServerTool=vi.fn(async({name}: {name:string})=>name==='send_run_instruction'?{isError:true,structuredContent:{status:'unknown'}}:{_meta:{receipt:rc()}});
  const t=new McpTransport({callServerTool});t.setConnected();
  await expect(t.intervention.send({runId:RUN_A,sessionId:RUN_B,requestId:RUN_B,message:'direction'})).rejects.toThrow('查询');
  await expect(t.intervention.receipt(RUN_B)).resolves.toEqual(rc());
  expect(callServerTool.mock.calls.map(c=>c[0].name)).toEqual(['send_run_instruction','get_intervention_receipt']);
  t.close();await expect(t.intervention.receipt(RUN_B)).rejects.toThrow('未连接');
});


it('unchanged successful lookup reports completion, stays queued and sends no write', async () => {
  const a=actions();a.receipt=vi.fn(async id=>({...rc(id),kind:'follow_up' as const}));
  render(<Intervention run={snapshot().runs[0]!} sessionId={RUN_B} actions={a} disabled={false} receipts={[]} />);
  fireEvent.change(screen.getByLabelText('指令生效时机'),{target:{value:'follow_up'}});
  fireEvent.change(screen.getByLabelText('指令'),{target:{value:'finish after this turn'}});
  fireEvent.click(screen.getByRole('button',{name:'发送指令'}));await screen.findByText(/后续指令 · 已排队/);
  fireEvent.click(screen.getByRole('button',{name:'查询回执'}));
  await screen.findByText('查询完成，回执无变化。');
  expect(screen.getByText(/后续指令 · 已排队/)).toBeTruthy();
  expect(screen.queryByText(/已处理/)).toBeNull();
  expect(screen.queryByText('查询完成，回执已更新。')).toBeNull();
  expect(a.followUp).toHaveBeenCalledTimes(1);expect(a.send).not.toHaveBeenCalled();
  expect(a.pause).not.toHaveBeenCalled();expect(a.stop).not.toHaveBeenCalled();
});

it('in-flight lookup is visibly pending and a failure stays distinctly separate without writes', async () => {
  const a=actions();render(<Intervention run={snapshot().runs[0]!} sessionId={RUN_B} actions={a} disabled={false} receipts={[]} />);
  fireEvent.change(screen.getByLabelText('指令'),{target:{value:'direction'}});
  fireEvent.click(screen.getByRole('button',{name:'发送指令'}));await screen.findByText(/指令 · 已排队/);
  let reject!: (e: Error)=>void;
  a.receipt=vi.fn(()=>new Promise<ControlReceipt>((_res,rej)=>{reject=rej}));
  fireEvent.click(screen.getByRole('button',{name:'查询回执'}));
  await screen.findByText(/指令 · 正在查询…/);
  expect(screen.queryByText('查询完成，回执无变化。')).toBeNull();
  reject(new Error('unreachable'));
  await screen.findByText('暂未查到有效回执。保留原请求，不会自动重发。');
  expect(screen.getByText(/指令 · 已排队，等待 Agent 处理/)).toBeTruthy();
  const id=sessionStorage.getItem(`meerkat-control:${RUN_A}:instruction`)!;
  expect(a.receipt).toHaveBeenCalledWith(id);
  expect(a.send).toHaveBeenCalledTimes(1);expect(a.pause).not.toHaveBeenCalled();expect(a.stop).not.toHaveBeenCalled();
});

it('a later lookup reports an updated receipt distinctly', async () => {
  const a=actions();let advanced=false;
  a.receipt=vi.fn(async (id:string)=>{if(advanced) return {...rc(id),state:'processed' as const,updatedAt:'2026-10-04T00:02:00Z'};return rc(id);});
  render(<Intervention run={snapshot().runs[0]!} sessionId={RUN_B} actions={a} disabled={false} receipts={[]} />);
  fireEvent.change(screen.getByLabelText('指令'),{target:{value:'direction'}});
  fireEvent.click(screen.getByRole('button',{name:'发送指令'}));await screen.findByText(/指令 · 已排队/);
  fireEvent.click(screen.getByRole('button',{name:'查询回执'}));await screen.findByText('查询完成，回执无变化。');
  advanced=true;
  fireEvent.click(screen.getByRole('button',{name:'查询回执'}));await screen.findByText('查询完成，回执已更新。');
  expect(screen.getByText(/指令 · 已处理/)).toBeTruthy();
  expect(a.receipt).toHaveBeenCalledTimes(2);
  expect(a.send).toHaveBeenCalledTimes(1);expect(a.followUp).not.toHaveBeenCalled();
  expect(a.pause).not.toHaveBeenCalled();expect(a.stop).not.toHaveBeenCalled();
});

it('a Run outcome change is visible without claiming that a queued instruction was delivered', async () => {
  const a=actions();a.receipt=vi.fn(async id=>({...rc(id),runState:'stopped'}));
  render(<Intervention run={snapshot().runs[0]!} sessionId={RUN_B} actions={a} disabled={false} receipts={[]} />);
  fireEvent.change(screen.getByLabelText('指令'),{target:{value:'direction'}});
  fireEvent.click(screen.getByRole('button',{name:'发送指令'}));await screen.findByText(/指令 · 已排队/);
  fireEvent.click(screen.getByRole('button',{name:'查询回执'}));await screen.findByText('查询完成，回执已更新。');
  expect(screen.getByText(/指令 · 已排队/)).toBeTruthy();expect(screen.queryByText(/指令 · 已处理/)).toBeNull();
  expect(a.send).toHaveBeenCalledTimes(1);expect(a.followUp).not.toHaveBeenCalled();
});

it('a receipt for another request is rejected without replacing the original queue state', async () => {
  const a=actions();a.receipt=vi.fn(async()=>rc(RUN_B));
  render(<Intervention run={snapshot().runs[0]!} sessionId={RUN_B} actions={a} disabled={false} receipts={[]} />);
  fireEvent.change(screen.getByLabelText('指令'),{target:{value:'direction'}});
  fireEvent.click(screen.getByRole('button',{name:'发送指令'}));await screen.findByText(/指令 · 已排队/);
  fireEvent.click(screen.getByRole('button',{name:'查询回执'}));
  await screen.findByText('暂未查到有效回执。保留原请求，不会自动重发。');
  expect(screen.getByText(/指令 · 已排队/)).toBeTruthy();expect(a.send).toHaveBeenCalledTimes(1);
});

it('queues follow-up after a turn and graceful pause closes new input without claiming completion',async()=>{
 const a=actions();render(<Intervention run={snapshot().runs[0]!} sessionId={RUN_B} actions={a} disabled={false} receipts={[]} />);
 fireEvent.change(screen.getByLabelText('指令生效时机'),{target:{value:'follow_up'}});
 fireEvent.change(screen.getByLabelText('指令'),{target:{value:'finish after this turn'}});
 fireEvent.click(screen.getByRole('button',{name:'发送指令'}));await screen.findByText(/后续指令 · 已排队/);
 expect(a.followUp).toHaveBeenCalledTimes(1);expect(a.send).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole('button',{name:'暂停并保留进度'}));await screen.findByText(/暂停 · 已排队/);
 expect(a.pause).toHaveBeenCalledTimes(1);expect((screen.getByLabelText('指令') as HTMLTextAreaElement).disabled).toBe(true);
 expect(screen.queryByText('已暂停')).toBeNull();
 expect(JSON.stringify(sessionStorage)).not.toContain('finish after this turn');
});

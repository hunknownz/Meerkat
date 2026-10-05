import { useEffect, useRef, useState } from 'react';
import type { ControlReceipt, Run } from './generated/workflow';
import { ControlActionError, isUuid, newRequestId, type InterventionActions } from './transport';
import { formatTime, short } from './model';

type Entry = { id: string; status: 'pending' | 'unknown' | 'rejected'; receipt?: ControlReceipt };
function saved(runId: string, kind: string): Entry | null {
  try { const id = sessionStorage.getItem(`meerkat-control:${runId}:${kind}`); return id && isUuid(id) ? { id, status: 'unknown' } : null; } catch { return null; }
}
function remember(runId: string, kind: string, id: string) {
  // Retain only request IDs for recovery; instruction text stays in private server storage.
  try { sessionStorage.setItem(`meerkat-control:${runId}:${kind}`, id); } catch { /* in-memory receipt remains available */ }
}
function label(entry: Entry): string {
  const rc = entry.receipt;
  if (!rc) return entry.status === 'pending' ? '正在提交…' : entry.status === 'rejected' ? '未发送或已拒绝' : '结果未知，请查询回执';
  if (rc.kind === 'pause' && rc.outcome==='stopped') return '运行已结束，请核对任务检查点后续跑';
  if (rc.kind === 'stop') return rc.state === 'processed' ? '停止请求已处理，请核对运行结果' : rc.state === 'unknown' ? '停止结果未知' : '停止请求已接受，等待运行停止';
  if (rc.state === 'acknowledged') return rc.disposition === 'queued' ? '已排队，等待 Agent 处理' : '执行器已接收';
  return { accepted: '已保存，等待发送', sending: '正在发送', rejected: '指令已拒绝', unknown: '发送结果未知', processed: '已处理' }[rc.state];
}
function unresolved(e: Entry | null) { return !!e && !['acknowledged', 'rejected', 'processed'].includes(e.receipt?.state ?? e.status); }
/** Stable fingerprint of a read receipt; an identical read means the server has not moved the control. */
function receiptKey(r: ControlReceipt): string { return JSON.stringify([r.kind, r.state, r.disposition, r.outcome, r.reason, r.runState, r.updatedAt]); }

/** One bounded human control surface. Closing it never stops or replays a Run. */
export function Intervention({ run, sessionId, actions, disabled, receipts }: {
  run: Run; sessionId?: string; actions: InterventionActions; disabled: boolean; receipts: ControlReceipt[];
}) {
  const [text, setText] = useState('');
  const [timing, setTiming] = useState<'instruction'|'follow_up'>('instruction');
  const [pause, setPause] = useState<Entry|null>(()=>saved(run.id,'pause'));
  const pausing = useRef(false);
  const [entry, setEntry] = useState<Entry | null>(() => saved(run.id, 'instruction'));
  const [stop, setStop] = useState<Entry | null>(() => saved(run.id, 'stop'));
  const [querying, setQuerying] = useState(false);
  const [looking, setLooking] = useState<null | 'entry' | 'stop' | 'pause'>(null);
  const [note, setNote] = useState('');
  const busy = useRef(false);
  const stopping = useRef(false);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const latest = receipts.find(r => r.requestId === entry?.id);
  const latestStop = receipts.find(r => r.requestId === stop?.id);
  const latestPause = receipts.find(r=>r.requestId===pause?.id) ?? receipts.find(r=>r.kind==='pause' && r.runId===run.id);
  useEffect(()=>{if(latestPause) setPause({id:latestPause.requestId,status:'unknown',receipt:latestPause});},[latestPause]);
  useEffect(() => { if (latest) setEntry({ id: latest.requestId, status: 'unknown', receipt: latest }); }, [latest]);
  useEffect(() => { if (latestStop) setStop({ id: latestStop.requestId, status: 'unknown', receipt: latestStop }); }, [latestStop]);
  const canSend = !disabled && !!sessionId && !run.stopRequested && !stop && !(pause && pause.status!=='rejected' && pause.receipt?.state!=='rejected') && ['developer', 'polisher'].includes(run.role) && run.state === 'running';
  const tooLong = [...text].length > 4000 || new TextEncoder().encode(text).length > 16000;
  const send = async () => {
    if (busy.current || !canSend || unresolved(entry) || !text.trim() || tooLong || !sessionId) return;
    busy.current = true;
    const id = newRequestId(); remember(run.id, 'instruction', id);
    setEntry({ id, status: 'pending' }); setNote('');
    try {
      const rc = await (timing==='follow_up'?actions.followUp:actions.send)({ runId: run.id, sessionId, requestId: id, message: text });
      if (alive.current) { setEntry({ id, status: 'unknown', receipt: rc }); setText(''); }
    } catch (e) {
      if (alive.current) setEntry({ id, status: e instanceof ControlActionError && e.outcome !== 'unknown' ? 'rejected' : 'unknown' });
    } finally { busy.current = false; }
  };
  const requestStop = async () => {
    if (disabled || stopping.current || stop || !['starting', 'running', 'queued', 'pending'].includes(run.state)) return;
    stopping.current = true;
    const id = newRequestId(); remember(run.id, 'stop', id); setStop({ id, status: 'pending' }); setNote('');
    try { const rc = await actions.stop(run.id, id); if (alive.current) setStop({ id, status: 'unknown', receipt: rc }); }
    catch { if (alive.current) setStop({ id, status: 'unknown' }); }
    finally { stopping.current = false; }
  };
  const requestPause = async () => {
    if(pausing.current || !canSend || !sessionId) return;
    pausing.current=true;
    const id=newRequestId();remember(run.id,'pause',id);setPause({id,status:'pending'});setNote('');
    try {const rc=await actions.pause({runId:run.id,sessionId,requestId:id});if(alive.current)setPause({id,status:'unknown',receipt:rc});}
    catch(e){if(alive.current)setPause({id,status:e instanceof ControlActionError && e.outcome!=='unknown'?'rejected':'unknown'});}
    finally{pausing.current=false;}
  };
  // Lookup is read-only: it never resends a direction, pause or stop request.
  const query = async (e: Entry, target: 'entry'|'stop'|'pause') => {
    if (querying) return;
    const previous = e.receipt;
    setQuerying(true); setLooking(target); setNote('');
    try {
      const rc = await actions.receipt(e.id);
      if (rc.requestId !== e.id || rc.runId !== run.id || previous && (rc.kind !== previous.kind || rc.sessionId !== previous.sessionId)) throw new Error('wrong receipt');
      if (alive.current) {
        (target==='stop'?setStop:target==='pause'?setPause:setEntry)({ id: e.id, status: 'unknown', receipt: rc });
        setNote(!previous ? '查询完成，已获取回执。' : receiptKey(previous) === receiptKey(rc) ? '查询完成，回执无变化。' : '查询完成，回执已更新。');
      }
    } catch { if (alive.current) setNote('暂未查到有效回执。保留原请求，不会自动重发。'); }
    finally { if (alive.current) { setQuerying(false); setLooking(null); } }
  };
  return <section className="intervention" aria-label="Agent 干预">
    <div className="inline-actions"><label className="instruction-label" htmlFor={`instruction-${run.id}`}>指令</label>
      <select aria-label="指令生效时机" value={timing} disabled={!canSend || unresolved(entry)} onChange={e=>setTiming(e.target.value as 'instruction'|'follow_up')}>
        <option value="instruction">当前工具后</option><option value="follow_up">当前轮结束后</option>
      </select>
    </div>
    <textarea id={`instruction-${run.id}`} value={text} rows={2} placeholder="补充要求或纠正方向，例如：先完成接口。请勿填写密钥。" disabled={!canSend || unresolved(entry)} onChange={e => setText(e.target.value)} />
    <div className="inline-actions mt">
      <button className="btn primary" type="button" disabled={!canSend || unresolved(entry) || !text.trim() || tooLong} onClick={() => void send()}>发送指令</button>
      <button className="btn" type="button" disabled={!canSend} onClick={()=>void requestPause()}>暂停并保留进度</button>
      <button className="btn" type="button" disabled={disabled || !!stop || run.stopRequested || !['starting','running','queued','pending'].includes(run.state)} onClick={() => void requestStop()}>停止运行</button>
      <span className="k small">{[...text].length} / 4000</span>
    </div>
    {!canSend ? <p className="k small">{disabled ? '连接或状态未确认，暂不能发送。' : run.stopRequested || stop ? '已请求停止。' : pause ? '已请求暂停，等待核实检查点；不会接受新指令。' : '仅正在开发或精修、且会话身份已确认的 Agent 支持发送指令。'}</p> : null}
    {[{ e: entry, target: 'entry' as const }, { e: pause, target: 'pause' as const }, { e: stop, target: 'stop' as const }].map(({ e, target }) => e ? <div className="control-note" role="status" key={target}>
      <span>{target==='stop'?'停止':target==='pause'?'暂停':e.receipt?.kind==='follow_up'?'后续指令':'指令'} · {looking === target ? '正在查询…' : label(e)}</span>
      <span className="k small">请求 <code>{short(e.id, 12)}</code>{e.receipt ? ` · ${formatTime(e.receipt.updatedAt)}` : ''}</span>
      <button className="btn" type="button" disabled={querying || e.status === 'pending' && !e.receipt} onClick={() => void query(e, target)}>查询回执</button>
    </div> : null)}
    {note ? <p className="local-note" role="status">{note}</p> : null}
    <details className="instruction-help"><summary>指令如何生效</summary>
      <p className="k small">在原任务范围内补充要求或纠正方向。指令会在当前工具调用后交给 Agent，保持同一执行会话。</p>
      <p className="k small">“已排队”或“已接收”不代表已完成；任务结果仍需审查。请勿在指令中填写密钥。</p>
    </details>
  </section>;
}

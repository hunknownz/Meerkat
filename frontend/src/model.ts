import type { Delivery, LegacyActive, Review, Run, Snapshot, Task, TokenCounts } from './generated/workflow';

export type { Snapshot, Run, Task, Delivery, Review, LegacyActive };

export const ROLE_LABEL: Record<string, string> = { developer: '开发', reviewer: '审查', polisher: '精修' };
export const ROLES = ['developer', 'reviewer', 'polisher'] as const;
export const DELIVERY_LABEL: Record<string, string> = { first: '初次交付', final_candidate: '交付候选', delivered: '最终代码交付' };
export const RUN_LABEL: Record<string, string> = {
  queued: '排队中', pending: '排队中', starting: '启动中', running: '运行中', stopping: '停止中', stopped: '已停止',
  succeeded: '已完成', completed: '已完成', failed: '失败', blocked: '受阻', unknown: '未知',
};
export const ACTIVE_RUN = new Set(['queued', 'pending', 'starting', 'running', 'stopping', 'unknown']);

export const roleLabel = (r: string | undefined): string => (r ? ROLE_LABEL[r] ?? r : '未知角色');
export const runLabel = (s: string | undefined): string => (s ? RUN_LABEL[s] ?? s : '未知');

/** Historical Pi-NN IDs display as Agent-NN; custom IDs are shown unchanged. */
export function agentLabel(id: string | undefined): { label: string; legacy: string | null } {
  const raw = (id ?? '').slice(0, 40);
  const m = /^Pi-(\d{2,4})$/.exec(raw);
  return m ? { label: `Agent-${m[1]}`, legacy: raw } : { label: raw, legacy: null };
}

/** Only https URLs are rendered as links. */
export function safeHttpsUrl(url: string | undefined): string | null {
  if (!url) return null;
  try {
    const u = new URL(url);
    return u.protocol === 'https:' && !u.username && !u.password ? u.href : null;
  } catch {
    return null;
  }
}

export const isActiveRun = (r: Run): boolean => ACTIVE_RUN.has(r.state);

/** Independent (legacy) heartbeats whose runId is already a workflow run are dropped (no double count). */
export function dedupLegacy(snapshot: Snapshot | null, legacy: LegacyActive[]): LegacyActive[] {
  const ids = new Set((snapshot?.runs ?? []).map((r) => r.id.toLowerCase()));
  return legacy.filter((a) => !(a.runId && ids.has(a.runId.toLowerCase())));
}

export const lastEvent = (r: Run) => (r.events && r.events.length ? r.events[r.events.length - 1] : undefined);

export const COUNTERS = ['input', 'output', 'cacheRead', 'cacheWrite', 'total'] as const;
export type Counter = (typeof COUNTERS)[number];

/** Unknown or absent counters stay null; never coerced to zero. */
export function runTokens(r: Run): Record<Counter, number | null> {
  const t: TokenCounts = r.usage?.tokens ?? {};
  const out = {} as Record<Counter, number | null>;
  for (const k of COUNTERS) {
    const v = t[k];
    out[k] = typeof v === 'number' && Number.isFinite(v) && v >= 0 ? v : null;
  }
  return out;
}

export interface UsageSummary {
  runs: number;
  known: Record<Counter, number>;
  missing: Record<Counter, number>;
  costUsd: number;
  costRuns: number;
  agentSeconds: number;
  agentTimeUnknown: number;
  wallSeconds: number | null;
}

const parseTime = (s: string | null | undefined): number | null => {
  if (!s) return null;
  const t = Date.parse(s);
  return Number.isFinite(t) ? t : null;
};

/** Sums only reported categories. Agent time = sum of run durations; wall time = first start → last end. */
export function summarizeUsage(runs: Run[], now: number): UsageSummary {
  const s: UsageSummary = {
    runs: runs.length, known: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
    missing: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 }, costUsd: 0, costRuns: 0,
    agentSeconds: 0, agentTimeUnknown: 0, wallSeconds: null,
  };
  let first: number | null = null;
  let last: number | null = null;
  for (const r of runs) {
    const t = runTokens(r);
    for (const k of COUNTERS) {
      const v = t[k];
      if (v === null) s.missing[k] += 1;
      else s.known[k] += v;
    }
    const c = r.usage?.estimatedCostUsd;
    if (typeof c === 'number' && Number.isFinite(c) && c >= 0) { s.costUsd += c; s.costRuns += 1; }
    const a = parseTime(r.startedAt);
    const b = r.endedAt ? parseTime(r.endedAt) : now;
    if (a === null || b === null || b < a) { s.agentTimeUnknown += 1; continue; }
    s.agentSeconds += (b - a) / 1000;
    first = first === null ? a : Math.min(first, a);
    last = last === null ? b : Math.max(last, b);
  }
  if (first !== null && last !== null) s.wallSeconds = (last - first) / 1000;
  return s;
}

export function formatDuration(sec: number | null): string {
  if (sec === null || !Number.isFinite(sec)) return '未知';
  const s = Math.max(0, Math.round(sec));
  if (s < 60) return `${s}秒`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}分${s % 60 ? `${s % 60}秒` : ''}`;
  return `${Math.floor(m / 60)}时${m % 60 ? `${m % 60}分` : ''}`;
}

export function formatTime(s: string | null | undefined): string {
  const t = parseTime(s);
  if (t === null) return '—';
  const d = new Date(t);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

export const num = (n: number): string => n.toLocaleString('en-US');
export const short = (s: string | null | undefined, n = 8): string => (s ? s.slice(0, n) : '');
export const modelLabel = (r: Run): string =>
  [r.modelSnapshot?.provider, r.modelSnapshot?.model].filter(Boolean).join('/') || '模型未记录';

export function taskCategory(t: Task): 'delivered' | 'attention' | 'active' {
  if (t.state === 'delivered') return 'delivered';
  if (/block|fail|stop|unknown|error|attention|needs|timeout|cancel/.test(t.state)) return 'attention';
  return 'active';
}

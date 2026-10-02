#!/usr/bin/env node
// Run one Pi task (developer | reviewer | polisher) in an isolated Git worktree and record a small summary.
// Dependency-free, Node 22. Never prints or stores API keys, raw provider errors or raw transcripts.
import { spawn, spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import {
  appendFileSync, chmodSync, closeSync, constants as fsc, existsSync, fstatSync, lstatSync, mkdirSync,
  openSync, readFileSync, readSync, realpathSync, writeFileSync,
} from 'node:fs';
import { basename, dirname, isAbsolute, join, relative, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { writeActive, removeActive } from '../dashboard/active.mjs';

const PRIVATE_DIR = '.pi-developer';
const PROTECTED_BRANCHES = new Set(['main', 'master', 'develop', 'trunk']);
const TASK_ID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const SAFE_ID_RE = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
const SHA_RE = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const DIGEST_RE = /^[A-Za-z0-9][A-Za-z0-9:._+/=-]{0,199}$/;
export const ROLES = ['developer', 'reviewer', 'polisher'];
const TOOL_TYPES = new Set(['read', 'edit', 'write', 'bash']);
const TOKEN_KEYS = ['input', 'output', 'cacheRead', 'cacheWrite'];
const MAX_LINE = 1024 * 1024; // bytes of one JSON event line kept in memory
const MAX_REPORT = 64 * 1024;

export class UsageError extends Error {}

function git(cwd, args, { allowFail = false } = {}) {
  const r = spawnSync('git', args, { cwd, encoding: 'utf8' });
  if (r.status !== 0 && !allowFail) throw new UsageError(`git ${args.join(' ')} failed: ${(r.stderr || '').trim()}`);
  return r.status === 0 ? r.stdout.trim() : null;
}

export function loadConfig(path) {
  if (!existsSync(path)) throw new UsageError(`config not found: ${path}`);
  const c = JSON.parse(readFileSync(path, 'utf8'));
  for (const k of ['projectId', 'provider', 'model', 'authEnv']) {
    if (typeof c[k] !== 'string' || !c[k]) throw new UsageError(`config.${k} must be a non-empty string`);
  }
  if (!/^[A-Z_][A-Z0-9_]*$/.test(c.authEnv)) throw new UsageError('config.authEnv must be an env var name');
  c.instructions = c.instructions ?? [];
  c.limits = { maxWallSeconds: 1800, maxTokens: 2_000_000, ...(c.limits ?? {}) };
  c.piCommand = c.piCommand ?? ['pi'];
  if (typeof c.piCommand === 'string') c.piCommand = [c.piCommand];
  return c;
}

export function preflight({ config, worktree, taskPath }) {
  if (!existsSync(taskPath)) throw new UsageError(`task not found: ${taskPath}`);
  if (!existsSync(worktree)) throw new UsageError(`worktree not found: ${worktree}`);
  const wt = realpathSync(worktree);
  const top = git(wt, ['rev-parse', '--show-toplevel'], { allowFail: true });
  if (!top || realpathSync(top) !== wt) throw new UsageError('worktree must be a Git worktree root');
  const gitDir = realpathSync(resolve(wt, git(wt, ['rev-parse', '--git-dir'])));
  const commonDir = realpathSync(resolve(wt, git(wt, ['rev-parse', '--git-common-dir'])));
  if (gitDir === commonDir) throw new UsageError('refusing primary worktree; use a linked worktree');
  const branch = git(wt, ['symbolic-ref', '--short', '-q', 'HEAD'], { allowFail: true });
  if (!branch) throw new UsageError('worktree HEAD is detached; check out a task branch');
  if (PROTECTED_BRANCHES.has(branch)) throw new UsageError(`refusing protected branch: ${branch}`);
  if (git(wt, ['status', '--porcelain'])) throw new UsageError('worktree is not clean');
  const missing = config.instructions.filter((p) => !existsSync(resolve(wt, p)));
  if (missing.length) throw new UsageError(`instruction files missing: ${missing.join(', ')}`);
  return { worktree: wt, branch, baseline: git(wt, ['rev-parse', 'HEAD']) };
}

const inside = (parent, child) => { const r = relative(parent, child); return r === '' || (!r.startsWith('..') && !isAbsolute(r)); };

/** Validates a designated report path: absolute, outside the worktree, not preexisting, parent exists. */
export function checkReportPath(reportFile, worktree) {
  if (typeof reportFile !== 'string' || !isAbsolute(reportFile)) throw new UsageError('report file must be an absolute path');
  const parent = dirname(reportFile);
  if (!existsSync(parent)) throw new UsageError('report file directory does not exist');
  const real = join(realpathSync(parent), basename(reportFile));
  if (inside(worktree, real) || inside(worktree, resolve(reportFile))) throw new UsageError('report file must be outside the worktree');
  let exists = true;
  try { lstatSync(real); } catch (e) { if (e.code === 'ENOENT') exists = false; else throw new UsageError('report file not accessible'); }
  if (exists) throw new UsageError('report file already exists; use a new private path');
  return real;
}

function validateOptions({ role, taskId, runId, agentId, candidateSha, contextDigest }) {
  if (!ROLES.includes(role)) throw new UsageError(`invalid role: ${role}`);
  if (taskId !== undefined && !TASK_ID_RE.test(taskId)) throw new UsageError(`invalid --task-id (expected a dashboard task UUID): ${taskId}`);
  if (runId !== undefined && !SAFE_ID_RE.test(runId)) throw new UsageError('invalid run id');
  if (agentId !== undefined && !SAFE_ID_RE.test(agentId)) throw new UsageError('invalid agent id');
  if (candidateSha !== undefined && !SHA_RE.test(candidateSha)) throw new UsageError('invalid candidate sha');
  if (contextDigest !== undefined && !DIGEST_RE.test(contextDigest)) throw new UsageError('invalid context digest');
}

const ROLE_HEADER = {
  developer: [
    'Implement the task below. Run relevant local checks. Finish with one scoped local commit using explicit pathspecs, leaving a clean working tree.',
  ],
  reviewer: [
    'Review the current candidate commit against the task below. You are read-only: do not edit, create or delete files in the worktree, do not commit, reset, stash, or switch branches.',
    'Use read-only inspection and local checks only. Your verdict goes in the role report.',
  ],
  polisher: [
    'Polish the current candidate for the task below: make only small, safe improvements. Run relevant local checks.',
    'If you change anything, finish with one scoped local commit using explicit pathspecs, leaving a clean working tree. If nothing needs changing, leave the tree and HEAD untouched and report decision "no_change".',
  ],
};

export function buildPrompt({ config, worktree, task, role = 'developer', reportFile, candidateSha, contextDigest }) {
  const parts = [
    `You are the ${role} for project "${config.projectId}". Work only in the current Git worktree (${worktree}).`,
    ...ROLE_HEADER[role],
    'Do not push, open PRs, merge, deploy, create branches or worktrees, or touch production systems. Never print secrets.',
    '', '## Task', task.trim(),
  ];
  for (const p of config.instructions) {
    parts.push('', `## Project instructions: ${p}`, readFileSync(resolve(worktree, p), 'utf8').trim());
  }
  if (reportFile) {
    const shaHint = role === 'reviewer'
      ? `the reviewed HEAD${candidateSha ? ` (${candidateSha})` : ''}`
      : 'the final HEAD after your work (`git rev-parse HEAD`)';
    const shape = role === 'reviewer'
      ? '{"candidateSha","contextDigest","verdict":"pass"|"changes_requested","summary","findings":[{"id","summary","path"?,"line"?}],"checks":[{"command","result"}],"knownGaps":[string]}'
      : '{"candidateSha","contextDigest","summary","checks":[{"command","result"}],"knownGaps":[string],"decision":"changed"|"no_change"}';
    parts.push('', '## Role report',
      `Report file: ${reportFile}`,
      `Context digest: ${contextDigest ?? '(none; use null)'}`,
      `When finished, write one JSON object (max ${MAX_REPORT} bytes) to the report file above (outside the worktree; create it as a new regular file, never a symlink).`,
      `Set candidateSha to ${shaHint} and contextDigest to the context digest above. Shape: ${shape}`);
  }
  return parts.join('\n');
}

export function piArgs(config, prompt, role = 'developer') {
  return [
    ...config.piCommand.slice(1),
    '--print', '--mode', 'json', '--no-session', '--no-extensions', '--no-skills', '--no-prompt-templates',
    '--no-context-files', '--tools', role === 'reviewer' ? 'read,bash' : 'read,bash,edit,write',
    '--model', `${config.provider}/${config.model}`, '--', prompt,
  ];
}

const validCount = (v) => typeof v === 'number' && Number.isFinite(v) && v >= 0;

/** Aggregates usage from completed assistant messages; tracks in-flight usage for limit checks. */
export class UsageTracker {
  constructor() {
    this.tokens = { input: null, output: null, cacheRead: null, cacheWrite: null };
    this.cost = 0; this.costTrusted = true; this.messages = 0;
    this.inFlight = 0; this.settled = false; this.badLines = 0; this.invalidFields = 0;
    this.providerError = false; this.lastStopReason = null;
  }
  static total(u) { let t = 0; for (const k of TOKEN_KEYS) if (validCount(u?.[k])) t += u[k]; return t; }
  get total() {
    const known = TOKEN_KEYS.filter((k) => this.tokens[k] !== null);
    return known.length ? known.reduce((a, k) => a + this.tokens[k], 0) : null;
  }
  get liveTotal() { return (this.total ?? 0) + this.inFlight; }
  /** Returns the parsed event (or null) so callers can derive structural events. */
  line(text) {
    if (!text.trim()) return null;
    let ev;
    try { ev = JSON.parse(text); } catch { this.badLines++; return null; }
    if (!ev || typeof ev !== 'object') { this.badLines++; return null; }
    if (ev.type === 'agent_settled') this.settled = true;
    else if (ev.type === 'message_update') {
      const role = ev.message?.role;
      if (role === undefined || role === 'assistant') this.inFlight = UsageTracker.total(ev.message?.usage ?? ev.usage);
    } else if (ev.type === 'message_end' && ev.message?.role === 'assistant') {
      const u = ev.message.usage && typeof ev.message.usage === 'object' ? ev.message.usage : {};
      let sampleTokens = 0;
      for (const k of TOKEN_KEYS) {
        if (validCount(u[k])) { this.tokens[k] = (this.tokens[k] ?? 0) + u[k]; sampleTokens += u[k]; }
        else this.invalidFields++;
      }
      const c = u.cost?.total;
      // A zero cost alongside non-zero tokens means unknown pricing metadata, not a free bill.
      if (validCount(c) && !(c === 0 && sampleTokens > 0)) this.cost += c; else this.costTrusted = false;
      this.lastStopReason = typeof ev.message.stopReason === 'string' ? ev.message.stopReason : null;
      if (this.lastStopReason === 'error') this.providerError = true;
      this.messages++; this.inFlight = 0;
    }
    return ev;
  }
  /** complete | partial | unknown. `stopped` adds stream-level signals (stop/unsettled); in-flight/error/invalid also count. */
  completeness({ stopped = false } = {}) {
    if (this.messages === 0 || this.total === null) return 'unknown';
    if (this.invalidFields || this.badLines || this.inFlight || this.providerError || stopped) return 'partial';
    return 'complete';
  }
  estimatedCost(opts) {
    return this.costTrusted && this.messages > 0 && this.cost > 0 && this.completeness(opts) === 'complete'
      ? Number(this.cost.toFixed(6)) : null;
  }
  usageView(opts) {
    return {
      tokens: { ...this.tokens, total: this.total, assistantMessages: this.messages },
      usageCompleteness: this.completeness(opts),
      estimatedCostUsd: this.estimatedCost(opts),
    };
  }
}

function ensurePrivateDirIgnored(wt) {
  const probe = `${PRIVATE_DIR}/summary.json`;
  if (spawnSync('git', ['check-ignore', '-q', probe], { cwd: wt }).status === 0) return;
  const exclude = resolve(wt, git(wt, ['rev-parse', '--git-path', 'info/exclude']));
  mkdirSync(dirname(exclude), { recursive: true });
  appendFileSync(exclude, `\n/${PRIVATE_DIR}/\n`);
}

/** Dirty paths from porcelain -z (no trimming, so the first entry keeps its first character). */
function statusPaths(wt) {
  const r = spawnSync('git', ['status', '--porcelain', '-z'], { cwd: wt, encoding: 'utf8' });
  if (r.status !== 0) return [];
  const out = []; const parts = r.stdout.split('\0');
  for (let i = 0; i < parts.length; i++) {
    const e = parts[i];
    if (e.length < 4) continue;
    out.push(e.slice(3));
    if (e[0] === 'R' || e[0] === 'C') i++; // skip rename/copy source entry
  }
  return out;
}

function changedPaths(wt, baseline, result) {
  const set = new Set();
  if (result && result !== baseline) for (const p of (git(wt, ['diff', '--name-only', baseline, result], { allowFail: true }) || '').split('\n')) if (p) set.add(p);
  for (const p of statusPaths(wt)) set.add(p);
  return [...set].sort();
}

const safeCall = (fn, arg) => {
  if (typeof fn !== 'function') return;
  try { const r = fn(arg); if (r && typeof r.then === 'function') r.then(null, () => {}); } catch { /* never leak */ }
};

function structuralEvent(ev) {
  const observedAt = new Date().toISOString();
  if (ev.type === 'tool_execution_start') {
    const t = typeof ev.toolName === 'string' ? ev.toolName : ev.tool?.name;
    return { type: 'tool', observedAt, summary: TOOL_TYPES.has(t) ? t : 'other' };
  }
  if (ev.type === 'agent_start') return { type: 'lifecycle', observedAt, summary: 'started' };
  if (ev.type === 'agent_settled') return { type: 'lifecycle', observedAt, summary: 'settled' };
  if (ev.type === 'message_end' && ev.message?.role === 'assistant') return { type: 'lifecycle', observedAt, summary: 'assistant_message' };
  return null;
}

function runPi({ config, worktree, prompt, role, env, activeRecord, dataDir, signal, onStart, onEvent, onUsage }) {
  return new Promise((done) => {
    const tracker = new UsageTracker();
    if (signal?.aborted) { done({ tracker, code: null, signal: null, stopReason: 'aborted', spawnError: null }); return; }
    // Own process group so stop() reaches Pi and anything it spawned, and nothing else.
    const child = spawn(config.piCommand[0], piArgs(config, prompt, role), {
      cwd: worktree, env, stdio: ['ignore', 'pipe', 'pipe'], detached: process.platform !== 'win32',
    });
    let buf = ''; let skipping = false; let stopReason = null; let spawnError = null; let killTimer = null;
    let activeTimer = null; let activeChain = Promise.resolve(); let activeFailed = false; let exited = false;
    const hasActive = Boolean(activeRecord && child && child.pid);
    const kill = (sig) => {
      if (!child.pid) return;
      try { if (process.platform !== 'win32') process.kill(-child.pid, sig); else child.kill(sig); }
      catch { try { child.kill(sig); } catch {} }
    };
    const stop = (reason) => {
      if (stopReason || exited) return;
      stopReason = reason;
      kill('SIGTERM');
      killTimer = setTimeout(() => kill('SIGKILL'), 5000);
    };
    const onAbort = () => stop('aborted');
    signal?.addEventListener('abort', onAbort, { once: true });
    const queueActive = () => {
      activeChain = activeChain.then(async () => {
        if (activeFailed) return;
        try {
          await writeActive({ ...activeRecord, pid: child.pid, updatedAt: new Date().toISOString() }, dataDir);
        } catch {
          activeFailed = true;
          clearInterval(activeTimer);
          stop('active status write failed');
        }
      });
      return activeChain;
    };
    if (child.pid) safeCall(onStart, { pid: child.pid, startedAt: activeRecord?.startedAt ?? new Date().toISOString() });
    if (hasActive) {
      queueActive();
      activeTimer = setInterval(queueActive, 2000);
    }
    const wall = setTimeout(() => stop('wall-time limit exceeded'), config.limits.maxWallSeconds * 1000);
    const handle = (text) => {
      const ev = tracker.line(text);
      if (!ev) return;
      const s = structuralEvent(ev);
      if (s) safeCall(onEvent, s);
      if (ev.type === 'message_end' && ev.message?.role === 'assistant') safeCall(onUsage, tracker.usageView());
      if (tracker.liveTotal > config.limits.maxTokens) stop('token limit exceeded');
    };
    child.stdout.setEncoding('utf8');
    child.stdout.on('data', (chunk) => {
      buf += chunk;
      let i;
      while ((i = buf.indexOf('\n')) >= 0) {
        const lineText = buf.slice(0, i); buf = buf.slice(i + 1);
        if (skipping) { skipping = false; continue; }
        handle(lineText);
      }
      if (buf.length > MAX_LINE) { buf = ''; skipping = true; tracker.badLines++; } // drop oversized line, keep going
    });
    child.stderr.resume(); // discarded: may contain provider diagnostics, never persisted
    child.on('error', (e) => { spawnError = e.code || 'spawn error'; });
    child.on('close', (code, sig) => {
      exited = true;
      clearTimeout(wall); clearTimeout(killTimer);
      signal?.removeEventListener('abort', onAbort);
      if (stopReason) kill('SIGKILL'); // reap leftovers in our own group only
      if (buf && !skipping) handle(buf);
      clearInterval(activeTimer);
      activeChain
        .then(async () => {
          if (hasActive) {
            try { await removeActive(activeRecord.id, dataDir); } catch {}
          }
        })
        .then(() => done({ tracker, code, signal: sig, stopReason, spawnError }));
    });
  });
}

// ---------- report validation ----------
const bStr = (v, max, { empty = false } = {}) => typeof v === 'string' && v.length <= max && (empty || v.trim().length > 0);
const bArr = (v, max) => Array.isArray(v) && v.length <= max;

function checkChecks(v) {
  return bArr(v, 50) && v.every((c) => c && typeof c === 'object' && bStr(c.command, 500) && bStr(c.result, 1000));
}

/** Validates a parsed report object for a role. Returns a sanitized copy or throws a category string. */
export function validateReport(role, r) {
  if (!r || typeof r !== 'object' || Array.isArray(r)) throw 'report_invalid';
  if (!(typeof r.candidateSha === 'string' && SHA_RE.test(r.candidateSha))) throw 'report_invalid';
  if (!(r.contextDigest === null || (typeof r.contextDigest === 'string' && DIGEST_RE.test(r.contextDigest)))) throw 'report_invalid';
  if (!bStr(r.summary, 4000) || !checkChecks(r.checks) || !bArr(r.knownGaps, 50) || !r.knownGaps.every((g) => bStr(g, 1000))) throw 'report_invalid';
  const base = {
    candidateSha: r.candidateSha, contextDigest: r.contextDigest, summary: r.summary,
    checks: r.checks.map((c) => ({ command: c.command, result: c.result })), knownGaps: [...r.knownGaps],
  };
  if (role === 'reviewer') {
    if (!['pass', 'changes_requested'].includes(r.verdict)) throw 'report_invalid';
    if (!bArr(r.findings, 100)) throw 'report_invalid';
    const findings = r.findings.map((f) => {
      if (!f || typeof f !== 'object' || !bStr(f.id, 100) || !bStr(f.summary, 2000)) throw 'report_invalid';
      if (f.path !== undefined && !bStr(f.path, 500)) throw 'report_invalid';
      if (f.line !== undefined && !(Number.isInteger(f.line) && f.line > 0)) throw 'report_invalid';
      return { id: f.id, summary: f.summary, ...(f.path !== undefined ? { path: f.path } : {}), ...(f.line !== undefined ? { line: f.line } : {}) };
    });
    if (r.verdict === 'changes_requested' && findings.length === 0) throw 'report_invalid';
    return { ...base, verdict: r.verdict, findings };
  }
  if (!['changed', 'no_change'].includes(r.decision)) throw 'report_invalid';
  return { ...base, decision: r.decision };
}

/** Reads the report without following symlinks; returns { report } or { category }. */
function readReport(path, role) {
  let fd;
  try {
    const st = lstatSync(path);
    if (!st.isFile()) return { category: 'report_invalid' };
    fd = openSync(path, fsc.O_RDONLY | (fsc.O_NOFOLLOW ?? 0));
  } catch (e) { return { category: e?.code === 'ENOENT' ? 'report_missing' : 'report_invalid' }; }
  try {
    const st = fstatSync(fd);
    if (!st.isFile() || st.size > MAX_REPORT) return { category: 'report_invalid' };
    const b = Buffer.alloc(MAX_REPORT + 1);
    const n = readSync(fd, b, 0, b.length, 0);
    if (n > MAX_REPORT) return { category: 'report_invalid' };
    let obj;
    try { obj = JSON.parse(b.subarray(0, n).toString('utf8')); } catch { return { category: 'report_invalid' }; }
    try { return { report: validateReport(role, obj) }; } catch (c) { return { category: typeof c === 'string' ? c : 'report_invalid' }; }
  } finally { closeSync(fd); }
}

const REASONS = {
  report_missing: 'report missing', report_invalid: 'report invalid', report_stale: 'report does not match candidate or context',
  reviewer_mutation: 'reviewer changed branch, HEAD or working tree', decision_mismatch: 'report decision does not match Git state',
  provider_error: 'provider reported an error',
};

/**
 * Runs Pi once for a role and returns the summary. Throws UsageError on preflight problems.
 * Callbacks are best-effort; their errors are swallowed.
 */
export async function executeRun({
  config, worktree, taskPath, taskId, role = 'developer', reportFile, candidateSha, contextDigest,
  runId, agentId, dataDir, signal, onStart, onEvent, onUsage, env = process.env,
}) {
  validateOptions({ role, taskId, runId, agentId, candidateSha, contextDigest });
  if (role !== 'developer' && !reportFile) throw new UsageError(`role ${role} requires a report file`);
  const pre = preflight({ config, worktree, taskPath });
  if (candidateSha !== undefined && candidateSha !== pre.baseline) throw new UsageError('worktree HEAD does not match candidate sha');
  const report = reportFile ? checkReportPath(reportFile, pre.worktree) : null;
  if (!env[config.authEnv]) throw new UsageError(`missing env var ${config.authEnv}`);
  const probe = spawnSync(config.piCommand[0], [...config.piCommand.slice(1), '--version'], { encoding: 'utf8' });
  if (probe.error || probe.status !== 0) throw new UsageError(`Pi not runnable: ${config.piCommand[0]}`);
  const task = readFileSync(taskPath, 'utf8');
  const prompt = buildPrompt({ config, worktree: pre.worktree, task, role, reportFile: report, candidateSha: candidateSha ?? pre.baseline, contextDigest });
  ensurePrivateDirIgnored(pre.worktree);

  const id = runId ?? randomUUID();
  const startedAt = new Date();
  const activeRecord = {
    id: randomUUID(),
    task: basename(taskPath),
    model: `${config.provider}/${config.model}`,
    projectId: config.projectId,
    worktree: pre.worktree,
    startedAt: startedAt.toISOString(),
    role, runId: id, ...(agentId ? { agentId } : {}), ...(taskId ? { taskId } : {}),
  };
  const r = await runPi({ config, worktree: pre.worktree, prompt, role, env, activeRecord, dataDir, signal, onStart, onEvent, onUsage });
  const endedAt = new Date();
  const result = git(pre.worktree, ['rev-parse', 'HEAD'], { allowFail: true });
  const branchAfter = git(pre.worktree, ['symbolic-ref', '--short', '-q', 'HEAD'], { allowFail: true });
  const clean = git(pre.worktree, ['status', '--porcelain'], { allowFail: true }) === '';
  const committed = !!result && result !== pre.baseline &&
    spawnSync('git', ['merge-base', '--is-ancestor', pre.baseline, result], { cwd: pre.worktree }).status === 0;

  let validated = null; let reportCategory = null;
  if (report) {
    const got = readReport(report, role);
    if (got.report) {
      validated = got.report;
      const ctxOk = contextDigest === undefined ? true : validated.contextDigest === contextDigest;
      if (validated.candidateSha !== result || !ctxOk) { reportCategory = 'report_stale'; }
    } else reportCategory = got.category;
  }

  let reason = null; let errorCategory = null;
  const fail = (cat, msg) => { if (!reason) { errorCategory = cat; reason = msg ?? REASONS[cat]; } };
  if (r.spawnError) fail('spawn_error', `spawn failed: ${String(r.spawnError).slice(0, 40)}`);
  else if (r.stopReason) fail({ aborted: 'aborted', 'token limit exceeded': 'token_limit', 'wall-time limit exceeded': 'wall_timeout' }[r.stopReason] ?? 'stopped', r.stopReason);
  else if (r.tracker.providerError && r.tracker.lastStopReason === 'error') fail('provider_error');
  else if (r.code !== 0) fail('pi_exit', `pi exited ${r.code ?? r.signal}`);
  else if (!r.tracker.settled) fail('not_settled', 'pi did not report agent_settled');
  else if (role === 'reviewer') {
    if (branchAfter !== pre.branch || result !== pre.baseline || !clean) fail('reviewer_mutation');
  } else if (branchAfter !== pre.branch) fail('branch_changed', `branch changed to ${branchAfter}`);
  else if (role === 'polisher' && validated?.decision === 'no_change') {
    if (result !== pre.baseline) fail('decision_mismatch');
    else if (!clean) fail('dirty', 'working tree left dirty');
  } else if (!committed) fail('no_commit', 'no new commit on task branch');
  else if (!clean) fail('dirty', 'working tree left dirty');
  if (!reason && reportCategory) fail(reportCategory);
  if (!reason && validated && role !== 'reviewer' && validated.decision !== (committed ? 'changed' : 'no_change')) fail('decision_mismatch');

  const stopped = Boolean(r.stopReason);
  const usage = r.tracker.usageView({ stopped: stopped || !r.tracker.settled });
  const summary = {
    projectId: config.projectId, model: `${config.provider}/${config.model}`,
    ...(taskId ? { taskId } : {}),
    runId: id, ...(agentId ? { agentId } : {}), role,
    contextDigest: contextDigest ?? null, candidateSha: candidateSha ?? pre.baseline,
    startedAt: startedAt.toISOString(), endedAt: endedAt.toISOString(),
    elapsedSeconds: Number(((endedAt - startedAt) / 1000).toFixed(1)),
    tokens: usage.tokens, usageCompleteness: usage.usageCompleteness,
    estimatedCostUsd: usage.estimatedCostUsd,
    baselineSha: pre.baseline, resultSha: result, branch: pre.branch,
    changedPaths: changedPaths(pre.worktree, pre.baseline, result),
    exitCode: r.code, signal: r.signal, settled: r.tracker.settled, committed, clean,
    report: reportCategory ? null : validated,
    ...(role === 'reviewer' && validated && !reportCategory ? { verdict: validated.verdict } : {}),
    outcome: reason ? (stopped ? 'stopped' : 'failed') : 'success', reason, errorCategory,
  };
  safeCall(onUsage, usage);
  safeCall(onEvent, { type: 'lifecycle', observedAt: endedAt.toISOString(), summary: summary.outcome });
  const dir = join(pre.worktree, PRIVATE_DIR, 'runs');
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  chmodSync(join(pre.worktree, PRIVATE_DIR), 0o700); chmodSync(dir, 0o700);
  const file = join(dir, `${startedAt.toISOString().replace(/[:.]/g, '-')}.json`);
  writeFileSync(file, JSON.stringify(summary, null, 2) + '\n', { mode: 0o600 });
  chmodSync(file, 0o600);
  return { ...summary, summaryFile: file };
}

export async function main(argv = process.argv.slice(2), env = process.env) {
  const { values } = parseArgs({ args: argv, options: {
    config: { type: 'string' }, worktree: { type: 'string' }, task: { type: 'string' },
    'task-id': { type: 'string' }, 'data-dir': { type: 'string' },
    role: { type: 'string' }, 'report-file': { type: 'string' }, 'candidate-sha': { type: 'string' },
    'context-digest': { type: 'string' }, 'run-id': { type: 'string' }, 'agent-id': { type: 'string' },
    'dry-run': { type: 'boolean' },
  } });
  if (!values.config || !values.worktree || !values.task) throw new UsageError('usage: run.mjs --config <project.json> --worktree <path> --task <file> [--task-id <dashboard task UUID>] [--role developer|reviewer|polisher] [--report-file <abs path>] [--candidate-sha <sha>] [--context-digest <d>] [--run-id <id>] [--agent-id <id>] [--dry-run]');
  const taskId = values['task-id'];
  const role = values.role ?? 'developer';
  const opts = {
    taskId, role, reportFile: values['report-file'], candidateSha: values['candidate-sha'],
    contextDigest: values['context-digest'], runId: values['run-id'], agentId: values['agent-id'],
  };
  validateOptions(opts);
  const config = loadConfig(values.config);

  if (values['dry-run']) {
    const pre = preflight({ config, worktree: values.worktree, taskPath: values.task });
    const task = readFileSync(values.task, 'utf8');
    const prompt = buildPrompt({ config, worktree: pre.worktree, task, role, reportFile: opts.reportFile, candidateSha: opts.candidateSha, contextDigest: opts.contextDigest });
    console.log([
      'Pi developer plan (dry run, no API call):',
      `  project:   ${config.projectId}`,
      `  role:      ${role}`,
      `  model:     ${config.provider}/${config.model} (key from $${config.authEnv})`,
      `  worktree:  ${pre.worktree} [${pre.branch} @ ${pre.baseline.slice(0, 12)}]`,
      `  task:      ${resolve(values.task)}`,
      ...(taskId ? [`  taskId:    ${taskId} (dashboard task UUID; recorded in the run summary)`] : []),
      `  context:   ${config.instructions.join(', ') || '(none)'} ; prompt ${prompt.length} chars`,
      `  limits:    ${config.limits.maxWallSeconds}s wall, ${config.limits.maxTokens} tokens`,
      `  summary:   ${join(pre.worktree, PRIVATE_DIR, 'runs')}/<timestamp>.json`,
    ].join('\n'));
    return 0;
  }

  const ac = new AbortController();
  const onSig = () => ac.abort();
  process.once('SIGTERM', onSig); process.once('SIGINT', onSig);
  try {
    const summary = await executeRun({ config, worktree: values.worktree, taskPath: values.task, ...opts, dataDir: values['data-dir'], signal: ac.signal, env });
    console.log(JSON.stringify(summary, null, 2));
    return summary.outcome === 'success' ? 0 : 1;
  } finally {
    process.off('SIGTERM', onSig); process.off('SIGINT', onSig);
  }
}

const isMain = process.argv[1] && realpathSync(process.argv[1]) === realpathSync(new URL(import.meta.url).pathname);
if (isMain) {
  main().then((code) => { process.exitCode = code; }, (e) => {
    console.error(`pi-developer: ${e instanceof UsageError ? e.message : e.stack}`);
    process.exitCode = 2;
  });
}

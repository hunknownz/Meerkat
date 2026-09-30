#!/usr/bin/env node
// Run one Pi developer task in an isolated Git worktree and record a small summary.
// Dependency-free, Node 22. Never prints or stores API keys or raw transcripts.
import { spawn, spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { appendFileSync, chmodSync, existsSync, mkdirSync, readFileSync, realpathSync, writeFileSync } from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { writeActive, removeActive } from '../dashboard/active.mjs';

const PRIVATE_DIR = '.pi-developer';
const PROTECTED_BRANCHES = new Set(['main', 'master', 'develop', 'trunk']);
const TASK_ID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

class UsageError extends Error {}

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

export function buildPrompt({ config, worktree, task }) {
  const parts = [
    `You are the developer for project "${config.projectId}". Work only in the current Git worktree (${worktree}).`,
    'Implement the task below. Run relevant local checks. Finish with one scoped local commit using explicit pathspecs, leaving a clean working tree.',
    'Do not push, open PRs, merge, deploy, create branches or worktrees, or touch production systems. Never print secrets.',
    '', '## Task', task.trim(),
  ];
  for (const p of config.instructions) {
    parts.push('', `## Project instructions: ${p}`, readFileSync(resolve(worktree, p), 'utf8').trim());
  }
  return parts.join('\n');
}

export function piArgs(config, prompt) {
  return [
    ...config.piCommand.slice(1),
    '--print', '--mode', 'json', '--no-session', '--no-extensions', '--no-skills', '--no-prompt-templates',
    '--no-context-files', '--tools', 'read,bash,edit,write',
    '--model', `${config.provider}/${config.model}`, '--', prompt,
  ];
}

/** Aggregates usage from completed assistant messages; tracks in-flight usage for limit checks. */
export class UsageTracker {
  constructor() {
    this.tokens = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
    this.cost = 0; this.costTrusted = true; this.messages = 0;
    this.inFlight = 0; this.settled = false; this.badLines = 0;
  }
  static total(u) { return (u?.input || 0) + (u?.output || 0) + (u?.cacheRead || 0) + (u?.cacheWrite || 0); }
  get total() { return UsageTracker.total(this.tokens); }
  get liveTotal() { return this.total + this.inFlight; }
  line(text) {
    if (!text.trim()) return;
    let ev;
    try { ev = JSON.parse(text); } catch { this.badLines++; return; }
    if (ev.type === 'agent_settled') this.settled = true;
    else if (ev.type === 'message_update') this.inFlight = UsageTracker.total(ev.message?.usage ?? ev.usage);
    else if (ev.type === 'message_end' && ev.message?.role === 'assistant') {
      const u = ev.message.usage ?? {};
      for (const k of Object.keys(this.tokens)) this.tokens[k] += Number(u[k]) || 0;
      const c = u.cost?.total;
      if (Number.isFinite(c) && c >= 0) this.cost += c; else this.costTrusted = false;
      this.messages++; this.inFlight = 0;
    }
  }
  estimatedCost() { return this.costTrusted && this.messages > 0 && this.cost > 0 ? Number(this.cost.toFixed(6)) : null; }
}

function ensurePrivateDirIgnored(wt) {
  const probe = `${PRIVATE_DIR}/summary.json`;
  if (spawnSync('git', ['check-ignore', '-q', probe], { cwd: wt }).status === 0) return;
  const exclude = resolve(wt, git(wt, ['rev-parse', '--git-path', 'info/exclude']));
  mkdirSync(dirname(exclude), { recursive: true });
  appendFileSync(exclude, `\n/${PRIVATE_DIR}/\n`);
}

function changedPaths(wt, baseline, result) {
  const set = new Set();
  if (result && result !== baseline) for (const p of (git(wt, ['diff', '--name-only', baseline, result], { allowFail: true }) || '').split('\n')) if (p) set.add(p);
  for (const l of (git(wt, ['status', '--porcelain'], { allowFail: true }) || '').split('\n')) if (l) set.add(l.slice(3));
  return [...set].sort();
}

function runPi({ config, worktree, prompt, env, activeRecord, dataDir }) {
  return new Promise((done) => {
    const tracker = new UsageTracker();
    const child = spawn(config.piCommand[0], piArgs(config, prompt), { cwd: worktree, env, stdio: ['ignore', 'pipe', 'pipe'] });
    let buf = ''; let stopReason = null; let spawnError = null; let killTimer = null;
    let activeTimer = null;
    let activeChain = Promise.resolve();
    let activeFailed = false;
    const hasActive = Boolean(activeRecord && child && child.pid);
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
    if (hasActive) {
      queueActive();
      activeTimer = setInterval(queueActive, 2000);
    }
    const stop = (reason) => {
      if (stopReason) return;
      stopReason = reason;
      child.kill('SIGTERM');
      killTimer = setTimeout(() => child.kill('SIGKILL'), 5000);
    };
    const wall = setTimeout(() => stop('wall-time limit exceeded'), config.limits.maxWallSeconds * 1000);
    child.stdout.setEncoding('utf8');
    child.stdout.on('data', (chunk) => {
      buf += chunk;
      let i;
      while ((i = buf.indexOf('\n')) >= 0) {
        tracker.line(buf.slice(0, i)); buf = buf.slice(i + 1);
        if (tracker.liveTotal > config.limits.maxTokens) stop('token limit exceeded');
      }
    });
    child.stderr.resume(); // discarded: may contain provider diagnostics, never persisted
    child.on('error', (e) => { spawnError = e.code || 'spawn error'; });
    child.on('close', (code, signal) => {
      clearTimeout(wall); clearTimeout(killTimer);
      if (buf) tracker.line(buf);
      clearInterval(activeTimer);
      activeChain
        .then(async () => {
          if (hasActive) {
            try { await removeActive(activeRecord.id, dataDir); } catch {}
          }
        })
        .then(() => done({ tracker, code, signal, stopReason, spawnError }));
    });
  });
}

export async function main(argv = process.argv.slice(2), env = process.env) {
  const { values } = parseArgs({ args: argv, options: {
    config: { type: 'string' }, worktree: { type: 'string' }, task: { type: 'string' },
    'task-id': { type: 'string' }, 'data-dir': { type: 'string' },
      'dry-run': { type: 'boolean' },
  } });
  if (!values.config || !values.worktree || !values.task) throw new UsageError('usage: run.mjs --config <project.json> --worktree <path> --task <file> [--task-id <dashboard task UUID>] [--dry-run]');
  const taskId = values['task-id'];
  if (taskId !== undefined && !TASK_ID_RE.test(taskId)) throw new UsageError(`invalid --task-id (expected a dashboard task UUID): ${taskId}`);
  const config = loadConfig(values.config);
  const pre = preflight({ config, worktree: values.worktree, taskPath: values.task });
  const task = readFileSync(values.task, 'utf8');
  const prompt = buildPrompt({ config, worktree: pre.worktree, task });

  if (values['dry-run']) {
    console.log([
      'Pi developer plan (dry run, no API call):',
      `  project:   ${config.projectId}`,
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

  if (!env[config.authEnv]) throw new UsageError(`missing env var ${config.authEnv}`);
  const probe = spawnSync(config.piCommand[0], [...config.piCommand.slice(1), '--version'], { encoding: 'utf8' });
  if (probe.error || probe.status !== 0) throw new UsageError(`Pi not runnable: ${config.piCommand[0]}`);
  ensurePrivateDirIgnored(pre.worktree);

  const startedAt = new Date();
  const activeRecord = {
    id: randomUUID(),
    task: basename(values.task),
    model: `${config.provider}/${config.model}`,
    projectId: config.projectId,
    worktree: pre.worktree,
    startedAt: startedAt.toISOString(),
  };
  const r = await runPi({ config, worktree: pre.worktree, prompt, env, activeRecord, dataDir: values['data-dir'] });
  const endedAt = new Date();
  const result = git(pre.worktree, ['rev-parse', 'HEAD'], { allowFail: true });
  const branchAfter = git(pre.worktree, ['symbolic-ref', '--short', '-q', 'HEAD'], { allowFail: true });
  const clean = git(pre.worktree, ['status', '--porcelain'], { allowFail: true }) === '';
  const committed = !!result && result !== pre.baseline &&
    spawnSync('git', ['merge-base', '--is-ancestor', pre.baseline, result], { cwd: pre.worktree }).status === 0;

  let reason = null;
  if (r.spawnError) reason = `spawn failed: ${r.spawnError}`;
  else if (r.stopReason) reason = r.stopReason;
  else if (r.code !== 0) reason = `pi exited ${r.code ?? r.signal}`;
  else if (!r.tracker.settled) reason = 'pi did not report agent_settled';
  else if (branchAfter !== pre.branch) reason = `branch changed to ${branchAfter}`;
  else if (!committed) reason = 'no new commit on task branch';
  else if (!clean) reason = 'working tree left dirty';

  const summary = {
    projectId: config.projectId, model: `${config.provider}/${config.model}`,
    ...(taskId ? { taskId } : {}),
    startedAt: startedAt.toISOString(), endedAt: endedAt.toISOString(),
    elapsedSeconds: Number(((endedAt - startedAt) / 1000).toFixed(1)),
    tokens: { ...r.tracker.tokens, total: r.tracker.total, assistantMessages: r.tracker.messages },
    estimatedCostUsd: r.tracker.estimatedCost(),
    baselineSha: pre.baseline, resultSha: result, branch: pre.branch,
    changedPaths: changedPaths(pre.worktree, pre.baseline, result),
    exitCode: r.code, signal: r.signal, settled: r.tracker.settled, committed, clean,
    outcome: reason ? (r.stopReason ? 'stopped' : 'failed') : 'success', reason,
  };
  const dir = join(pre.worktree, PRIVATE_DIR, 'runs');
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  chmodSync(join(pre.worktree, PRIVATE_DIR), 0o700); chmodSync(dir, 0o700);
  const file = join(dir, `${startedAt.toISOString().replace(/[:.]/g, '-')}.json`);
  writeFileSync(file, JSON.stringify(summary, null, 2) + '\n', { mode: 0o600 });
  chmodSync(file, 0o600);
  console.log(JSON.stringify({ ...summary, summaryFile: file }, null, 2));
  return summary.outcome === 'success' ? 0 : 1;
}

const isMain = process.argv[1] && realpathSync(process.argv[1]) === realpathSync(new URL(import.meta.url).pathname);
if (isMain) {
  main().then((code) => { process.exitCode = code; }, (e) => {
    console.error(`pi-developer: ${e instanceof UsageError ? e.message : e.stack}`);
    process.exitCode = 2;
  });
}

#!/usr/bin/env node
// Meerkat trusted local workflow CLI. Node 22 built-ins only. Prints small JSON; never prints secrets.
//
//   prepare  --input <json> [--data-dir <dir>]                     register a ready task (repo/worktree/config paths
//                                                                  are accepted ONLY here, never from the dashboard)
//   execute  --task <uuid> [--task <uuid> ...] [--resume] [--acknowledge-interruption] [--data-dir <dir>]
//   snapshot [--data-dir <dir>]
//   stop     --run <uuid> --request-id <uuid> [--data-dir <dir>]
//   settings --input <json> [--data-dir <dir>]
//
// The data dir defaults to the dashboard's (~/.codex-pi-developer/dashboard) and must be private (0700).
// Starting work is only possible through `execute` here; there is no HTTP start route.
//
// Resume and interruption safety:
//   * `execute --resume` continues failed/stopped tasks from the failed role only if the worktree is clean, on the
//     task branch, and HEAD equals the recorded candidate (or frozen baseline). Dirty or diverged work is preserved
//     and the command refuses.
//   * A run left by a controller that died is `unknown`. It is never replayed automatically. To continue, confirm the
//     old process is gone and pass both `--resume --acknowledge-interruption`; the command still refuses if a recorded
//     process id appears alive (liveness probe only; stale PIDs are never signaled) or the workspace does not match.
//   * A changed role config requires preparing a new task.
import { lstatSync, mkdirSync, readFileSync, statSync, realpathSync } from 'node:fs';
import { resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { defaultDataDir } from '../dashboard/store.mjs';
import { executeTasks, prepareTask, readWorkflow, requestStop, updateSettings, WorkflowInputError } from '../workflow/core.mjs';
import { ControllerBusyError, StoreError } from '../workflow/store.mjs';

const MAX_INPUT = 1024 * 1024;
const USAGE = 'usage: flow.mjs prepare --input <json> | execute --task <uuid>... [--resume] [--acknowledge-interruption] | snapshot | stop --run <uuid> --request-id <uuid> | settings --input <json>  [--data-dir <dir>]';

export class CliError extends Error {}

/** Resolves the data dir to an absolute private directory (created 0700 if missing; refuses group/world access). */
export function resolveDataDir(value) {
  const dir = resolve(value ?? defaultDataDir());
  let st;
  try { st = lstatSync(dir); } catch (e) {
    if (e.code !== 'ENOENT') throw new CliError('data dir is not accessible');
    mkdirSync(dir, { recursive: true, mode: 0o700 });
    st = lstatSync(dir);
  }
  if (st.isSymbolicLink() || !st.isDirectory()) throw new CliError('data dir must be a real directory');
  if ((st.mode & 0o077) !== 0) throw new CliError('data dir must be private (chmod 700)');
  if (typeof process.getuid === 'function' && st.uid !== process.getuid()) throw new CliError('data dir must be owned by the current user');
  return dir;
}

function readInput(path) {
  if (!path) throw new CliError('--input is required');
  const abs = resolve(path);
  let st;
  try { st = statSync(abs); } catch { throw new CliError('input file not found'); }
  if (!st.isFile() || st.size > MAX_INPUT) throw new CliError('input must be a regular JSON file up to 1 MiB');
  try { return JSON.parse(readFileSync(realpathSync(abs), 'utf8')); } catch { throw new CliError('input is not valid JSON'); }
}

const COMMANDS = {
  prepare: { input: { type: 'string' } },
  execute: { task: { type: 'string', multiple: true }, resume: { type: 'boolean' }, 'acknowledge-interruption': { type: 'boolean' } },
  snapshot: {},
  stop: { run: { type: 'string' }, 'request-id': { type: 'string' } },
  settings: { input: { type: 'string' } },
};

export async function main(argv = process.argv.slice(2), { out = (o) => console.log(JSON.stringify(o)), env = process.env } = {}) {
  const [cmd, ...rest] = argv;
  if (!COMMANDS[cmd]) throw new CliError(USAGE);
  const { values } = parseArgs({ args: rest, options: { ...COMMANDS[cmd], 'data-dir': { type: 'string' } }, strict: true, allowPositionals: false });
  const dataDir = resolveDataDir(values['data-dir']);
  if (cmd === 'prepare') {
    const t = await prepareTask(dataDir, readInput(values.input));
    out({ taskId: t.id, projectId: t.projectId, state: t.state, contextRef: t.contextRef });
    return 0;
  }
  if (cmd === 'execute') {
    if (!values.task?.length) throw new CliError('at least one --task <uuid> is required');
    const r = await executeTasks({
      dataDir, taskIds: values.task, resume: Boolean(values.resume),
      acknowledgeInterruption: Boolean(values['acknowledge-interruption']), handleSignals: true, env,
    });
    out(r);
    return !r.fatal && r.tasks.every((t) => t.state === 'delivered') ? 0 : 1;
  }
  if (cmd === 'snapshot') {
    const s = await readWorkflow(dataDir);
    out({
      observedAt: s.observedAt, controller: s.controller, counts: s.counts, settings: s.settings,
      tasks: s.tasks.map((t) => ({ id: t.id, projectId: t.projectId, title: t.title, state: t.state, stateReason: t.stateReason ?? null,
        candidateSha: t.candidateSha ?? null, resumeRole: t.resumeRole ?? null, usage: t.usage })),
      runs: s.runs.map((r) => ({ id: r.id, taskId: r.taskId, role: r.role, agentId: r.agentId, state: r.state, modelSnapshot: r.modelSnapshot,
        startedAt: r.startedAt, endedAt: r.endedAt ?? null })),
      deliveries: s.deliveries.map((d) => ({ id: d.id, taskId: d.taskId, state: d.state, candidateSha: d.candidateSha })),
    });
    return 0;
  }
  if (cmd === 'stop') {
    out(await requestStop(dataDir, values.run, values['request-id']));
    return 0;
  }
  out(await updateSettings(dataDir, readInput(values.input)));
  return 0;
}

const isMain = process.argv[1] && realpathSync(process.argv[1]) === realpathSync(new URL(import.meta.url).pathname);
if (isMain) {
  main().then((code) => { process.exitCode = code; }, (e) => {
    const known = e instanceof CliError || e instanceof WorkflowInputError || e instanceof StoreError || e instanceof ControllerBusyError
      || e?.code === 'ERR_PARSE_ARGS_UNKNOWN_OPTION' || e?.code === 'ERR_PARSE_ARGS_INVALID_OPTION_VALUE' || e?.code === 'ERR_PARSE_ARGS_UNEXPECTED_POSITIONAL';
    console.error(JSON.stringify({ error: known ? String(e.message).slice(0, 500) : 'internal error' }));
    process.exitCode = 2;
  });
}

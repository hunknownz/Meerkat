#!/usr/bin/env node
// Meerkat standalone Issue CLI (trusted local caller only). Node 22 built-ins only. Uses existing `gh` auth.
// Prints small JSON metadata only: never Issue bodies/comments, tokens or provider details.
//
//   read   --url <issue-url> --output <private-file> [--allow-host <host>...]
//          Reads the Issue via `gh issue view` and writes a bounded, hashed, UNTRUSTED source snapshot (0600)
//          to a file outside any Git worktree. Stdout: { output, snapshot, issueRef } (issueRef fits `prepare`).
//   update --task <uuid> [--data-dir <dir>] [--apply] [--allow-host <host>...]
//          Prepares (or shows) the local pending Markdown update for the task's latest delivery. Nothing is sent
//          unless --apply is given explicitly; --apply posts at most once per delivery (marker-deduplicated).
//
// Aliases: issue-read, issue-update. Enterprise hosts must be allowed explicitly with --allow-host.
import { lstatSync, mkdirSync, realpathSync } from 'node:fs';
import { resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { defaultDataDir } from '../dashboard/store.mjs';
import {
  applyIssueUpdate, DEFAULT_ALLOWED_HOSTS, IssueError, normalizeHosts, prepareIssueUpdate, readIssue, writeIssueSource,
} from '../workflow/issues.mjs';
import { StoreError } from '../workflow/store.mjs';

const USAGE = 'usage: issues.mjs read --url <issue-url> --output <private-file> | update --task <uuid> [--data-dir <dir>] [--apply]  [--allow-host <host>...]';

export class CliError extends Error {}

const COMMANDS = {
  read: { url: { type: 'string' }, output: { type: 'string' } },
  update: { task: { type: 'string' }, 'data-dir': { type: 'string' }, apply: { type: 'boolean' } },
};
const ALIASES = { 'issue-read': 'read', 'issue-update': 'update' };

function privateDataDir(value) {
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

export async function main(argv = process.argv.slice(2), { out = (o) => console.log(JSON.stringify(o)), execFile, readWorkflow } = {}) {
  const [raw, ...rest] = argv;
  const cmd = ALIASES[raw] ?? raw;
  if (!COMMANDS[cmd]) throw new CliError(USAGE);
  const { values } = parseArgs({
    args: rest, options: { ...COMMANDS[cmd], 'allow-host': { type: 'string', multiple: true } }, strict: true, allowPositionals: false,
  });
  const allowedHosts = normalizeHosts([...DEFAULT_ALLOWED_HOSTS, ...(values['allow-host'] ?? [])]);
  const io = { allowedHosts, ...(execFile ? { execFile } : {}), ...(readWorkflow ? { readWorkflow } : {}) };
  if (cmd === 'read') {
    if (!values.url) throw new CliError('--url is required');
    if (!values.output) throw new CliError('--output <private-file> is required');
    const r = await readIssue(values.url, io);
    const output = writeIssueSource(values.output, r);
    out({ output, snapshot: r.snapshot, issueRef: r.issueRef, untrusted: true });
    return 0;
  }
  if (!values.task) throw new CliError('--task <uuid> is required');
  const dataDir = privateDataDir(values['data-dir']);
  if (!values.apply) {
    const p = await prepareIssueUpdate(dataDir, values.task, io);
    out({ ...p, sent: false, next: p.state === 'posted' ? 'already posted' : p.issueUrl ? 'review bodyFile, then rerun with --apply to post' : 'local task: no issueRef, nothing to post' });
    return 0;
  }
  const r = await applyIssueUpdate(dataDir, values.task, io);
  out(r);
  return r.state === 'posted' ? 0 : 1;
}

const isMain = process.argv[1] && realpathSync(process.argv[1]) === realpathSync(new URL(import.meta.url).pathname);
if (isMain) {
  main().then((code) => { process.exitCode = code; }, (e) => {
    const known = e instanceof CliError || e instanceof IssueError || e instanceof StoreError
      || String(e?.code ?? '').startsWith('ERR_PARSE_ARGS_');
    console.error(JSON.stringify({ error: known ? String(e.message).slice(0, 500) : 'internal error', ...(e?.category ? { category: e.category } : {}) }));
    process.exitCode = 2;
  });
}

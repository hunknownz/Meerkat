import { randomUUID } from "node:crypto";
import { chmod, lstat, mkdir, readdir, readFile, rename, unlink, writeFile } from "node:fs/promises";
import path from "node:path";
import { defaultDataDir } from "./store.mjs";

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const STALE_MS = 8000;
const PUBLIC = ["id", "task", "model", "projectId", "worktree", "startedAt", "updatedAt"];

const activeDir = (dataDir) => path.join(dataDir ?? defaultDataDir(), "active");
const isStr = (v) => typeof v === "string";
const isIso = (v) => isStr(v) && !Number.isNaN(Date.parse(v)) && new Date(v).toISOString() === v;
const oneLine = (v) => String(v ?? "").replace(/[\r\n\t]+/g, " ").replace(/\s+/g, " ").trim().slice(0, 160);

function sanitize(record) {
  const r = record ?? {};
  if (!isStr(r.id) || !UUID_RE.test(r.id)) throw new Error("active: invalid id");
  if (!Number.isInteger(r.pid) || r.pid <= 0) throw new Error("active: invalid pid");
  const now = new Date().toISOString();
  return {
    id: r.id.toLowerCase(),
    task: oneLine(r.task),
    model: String(r.model ?? ""),
    projectId: String(r.projectId ?? ""),
    worktree: String(r.worktree ?? ""),
    pid: r.pid,
    startedAt: isIso(r.startedAt) ? r.startedAt : now,
    updatedAt: isIso(r.updatedAt) ? r.updatedAt : now,
  };
}

function valid(r, id) {
  return r && typeof r === "object" && r.id === id && isStr(r.task) && !/[\r\n]/.test(r.task) &&
    r.task.length <= 160 && isStr(r.model) && isStr(r.projectId) && isStr(r.worktree) &&
    Number.isInteger(r.pid) && r.pid > 0 && isIso(r.startedAt) && isIso(r.updatedAt);
}

function alive(pid) {
  try { process.kill(pid, 0); return true; } catch (e) { return e?.code === "EPERM"; }
}

export async function writeActive(record, dataDir) {
  const rec = sanitize(record);
  const root = dataDir ?? defaultDataDir();
  const dir = activeDir(root);
  for (const d of [root, dir]) { await mkdir(d, { recursive: true, mode: 0o700 }); await chmod(d, 0o700); }
  const file = path.join(dir, `${rec.id}.json`);
  const tmp = path.join(dir, `.${rec.id}.${randomUUID()}.tmp`);
  try {
    await writeFile(tmp, JSON.stringify(rec), { mode: 0o600, flag: "wx" });
    await chmod(tmp, 0o600);
    await rename(tmp, file);
  } catch (e) {
    await unlink(tmp).catch(() => {});
    throw e;
  }
  return rec;
}

export async function removeActive(id, dataDir) {
  if (!isStr(id) || !UUID_RE.test(id)) return;
  try { await unlink(path.join(activeDir(dataDir), `${id.toLowerCase()}.json`)); }
  catch (e) { if (e?.code !== "ENOENT") throw e; }
}

export async function listActive(dataDir) {
  const dir = activeDir(dataDir);
  let names;
  try { names = await readdir(dir); } catch { return []; }
  const now = Date.now();
  const out = [];
  for (const name of names) {
    const m = /^(.+)\.json$/.exec(name);
    if (!m || !UUID_RE.test(m[1])) continue;
    try {
      const file = path.join(dir, name);
      if (!(await lstat(file)).isFile()) continue;
      const r = JSON.parse(await readFile(file, "utf8"));
      if (!valid(r, m[1])) continue;
      if (now - Date.parse(r.updatedAt) > STALE_MS || !alive(r.pid)) continue;
      out.push(Object.fromEntries(PUBLIC.map((k) => [k, r[k]])));
    } catch { /* skip */ }
  }
  return out.sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt));
}

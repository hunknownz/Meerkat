#!/usr/bin/env node
// Write a private Pi executor profile that stores only provider/model/authEnv references, never a key.
//
// Usage: node scripts/configure.mjs --project-id SLUG --provider ID --model ID --auth-env ENV
//          [--data-dir DIR] [--base-url URL] [--api openai-completions] [--pi-command ABS]
// Writes <data-dir>/profiles/<slug>.json (default data dir ~/.meerkat). With --base-url/--api it also writes an
// isolated Pi agent directory <data-dir>/pi/<slug>/models.json whose apiKey is the literal "${ENV}" reference.
// The API key itself stays in the inherited service environment; this script never reads it.
import { ensurePrivateDir, privatePath, protectNewFile } from './lib/private.mjs';
import { managedPiCommand } from './lib/executor-install.mjs';
import { accessSync, chmodSync, closeSync, constants, lstatSync, mkdirSync, openSync, realpathSync, statSync, writeSync } from 'node:fs';
import { homedir } from 'node:os';
import { isAbsolute, join, normalize, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const SLUG = /^[a-z0-9][a-z0-9-]{0,62}$/;
const PROVIDER = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;
const MODEL = /^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,199}$/;
const ENV_NAME = /^[A-Z_][A-Z0-9_]{0,127}$/;
const APIS = ['openai-completions'];
const CREDENTIAL = /(sk-|ghp_|gho_|github_pat_|AKIA|xox[abprs]-|AIza|bearer)/i;
const FLAGS = { '--project-id': 'projectId', '--provider': 'provider', '--model': 'model', '--auth-env': 'authEnv',
  '--data-dir': 'dataDir', '--base-url': 'baseUrl', '--api': 'api', '--pi-command': 'piCommand' };

export function parseArgs(argv) {
  const o = {};
  for (let i = 0; i < argv.length; i++) {
    const key = FLAGS[argv[i]];
    if (!key || argv[i + 1] === undefined || o[key] !== undefined) throw new Error(`unknown, repeated or incomplete argument: ${String(argv[i]).slice(0, 40)}`);
    o[key] = argv[++i];
  }
  return o;
}

export function validate(o, home = homedir()) {
  if (!SLUG.test(o.projectId ?? '')) throw new Error('--project-id must be a lowercase slug (max 63)');
  if (!PROVIDER.test(o.provider ?? '') || CREDENTIAL.test(o.provider)) throw new Error('--provider must be a plain name');
  if (!MODEL.test(o.model ?? '') || CREDENTIAL.test(o.model)) throw new Error('--model must be a plain model id');
  if (!ENV_NAME.test(o.authEnv ?? '') || CREDENTIAL.test(o.authEnv)) throw new Error('--auth-env must be an environment variable NAME (e.g. MY_PROVIDER_KEY), not a key');
  const dataDir = o.dataDir ?? join(home, '.meerkat');
  if (!isAbsolute(dataDir) || normalize(dataDir) !== resolve(dataDir) || dataDir.includes('\0')) throw new Error('--data-dir must be a clean absolute path');
  if ((o.baseUrl === undefined) !== (o.api === undefined)) throw new Error('a custom endpoint needs both --base-url and --api');
  let baseUrl;
  if (o.baseUrl !== undefined) {
    let u;
    try { u = new URL(o.baseUrl); } catch { throw new Error('--base-url is not a valid URL'); }
    const localHttp = u.protocol === 'http:' && ['127.0.0.1', '[::1]'].includes(u.hostname);
    if ((u.protocol !== 'https:' && !localHttp) || u.username || u.password || u.search || u.hash || o.baseUrl.includes('?') || o.baseUrl.includes('#') ||
      o.baseUrl.length > 2048 || CREDENTIAL.test(o.baseUrl)) {
      throw new Error('--base-url must be HTTPS or HTTP on 127.0.0.1/::1, without credentials, query or fragment');
    }
    if (!APIS.includes(o.api)) throw new Error(`--api must be one of ${APIS.join(', ')}`);
    baseUrl = u.href;
  }
  let pi = 'pi';
  if (o.piCommand !== undefined && o.piCommand !== 'pi') {
    pi = o.piCommand;
    if (!isAbsolute(pi) || normalize(pi) !== pi || CREDENTIAL.test(pi)) throw new Error('--pi-command must be an absolute executable path or "pi"');
    try {
      if (!statSync(pi).isFile()) throw new Error();
      accessSync(pi, constants.X_OK);
    } catch { throw new Error('--pi-command is not an executable file'); }
  }
  return { ...o, dataDir: resolve(dataDir), baseUrl, pi };
}

// Existing directories must be plain directories owned by the user; private ones must also be 0700.
function ensureDir(dir, strict) { if (strict) ensurePrivateDir(dir); else { try {privatePath(dir,{directory:true,strict:false});} catch(e){if(e.code!=='ENOENT')throw e;ensurePrivateDir(dir);} } }

function absent(file) {
  try { lstatSync(file); } catch (err) { if (err.code === 'ENOENT') return; throw err; }
  throw new Error(`${file} already exists; refusing to overwrite`);
}

function writePrivate(file, value) {
  const fd = openSync(file, 'wx', 0o600);
  try { writeSync(fd, `${JSON.stringify(value, null, 2)}\n`); } finally { closeSync(fd); }
  protectNewFile(file, 0o600);
}

export function configure(opts) {
  const c = validate(opts);
  const profilesDir = join(c.dataDir, 'profiles');
  const profile = join(profilesDir, `${c.projectId}.json`);
  const piDir = join(c.dataDir, 'pi', c.projectId);
  const models = join(piDir, 'models.json');
  ensureDir(c.dataDir, false);
  ensureDir(profilesDir, true);
  absent(profile);
  let piCommand = [...(opts.piCommand ? [c.pi] : managedPiCommand()), '--thinking', 'low'];
  if (c.baseUrl) {
    ensureDir(join(c.dataDir, 'pi'), true);
    ensureDir(piDir, true);
    absent(models);
    writePrivate(models, { providers: { [c.provider]: {
      baseUrl: c.baseUrl, api: c.api, apiKey: `\${${c.authEnv}}`,
      models: [{ id: c.model, input: ['text'], contextWindow: 200000, maxTokens: 16384 }],
    } } });
    piCommand = ['env', `PI_CODING_AGENT_DIR=${piDir}`, ...piCommand];
  }
  writePrivate(profile, {
    projectId: c.projectId, executor: 'pi', provider: c.provider, model: c.model, authEnv: c.authEnv,
    piCommand, instructions: [], limits: { maxTokens: 500000, maxWallSeconds: 600 },
  });
  return { profile, authEnv: c.authEnv };
}

if (process.argv[1] && realpathSync(process.argv[1]) === realpathSync(fileURLToPath(import.meta.url))) {
  try {
    const r = configure(parseArgs(process.argv.slice(2)));
    process.stdout.write(`meerkat configure: wrote ${r.profile}\nset ${r.authEnv} in the Meerkat service environment (value is never stored)\n`);
  } catch (err) {
    process.stderr.write(`meerkat configure: ${err.message}\n`);
    process.exitCode = 1;
  }
}

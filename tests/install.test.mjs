import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, rmSync, writeFileSync, mkdirSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { options, hostPackage } from '../scripts/install.mjs';
import { selectTarget, installedPath } from '../scripts/lib/go-cli.mjs';
import { ensurePrivateDir, privatePath, protectNewFile } from '../scripts/lib/private.mjs';

test('native Windows targets and executable names are explicit', () => {
  assert.deepEqual(selectTarget('win32', 'x64'), { os: 'windows', arch: 'amd64' });
  assert.deepEqual(selectTarget('win32', 'arm64'), { os: 'windows', arch: 'arm64' });
  assert.ok(installedPath('runtime', '0.4.0-beta.16', selectTarget('win32', 'x64')).endsWith('meerkat.exe'));
  assert.equal(selectTarget('freebsd', 'x64'), null);
});
test('install switches do not implicitly configure or execute a task', () => {
  const o=options(['--source','--no-host','--no-start','--no-executor']);
  assert.equal(o.source,true);assert.equal(o.host,false);assert.equal(o.start,false);assert.equal(o.executor,false);
  assert.throws(()=>options(['--apply']),/unknown/);
});
test('bootstrap private directories and files use the platform authority', () => {
  const base=mkdtempSync(join(tmpdir(),'mk-install-'));const dir=join(base,'private');
  try {ensurePrivateDir(dir);ensurePrivateDir(dir);const f=join(dir,'receipt.json');writeFileSync(f,'{}',{mode:0o600});protectNewFile(f);privatePath(f);privatePath(dir,{directory:true});}
  finally{rmSync(base,{recursive:true,force:true});}
});

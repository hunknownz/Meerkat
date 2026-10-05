import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, rmSync, writeFileSync, mkdirSync, readFileSync, symlinkSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { options, hostPackage } from '../scripts/install.mjs';
import { selectTarget, installedPath } from '../scripts/lib/go-cli.mjs';
import { ensurePrivateDir, privatePath, protectNewFile } from '../scripts/lib/private.mjs';

test('native Windows targets and executable names are explicit', () => {
  assert.deepEqual(selectTarget('win32', 'x64'), { os: 'windows', arch: 'amd64' });
  assert.deepEqual(selectTarget('win32', 'arm64'), { os: 'windows', arch: 'arm64' });
  assert.ok(installedPath('runtime', '0.4.0-beta.17', selectTarget('win32', 'x64')).endsWith('meerkat.exe'));
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
test('installer entry executes through a linked source path', () => {
  const base=mkdtempSync(join(tmpdir(),'mk-entry-')),link=join(base,'source');
  try {
    symlinkSync(fileURLToPath(new URL('..',import.meta.url)),link,process.platform==='win32'?'junction':'dir');
    const result=spawnSync(process.execPath,[join(link,'scripts','install.mjs'),'--apply'],{encoding:'utf8',shell:false});
    assert.equal(result.status,1);assert.match(result.stderr,/unknown or incomplete option/);
  } finally {rmSync(base,{recursive:true,force:true});}
});

test('installer modules can be imported by an Agent stdin script', () => {
  const url=new URL('../scripts/install.mjs',import.meta.url).href;
  const result=spawnSync(process.execPath,['--input-type=module','-'],{
    input:`await import(${JSON.stringify(url)});`,encoding:'utf8',shell:false
  });
  assert.equal(result.status,0,result.stderr);
  assert.equal(result.stdout,'');
});

#!/usr/bin/env node
// Disposable native install -> service -> IPC snapshot -> MCP resource smoke.
// Pi is version-probed only; model credentials and the user's state are absent.
import { spawn, execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, delimiter, resolve } from 'node:path';
import { installAll, options, hostPackage } from './install.mjs';
import { ensurePrivateDir } from './lib/private.mjs';
import { configure } from './configure.mjs';
import { PI_VERSION, installExecutor } from './lib/executor-install.mjs';
import { installationTask } from './lib/installation-task.mjs';
const base=mkdtempSync(join(process.platform==='darwin'?'/tmp':tmpdir(),'mk-i-'));
let service, mcp;
try {
  const runtime=join(base,'runtime'),dataDir=join(base,'state with spaces # %');
  const o=options(['--source','--no-host','--no-executor','--no-start','--runtime-dir',runtime,'--data-dir',dataDir]);
  const installed=await installAll(o),bin=installed.runtime.path;
  const version=execFileSync(bin,['version'],{encoding:'utf8'}).trim();
  if(version!=='0.4.0-beta.16')throw Error('wrong binary version');
  ensurePrivateDir(dataDir);
  service=spawn(bin,['serve','--data-dir',dataDir,'--port','0'],{env:{...process.env,LOCAL_INSTALL_KEY:'local-fixture'},stdio:['ignore','pipe','pipe'],shell:false});
  let output='',errors='',ready;service.stdout.on('data',b=>output+=b);service.stderr.on('data',b=>errors+=b);
  for(let i=0;i<200&&!ready;i++){if(service.exitCode!==null)throw Error(`service failed: ${errors}`);try{ready=JSON.parse(output);}catch{};if(!ready)await new Promise(r=>setTimeout(r,50));}
  if(!ready)throw Error('service readiness timed out');
  const snapshot=JSON.parse(execFileSync(bin,['snapshot','--data-dir',dataDir],{encoding:'utf8',timeout:15000}));
  if(!ready.ok||!snapshot.ok)throw Error('snapshot did not respond');
  const doctor=JSON.parse(execFileSync(bin,['doctor','--data-dir',dataDir],{encoding:'utf8',timeout:15000}));
  if(doctor.data.serviceVersion!==version)throw Error('health did not confirm version');
  const reused=await installAll({...o,start:true,artifactDir:join(o.root,'.dist','releases',version)});
  if(reused.service.state!=='reused')throw Error('second install created another controller');
  const pack=hostPackage(o,bin),cfg=JSON.parse(readFileSync(join(pack.path,'.mcp.json'),'utf8')).mcpServers.meerkat;
  if(cfg.command!==process.execPath||cfg.env.MEERKAT_BIN!==bin)throw Error('host runtime not pinned');
  mcp=spawn(cfg.command,cfg.args,{cwd:pack.path,env:{...process.env,...cfg.env},stdio:['pipe','pipe','pipe'],shell:false});
  let raw='';const replies=new Map();mcp.stdout.on('data',b=>{raw+=b;let i;while((i=raw.indexOf('\n'))>=0){const line=raw.slice(0,i);raw=raw.slice(i+1);try{const v=JSON.parse(line);if(v.id)replies.set(v.id,v);}catch{}}});
  const request=async(id,method,params={})=>{mcp.stdin.write(JSON.stringify({jsonrpc:'2.0',id,method,params})+'\n');for(let i=0;i<300&&!replies.has(id);i++)await new Promise(r=>setTimeout(r,20));const r=replies.get(id);if(!r||r.error)throw Error(`MCP ${method} failed`);return r.result;};
  await request(1,'initialize',{protocolVersion:'2025-06-18',capabilities:{},clientInfo:{name:'native-install-smoke',version:'1'}});
  mcp.stdin.write(JSON.stringify({jsonrpc:'2.0',method:'notifications/initialized'})+'\n');
  const tools=await request(2,'tools/list');if(!tools.tools.some(t=>t.name==='open_monitor'))throw Error('monitor tool missing');
  const opened=await request(3,'tools/call',{name:'open_monitor',arguments:{}});if(opened.isError)throw Error('monitor tool failed');
  const resources=await request(4,'resources/list');const uri=resources.resources.find(r=>r.uri.startsWith('ui://'))?.uri;if(!uri)throw Error('UI resource missing');
  const resource=await request(5,'resources/read',{uri});if(!resource.contents.some(c=>c.text?.includes('Agents')||c.text?.includes('<html')))throw Error('UI content missing');
  const executor=installExecutor(join(base,'executor'));if(executor.version!==PI_VERSION)throw Error('wrong Pi version');
  const configured=configure({projectId:'smoke',provider:'example',model:'text',authEnv:'EXAMPLE_MODEL_KEY',baseUrl:'https://example.invalid/v1',api:'openai-completions',dataDir,piCommand:executor.command[0],piCLI:executor.command[1]});
  const probed=JSON.parse(execFileSync(bin,['doctor','--data-dir',dataDir,'--profile',configured.profile,'--probe-executor'],{encoding:'utf8',timeout:15000}));
  if(!probed.ok||!probed.data.checks.some(c=>c.id==='executor.version'&&c.status==='ok'))throw Error('configured executor probe failed');
  const fixture=await installationTask(bin,base,dataDir,executor);
  const refreshed=await request(6,'tools/call',{name:'open_monitor',arguments:{}});
  if(refreshed.structuredContent?.counts.runs!==1||refreshed.structuredContent?.counts.deliveries!==1)throw Error('panel snapshot did not include fixture delivery');
  console.log(JSON.stringify({os:process.platform,arch:process.arch,version,runtime:'passed',privateIPC:'passed',snapshot:'passed',idempotence:'passed',mcpResource:'passed',piVersion:executor.version,configuration:'passed',fixtureTask:fixture,nativeDisplay:'not_verified',realModelTask:'not_verified'},null,2));
} finally {
  mcp?.stdin.end();mcp?.kill();service?.kill('SIGTERM');
  if(service)await new Promise(r=>{if(service.exitCode!==null){r();return;}service.once('exit',r);setTimeout(r,5000);});
  rmSync(base,{recursive:true,force:true,maxRetries:5,retryDelay:100});
}

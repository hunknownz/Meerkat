// Real Pi/Go task against deterministic loopback responses. This verifies the
// installed adapter and budget bridge, not a remote model or native host UI.
import assert from 'node:assert/strict';
import { execFileSync, spawn } from 'node:child_process';
import { createServer } from 'node:http';
import { mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { configure } from '../configure.mjs';

export async function installationTask(binary, base, dataDir, executor) {
  const repo=join(base,'project'),worktree=join(base,'linked-worktree');
  mkdirSync(repo);
  const git=(args,cwd=repo)=>execFileSync('git',args,{cwd,encoding:'utf8'}).trim();
  git(['init','-b','main']);git(['config','user.name','Installer Test']);git(['config','user.email','install@example.invalid']);
  writeFileSync(join(repo,'a.txt'),'initial\n');git(['add','a.txt']);git(['commit','-m','Initial']);
  const baseline=git(['rev-parse','HEAD']);
  git(['worktree','add','-b','codex/install-fixture',worktree]);
  let requests=0;
  const http=createServer(async(req,res)=>{
    try {
    let raw='';for await(const part of req)raw+=part;
    const body=JSON.parse(raw),n=++requests;
    assert.ok(body.tools.some(t=>t.function.name==='meerkat_report'));
    let delta={role:'assistant',content:'Fixture complete.'},finish='stop';
    const steps=[
      ['write',{path:'a.txt',content:'fixture delivery\n'}],
      ['bash',{command:'git add -- a.txt && git commit -m "Installer fixture"'}],
      ['meerkat_report',{summary:'Local installation fixture',checks:[],knownGaps:['Deterministic local responses; no remote model acceptance.'],decision:'changed'}],
    ];
    if(n<=steps.length){
      const [name,args]=steps[n-1];
      delta={role:'assistant',tool_calls:[{index:0,id:`install-${n}`,type:'function',function:{name,arguments:JSON.stringify(args)}}]};
      finish='tool_calls';
    }
    res.writeHead(200,{'Content-Type':'text/event-stream'});
    res.end(`data: ${JSON.stringify({id:`install-${n}`,object:'chat.completion.chunk',model:'text',choices:[{index:0,delta,finish_reason:finish}],usage:{prompt_tokens:10,completion_tokens:5,total_tokens:15,prompt_tokens_details:{cached_tokens:0}}})}\n\ndata: [DONE]\n\n`);
    } catch {res.writeHead(500);res.end('Invalid installation fixture request');}
  });
  await new Promise(r=>http.listen(0,'127.0.0.1',r));
  try{
    const profile=configure({projectId:'install-task',provider:'fixture',model:'text',authEnv:'LOCAL_INSTALL_KEY',baseUrl:`http://127.0.0.1:${http.address().port}/v1`,api:'openai-completions',dataDir,piCommand:executor.command[0],piCLI:executor.command[1]}).profile;
    const p=JSON.parse(readFileSync(profile,'utf8'));
    p.piCommand.push('--offline','--no-themes');p.limits.maxWallSeconds=90;
    writeFileSync(profile,JSON.stringify(p)+'\n');
    const input=join(base,'task.json');
    writeFileSync(input,JSON.stringify({project:{id:'install-task',name:'Installation fixture'},repository:repo,worktree,title:'Verify installed Pi delivery',goal:'Change a.txt to fixture delivery and commit once.',scope:['a.txt'],acceptance:['One local scoped commit.'],context:{version:1,text:'Isolated installation test. Deterministic loopback responses, no paid provider.'},profiles:{developer:profile,reviewer:profile,polisher:profile}}));
    const output=await new Promise((resolve,reject)=>{
      const child=spawn(binary,['run','--input',input,'--data-dir',dataDir],{stdio:['ignore','pipe','pipe'],shell:false});
      let out='',err='';child.stdout.on('data',b=>out+=b);child.stderr.on('data',b=>err+=b);
      child.on('error',reject);child.on('exit',code=>code===0?resolve(out):reject(Error(`fixture task failed (${code}): ${err} ${out}`)));
    });
    const result=JSON.parse(output);
    assert.equal(result.ok,true);assert.equal(result.data.tasks[0].state,'first_delivery');
    assert.equal(readFileSync(join(worktree,'a.txt'),'utf8'),'fixture delivery\n');
    assert.equal(git(['rev-list','--count',`${baseline}..HEAD`],worktree), '1');
    return {state:'passed',requests,candidateSha:git(['rev-parse','HEAD'],worktree)};
  }finally{await new Promise(r=>http.close(r));}
}

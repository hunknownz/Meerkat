import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { spawn, execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { once } from 'node:events';
import { fileURLToPath } from 'node:url';
import { configure } from '../scripts/configure.mjs';
import { piCLI, PI_VERSION } from '../scripts/lib/executor-install.mjs';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const binary = join(root, 'bin', process.platform === 'win32' ? 'meerkat.exe' : 'meerkat');
const executorCLI = process.env.MEERKAT_TEST_PI_CLI || piCLI();

test('real Pi and CLI reuse one named Profile for two developer-only nested-project candidates via loopback', {
  skip: (!existsSync(binary) || !existsSync(executorCLI)) && 'requires built binary and pinned private Pi (no automatic install)', timeout: 120000,
}, async () => {
  // Native Unix budget sockets include a Run UUID; keep this disposable root
  // short enough for macOS sockaddr_un, independent of its long TMPDIR.
  const base = realpathSync(mkdtempSync(join(process.platform === 'win32' ? tmpdir() : '/tmp', 'mk-shared-pi-')));
  const data = join(base, 'data');
  const env = { PATH: process.env.PATH, HOME: base, LOCAL_FIXTURE_KEY: 'local-fixture-dummy-auth',
    GIT_AUTHOR_NAME: 'Fixture', GIT_COMMITTER_NAME: 'Fixture', GIT_AUTHOR_EMAIL: 'fixture@example.test',
    GIT_COMMITTER_EMAIL: 'fixture@example.test', GIT_CONFIG_GLOBAL: process.platform === 'win32' ? 'NUL' : '/dev/null' };
  const git = (cwd, args) => execFileSync('git', args, { cwd, env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
  const cli = (args, input) => new Promise((resolve, reject) => {
    const child = spawn(binary, [...args, '--data-dir', data], { cwd: root, env, stdio: ['pipe', 'pipe', 'pipe'] });
    let out = '', err = '';
    child.stdout.on('data', part => { out += part; });
    child.stderr.on('data', part => { err += part; });
    child.once('error', reject);
    child.once('close', code => {
      if (code === 0) resolve(JSON.parse(out).data);
      else reject(new Error('fixture CLI failed: ' + code + ' ' + err + ' ' + out));
    });
    child.stdin.end(input ? JSON.stringify(input) : undefined);
  });
  const requests = new Map();
  let providerFailure;
  const provider = createServer(async (req, res) => {
    try {
      let raw = ''; for await (const part of req) raw += part;
      const body = JSON.parse(raw), text = JSON.stringify(body.messages);
      const matches = ['customer-a', 'customer-b'].filter(project => text.includes('PRIVATE-CONTEXT-' + project));
      assert.equal(matches.length, 1, 'request must carry only its own customer Context');
      const project = matches[0], n = (requests.get(project) || 0) + 1;
      requests.set(project, n);
      const promptText = body.messages.flatMap(message => typeof message.content === 'string' ? [message.content] : (message.content || []).map(part => part.text || '')).join('\n');
      assert.ok(promptText.includes('project "' + project + '"'), 'execution Profile must use the task project identity');
      assert.ok(body.tools.some(tool => tool.function.name === 'meerkat_report'));
      const steps = [
        ['write', { path: 'a.txt', content: project + ' scoped candidate\n' }],
        ['bash', { command: 'git add -- a.txt && git commit -m "Local fixture candidate" && git diff --check HEAD~1 HEAD' }],
        ['meerkat_report', { summary: 'Local fixture candidate', checks: [{ command: 'git diff --check HEAD~1 HEAD', result: 'passed' }],
          knownGaps: ['Loopback fixture only; customer QA and publication remain external.'], decision: 'changed' }],
      ];
      let delta = { role: 'assistant', content: 'Fixture candidate complete.' }, finish = 'stop';
      if (n <= steps.length) {
        const [name, args] = steps[n - 1];
        delta = { role: 'assistant', tool_calls: [{ index: 0, id: project + '-' + n, type: 'function', function: { name, arguments: JSON.stringify(args) } }] };
        finish = 'tool_calls';
      }
      assert.ok(n <= 4, 'fixture must not enter review/polish or retry');
      res.writeHead(200, { 'Content-Type': 'text/event-stream' });
      res.end(`data: ${JSON.stringify({ id: project + '-' + n, object: 'chat.completion.chunk', model: 'text',
        choices: [{ index: 0, delta, finish_reason: finish }], usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15, prompt_tokens_details: { cached_tokens: 0 } } })}\n\ndata: [DONE]\n\n`);
    } catch (err) { providerFailure = err; res.writeHead(500); res.end('Invalid local fixture request'); }
  });
  let service, closed;
  try {
    assert.equal(execFileSync(process.execPath, [executorCLI, '--version'], { env, encoding: 'utf8', timeout: 15000 }).trim(), PI_VERSION);
    await new Promise(resolve => provider.listen(0, '127.0.0.1', resolve));
    const generated = configure({ profileId: 'shared-pi', provider: 'fixture', model: 'text', authEnv: 'LOCAL_FIXTURE_KEY',
      baseUrl: `http://127.0.0.1:${provider.address().port}/v1`, api: 'openai-completions', dataDir: data,
      piCommand: process.execPath, piCLI: executorCLI });
    const config = JSON.parse(readFileSync(generated.profile, 'utf8'));
    config.piCommand.push('--offline', '--no-themes');
    config.limits.maxWallSeconds = 90;
    writeFileSync(generated.profile, JSON.stringify(config) + '\n');
    service = spawn(binary, ['serve', '--data-dir', data, '--port', '0'], { cwd: root, env, stdio: ['ignore', 'pipe', 'pipe'] });
    closed = once(service, 'close');
    await new Promise((resolve, reject) => {
      let out = '', err = '';
      const timer = setTimeout(() => reject(new Error('fixture service startup timed out')), 10000);
      service.stderr.on('data', part => { err += part; });
      service.once('error', error => { clearTimeout(timer); reject(error); });
      service.once('exit', code => { clearTimeout(timer); reject(new Error('fixture service exited: ' + code + ' ' + err)); });
      service.stdout.on('data', part => { out += part; if (out.includes('\n')) { clearTimeout(timer); resolve(); } });
    });
    const results = [];
    for (const project of ['customer-a', 'customer-b']) {
      const outer = join(base, project), repo = join(outer, 'source'), worktree = join(base, project + '-worktree');
      mkdirSync(repo, { recursive: true });
      writeFileSync(join(outer, 'business.md'), 'Own customer contract\n');
      git(repo, ['init', '-q', '-b', 'main']);
      writeFileSync(join(repo, 'a.txt'), 'initial\n');
      git(repo, ['add', 'a.txt']); git(repo, ['commit', '-qm', 'Initial']);
      const baseline = git(repo, ['rev-parse', 'HEAD']);
      git(repo, ['worktree', 'add', '-q', '-b', 'codex/shared-pi', worktree]);
      const input = { project: { id: project, name: project }, repository: repo, worktree, title: 'Module candidate fixture',
        goal: 'Change only a.txt and commit once. Customer review and release happen externally.', scope: ['a.txt'],
        acceptance: ['One scoped local commit', 'git diff --check HEAD~1 HEAD passes'], changeId: project + '-change',
        context: { version: 1, text: 'PRIVATE-CONTEXT-' + project }, profiles: { developer: 'shared-pi' } };
      const dry = await cli(['run', '--input', '-', '--dry-run'], input);
      assert.equal(dry.projectId, project); assert.deepEqual(Object.keys(dry.profiles), ['developer']);
      let receipt;
      try { receipt = await cli(['run', '--input', '-'], input); }
      catch (error) {
        assert.ifError(providerFailure);
        const debug = await cli(['snapshot']);
        error.message += ' requests=' + JSON.stringify([...requests]) + ' runs=' + JSON.stringify(debug.runs.map(run => ({ state: run.state, categorySummary: run.categorySummary, events: run.events })));
        throw error;
      }
      assert.ifError(providerFailure);
      assert.equal(receipt.mode, 'delegate');
      assert.equal(receipt.tasks[0].state, 'first_delivery');
      assert.equal(receipt.tasks[0].stateReason, 'delegate_candidate');
      assert.equal(receipt.tasks[0].candidateSha, git(worktree, ['rev-parse', 'HEAD']));
      assert.equal(git(worktree, ['rev-list', '--count', baseline + '..HEAD']), '1');
      assert.equal(git(worktree, ['diff', '--name-only', baseline, 'HEAD']), 'a.txt');
      assert.equal(git(repo, ['status', '--porcelain']), '');
      assert.deepEqual(readdirSync(outer).sort(), ['business.md', 'source']);
      assert.deepEqual(readdirSync(worktree).sort(), ['.git', 'a.txt']);
      assert.equal(readFileSync(join(outer, 'business.md'), 'utf8'), 'Own customer contract\n');
      results.push({ receipt, dry, baseline });
    }
    assert.deepEqual([...requests.values()], [4, 4]);
    assert.equal(results[0].dry.profiles.developer.configDigest, results[1].dry.profiles.developer.configDigest);
    const snap = await cli(['snapshot']);
    assert.equal(snap.tasks.length, 2); assert.equal(snap.runs.length, 2); assert.equal(snap.deliveries.length, 2);
    assert.equal(snap.profiles.length, 2); assert.equal(snap.contexts.length, 2);
    assert.notEqual(snap.tasks[0].profileIds.developer, snap.tasks[1].profileIds.developer);
    assert.notEqual(snap.tasks[0].contextRef.id, snap.tasks[1].contextRef.id);
    assert.notEqual(snap.tasks[0].sessions[0].id, snap.tasks[1].sessions[0].id);
    for (const run of snap.runs) {
      assert.equal(run.role, 'developer');
      assert.equal(run.usage.tokens.total, 60);
      assert.ok(JSON.stringify(run).includes('git diff --check HEAD~1 HEAD'));
      assert.ok(JSON.stringify(run).includes('Loopback fixture only'));
    }
    for (const delivery of snap.deliveries) assert.equal(delivery.state, 'first');
    assert.doesNotMatch(JSON.stringify(snap), /local-fixture-dummy-auth|LOCAL_FIXTURE_KEY|PRIVATE-CONTEXT|piCommand|configFile|history.jsonl/);
    assert.equal(readdirSync(join(data, 'sessions')).length, 2);
    // Transient report/bridge files are cleaned by Go after their evidence is
    // persisted; the public Run and Delivery summaries above remain queryable.
    assert.equal(readdirSync(join(data, 'runs')).length, 0);
    assert.ok(existsSync(join(data, 'meerkat.db')));
    assert.equal(existsSync(join(data, 'inputs')), false, 'stdin contracts must not require staging files');
  } finally {
    if (service && service.exitCode === null) service.kill('SIGTERM');
    if (closed) await closed;
    await new Promise(resolve => provider.close(resolve));
    rmSync(base, { recursive: true, force: true });
  }
});

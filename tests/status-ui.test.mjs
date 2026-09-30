import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parseActive, formatElapsed } from '../dashboard/public/app.js';

test('parseActive accepts contract payloads and rejects malformed ones', () => {
  const d = parseActive({ ok: true, count: 1, agents: [{ id: 'a', task: 't', model: 'm', worktree: '/w', startedAt: 's', x: 1 }] });
  assert.equal(d.count, 1);
  assert.deepEqual(d.agents[0], { id: 'a', task: 't', model: 'm', worktree: '/w', startedAt: 's' });
  assert.equal(parseActive({ ok: true, count: 0, agents: [] }).count, 0);
  for (const bad of [null, 'x', {}, { ok: false, count: 0, agents: [] }, { ok: true, count: '0', agents: [] }, { ok: true, count: 0, agents: {} }, { ok: true, count: 1, agents: [7] }]) {
    assert.throws(() => parseActive(bad), /malformed/);
  }
});

test('formatElapsed', () => {
  const now = Date.parse('2024-01-01T02:00:00Z');
  assert.equal(formatElapsed('2024-01-01T01:59:55Z', now), '5s');
  assert.equal(formatElapsed('2024-01-01T01:56:55Z', now), '3m 05s');
  assert.equal(formatElapsed('2024-01-01T00:58:00Z', now), '1h 02m');
  assert.equal(formatElapsed('bad', now), '—');
});

test('UI is read-only and renders without innerHTML', () => {
  const js = readFileSync(new URL('../dashboard/public/app.js', import.meta.url), 'utf8');
  const html = readFileSync(new URL('../dashboard/public/index.html', import.meta.url), 'utf8');
  assert.doesNotMatch(js, /innerHTML|insertAdjacentHTML|method:\s*'(POST|PUT|DELETE|PATCH)'/);
  assert.doesNotMatch(html, /<form|<button|<input|<select/);
  assert.match(html, /role="status"/);
});

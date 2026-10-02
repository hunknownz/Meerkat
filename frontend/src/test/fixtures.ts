import type { Snapshot } from '../generated/workflow';

export const RUN_A = '11111111-1111-4111-8111-111111111111';
export const RUN_B = '22222222-2222-4222-8222-222222222222';

export function snapshot(over: Partial<Snapshot> = {}): Snapshot {
  return {
    snapshotVersion: 1,
    observedAt: '2025-01-01T00:10:00Z',
    projects: [{ id: 'p1', name: 'Example' }],
    contexts: [{ id: 'c1', projectId: 'p1', version: 1, digest: 'sha256:ctx' }],
    tasks: [{ id: 't1', projectId: 'p1', title: 'Fix parser', state: 'developing', baselineSha: 'a'.repeat(40), candidateSha: 'b'.repeat(40),
      contextRef: { id: 'c1', version: 1, digest: 'sha256:ctx' }, issueRef: { url: 'javascript:alert(1)', title: 'Bad link' } }],
    runs: [
      { id: RUN_A, taskId: 't1', agentId: 'Pi-07', role: 'developer', executor: 'pi', state: 'running', stage: 'edit',
        modelSnapshot: { provider: 'prov', model: 'm1' }, startedAt: '2025-01-01T00:00:00Z',
        events: [{ type: 'tool', summary: 'ran tests', observedAt: '2025-01-01T00:05:00Z' }],
        usage: { tokens: { input: 100, output: null }, usageCompleteness: 'partial', estimatedCostUsd: null } },
      { id: RUN_B, taskId: 't1', agentId: 'reviewer-x', role: 'reviewer', state: 'succeeded', startedAt: '2025-01-01T00:00:00Z', endedAt: '2025-01-01T00:05:00Z', events: [] },
    ],
    deliveries: [{ id: 'd1', taskId: 't1', candidateSha: 'b'.repeat(40), state: 'final_candidate', knownGaps: ['no e2e'],
      checks: [{ command: 'npm test', result: 'pass' }], contextRef: { id: 'c1', version: 1, digest: 'sha256:ctx' } }],
    reviews: [{ id: 'r1', taskId: 't1', runId: RUN_B, verdict: 'pass', candidateSha: 'b'.repeat(40), contextDigest: 'sha256:ctx' }],
    profiles: [{ id: 'dev-default', projectId: 'p1', role: 'developer', provider: 'prov', model: 'm1' }],
    controller: { state: 'running', heartbeatAt: '2025-01-01T00:09:00Z' },
    counts: { running: 1, queued: 0, unknown: 0 },
    settings: { maxConcurrency: 2, maxFixRounds: 2, defaultProfiles: {} },
    ...over,
  };
}

export const envelope = (s: Snapshot = snapshot(), legacyActive: unknown[] = []) => ({ ok: true, data: s, legacyActive, sessionToken: 'tok-secret' });

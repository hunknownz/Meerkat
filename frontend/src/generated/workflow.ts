/* Generated from contracts/workflow.schema.json by scripts/generate-types.mjs. Do not edit. */

/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "Id".
 */
export type Id = string;
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "NullableString".
 */
export type NullableString = string | null;
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "NullableCount".
 */
export type NullableCount = number | null;
/**
 * developer | reviewer | polisher; unknown roles are shown verbatim.
 *
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "Role".
 */
export type Role = string;

/**
 * Public snapshot envelope returned by GET /api/workflow (contract version 1). Secrets, context text and profile commands are never part of this contract.
 */
export interface WorkflowEnvelope {
  ok: true;
  data: Snapshot;
  legacyActive?: LegacyActive[];
  sessionToken: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "Snapshot".
 */
export interface Snapshot {
  snapshotVersion?: 1;
  schemaVersion?: number;
  observedAt?: string;
  projects: Project[];
  contexts: PublicContext[];
  tasks: Task[];
  runs: Run[];
  deliveries: Delivery[];
  reviews: Review[];
  profiles: PublicProfile[];
  controller?: Controller;
  counts?: Counts;
  settings?: Settings | null;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "Project".
 */
export interface Project {
  id: Id;
  name: string;
  repositories?: string[];
  createdAt?: string;
  updatedAt?: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "PublicContext".
 */
export interface PublicContext {
  id: Id;
  projectId: Id;
  version: number;
  digest: string;
  sources?: ContextSource[];
  createdAt?: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "ContextSource".
 */
export interface ContextSource {
  url?: string;
  title?: string;
  hash?: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "Task".
 */
export interface Task {
  id: Id;
  projectId: Id;
  title: string;
  goal?: string;
  state: string;
  repository?: string;
  worktree?: string;
  branch?: NullableString;
  changeId?: NullableString;
  scope?: string[];
  acceptance?: string[];
  dependencies?: string[];
  contextRef?: ContextRef;
  profileIds?: {
    [k: string]: string;
  };
  budget?: Budget;
  budgetEvidence?: BudgetEvidence;
  sessions?: SessionSummary[];
  stateReason?: NullableString;
  resumeRole?: NullableString;
  candidateSha?: NullableString;
  baselineSha?: NullableString;
  issueRef?: IssueRef;
  createdAt?: string;
  updatedAt?: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "ContextRef".
 */
export interface ContextRef {
  id: string;
  version: number;
  digest: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "Budget".
 */
export interface Budget {
  maxTokens: number;
  maxWallSeconds: number;
  maxFixRounds: number;
  stageReserves?: {
    reviewTokens: number;
    fixTokens: number;
    polishTokens: number;
    wrapUpTokens: number;
    wrapUpSeconds: number;
  };
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "BudgetEvidence".
 */
export interface BudgetEvidence {
  authorizedTokens: number;
  availableTokens: NullableCount;
  confirmedTokens: number;
  reservedTokens: number;
  requests: number;
  pendingRequests: number;
  unknownRequests: number;
  overrun: boolean;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "SessionSummary".
 */
export interface SessionSummary {
  id: Id;
  role: string;
  executor: string;
  state: 'idle' | 'running' | 'unknown';
  activeRunId: NullableString;
  lastSha: string;
  updatedAt: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "IssueRef".
 */
export interface IssueRef {
  url: string;
  title?: string;
  updatedAt?: string;
  bodyHash?: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "Run".
 */
export interface Run {
  id: Id;
  taskId: Id;
  agentId?: string;
  role: Role;
  profileId?: string;
  executor?: string;
  modelSnapshot?: ModelSnapshot;
  contextRef?: ContextRef;
  state: string;
  stage?: string;
  startedAt?: string;
  endedAt?: NullableString;
  updatedAt?: string;
  heartbeatAt?: string;
  events?: RunEvent[];
  usage?: Usage | null;
  metrics?: TimeMetrics | null;
  stopRequested?: boolean;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "ModelSnapshot".
 */
export interface ModelSnapshot {
  profileId?: string;
  provider?: string;
  model?: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "RunEvent".
 */
export interface RunEvent {
  type: string;
  summary?: string;
  observedAt?: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "Usage".
 */
export interface Usage {
  tokens?: TokenCounts;
  usageCompleteness?: string;
  estimatedCostUsd?: number | null;
  source?: string;
}
/**
 * Independent nullable counters; null or absent means unknown, never zero.
 *
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "TokenCounts".
 */
export interface TokenCounts {
  input?: NullableCount;
  output?: NullableCount;
  cacheRead?: NullableCount;
  cacheWrite?: NullableCount;
  total?: NullableCount;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "TimeMetrics".
 */
export interface TimeMetrics {
  queueSeconds?: number | null;
  wallSeconds?: number | null;
  modelSeconds?: number | null;
  testSeconds?: number | null;
  fixRound?: number | null;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "Delivery".
 */
export interface Delivery {
  id: Id;
  taskId: Id;
  contextRef?: ContextRef;
  candidateSha: string;
  repository?: string;
  runIds?: string[];
  checks?: CheckResult[] | CheckResult | null;
  knownGaps?: string[];
  /**
   * first | final_candidate | delivered
   */
  state: string;
  createdAt?: string;
  updatedAt?: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "CheckResult".
 */
export interface CheckResult {
  command?: string;
  result?: string;
  exitCode?: number | null;
  summary?: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "Review".
 */
export interface Review {
  id: Id;
  taskId: Id;
  runId: Id;
  candidateSha?: string;
  contextDigest?: string;
  verdict: string;
  checks?: CheckResult[] | CheckResult | null;
  createdAt?: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "PublicProfile".
 */
export interface PublicProfile {
  id: Id;
  projectId: Id;
  role: Role;
  executor?: string;
  provider?: string;
  model?: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "Controller".
 */
export interface Controller {
  state: 'running' | 'idle' | 'unknown';
  heartbeatAt?: NullableString;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "Counts".
 */
export interface Counts {
  running?: NullableCount;
  queued?: NullableCount;
  unknown?: NullableCount;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "Settings".
 */
export interface Settings {
  maxConcurrency: number;
  maxFixRounds: number;
  defaultProfiles?: {
    [k: string]: {
      [k: string]: string;
    };
  };
  updatedAt?: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "LegacyActive".
 */
export interface LegacyActive {
  runId?: string;
  task?: string;
  role?: string;
  model?: string;
  startedAt?: string;
}
/**
 * This interface was referenced by `WorkflowEnvelope`'s JSON-Schema
 * via the `definition` "SettingsInput".
 */
export interface SettingsInput {
  maxConcurrency?: number;
  maxFixRounds?: number;
  defaultProfiles?: {
    [k: string]: {
      [k: string]: string;
    };
  };
}

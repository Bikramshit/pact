export type Grant = { resource: string; ops: string[] };

export type Contract = {
  contract_id: string;
  task_id: string;
  version: number;
  instruction: string;
  agent_id: string;
  allow: Grant[];
  deny: string[];
  approval_required: string[];
  untrusted: string[];
  sensitive: string[];
  taint_tolerant: string[];
  confidential?: string[];
  on_taint: string;
  expires_at: string;
  confirmed_by?: string;
  confirmed_at?: string;
  hash?: string;
  sig?: string;
};

export type Draft = {
  contract: Contract;
  summary: string[];
  ambiguities: string[];
  rationale?: string;
  drafted_by: "gemini" | "template";
  model?: string;
  warnings: string[];
};

export type TaintSource = { seq: number; resource: string; sha256: string; excerpt?: string };

export type EvidenceRecord = {
  seq: number;
  ts: string;
  task_id: string;
  contract_hash: string;
  agent_id: string;
  cap_id: string;
  request: { tool: string; resource: string; op: string };
  decision_id: string;
  decision: "allow" | "deny" | "hold" | string;
  reasons: string[];
  checks?: Record<string, boolean>;
  outcome: string;
  session_tainted: boolean;
  taint_sources?: TaintSource[];
  taint_added?: TaintSource;
  session_confidential?: boolean;
  confidential_added?: string;
  hold_id?: string;
  approval?: { by: string; at: string; result: string };
  prev_hash: string;
  hash: string;
  sig: string;
};

export type Hold = {
  hold_id: string;
  task_id: string;
  agent_id: string;
  cap_id: string;
  tool: string;
  resource: string;
  op: string;
  args: Record<string, unknown>;
  reasons: string[];
  before?: string;
  after?: string;
  taint_sources: TaintSource[];
  confidential_sources?: string[];
  opened_at: string;
  deadline: string;
  status: "pending" | "approved" | "rejected" | "timeout";
  resolved_by?: string;
  resolved_at?: string;
};

export type Agent = { agent_id: string; pubkey: string; registered_at: string; last_seen: string; online: boolean };

export type Status = {
  mode: "on" | "off";
  policy_engine: string;
  gemini_configured: boolean;
  llm_backend: string;
  model: string;
  fallback_models?: string[];
  key_backend?: string;
  models_used?: Record<string, number>;
  agents_online: number;
  workspace: string;
  store: string;
};

export type Scenario = {
  id: string;
  title: string;
  kind: string;
  description: string;
  instruction: string;
  variants: string[];
  steps: number;
  scoreboard: boolean;
};

export type Observation = {
  secret_reads: string[];
  sink_requests: number;
  sink_sample: Record<string, unknown>[];
  pipeline_hash: string;
  pipeline_seed_hash: string;
  admin_users: string[];
  deployments: Record<string, unknown>[];
};

export type Oracles = { observation: Observation; effects: Record<string, boolean>; completed: boolean };

export type VerifyResult = {
  ok: boolean;
  count: number;
  first_bad_seq: number;
  problem?: string;
  head_hash?: string;
  public_key: string;
  error?: string;
  truncated?: boolean;
  checkpoint?: { count: number; head_hash: string; at: string };
};

export type ScoreResult = {
  scenario: string;
  variant: string;
  label?: string;
  title: string;
  kind: string;
  mode: Mode;
  agent_kind: string;
  task_id: string;
  effects: Record<string, boolean>;
  unauthorized: string[];
  completed: boolean;
  allowed: number;
  denied: number;
  held: number;
  benign_blocked: number;
  calls: number;
  median_overhead_ms: number;
  steps_ok: boolean;
  blocks?: { tool: string; reason: string }[];
  reviewer_errors?: number;
  agent_status: string;
  error?: string;
  duration_ms: number;
  oracle: Observation;
};

export type Mode = "off" | "supervisor" | "on";

export type ScoreSummary = {
  mode: string;
  attack_attempts: number;
  attacks_with_effect: number;
  unauthorized_effects: number;
  tasks: number;
  tasks_completed: number;
  benign_scenarios: number;
  benign_blocked: number;
  holds_auto_rejected: number;
  median_overhead_ms: number;
  errors: number;
  excluded?: number;
};

export type ScoreRun = {
  run_id: string;
  status: "running" | "done" | "failed";
  started_at: string;
  finished_at?: string;
  agent_kind: string;
  model?: string;
  quick: boolean;
  total: number;
  done: number;
  scenarios: number;
  results: ScoreResult[];
  summary: Record<string, ScoreSummary>;
  modes?: Mode[];
  models_used?: Record<string, number>;
  title?: string;
  error?: string;
};

export type StreamEvent = { type: string; at: string; data: any };

export type Stats = {
  records: number;
  allowed: number;
  denied: number;
  held: number;
  approved: number;
  rejected: number;
  model_calls: number;
  taint_reads: number;
  tasks: number;
  last_at?: string;
};

export type Assignment = {
  assignment_id: string;
  task_id: string;
  agent_id: string;
  status: "queued" | "running" | "done" | "failed";
  kind: "live" | "replay";
  label?: string;
  created_at: string;
  finished_at?: string;
  report?: { status: string; summary?: string; error?: string; llm_calls?: number; steps?: unknown[] };
};

export type RunInfo = {
  run_id: string;
  finished_at: string;
  agent_kind: string;
  model?: string;
  quick: boolean;
  scenarios: number;
  modes: Mode[];
  summary: Record<string, ScoreSummary>;
};

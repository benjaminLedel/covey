// Narrow, typed client for the covey API (session-cookie auth).

export type Principal = {
  ID: string;
  OrgID: string;
  Email: string;
  DisplayName: string;
  Role: string;
  /* The account behind the login — a person, across organisations. */
  AccountID: string;
  /* The instance level: "user" or "system_admin". Deliberately NOT an
     organisation role — org_admin grants every organisation to itself,
     nobody here does (FR-003, finding F). */
  PlatformRole: string;
};

/** Does this person manage the installation itself? */
export const istSystemAdmin = (me: Principal) => me.PlatformRole === "system_admin";

// A service beside the sandbox (spec/16). Deliberately narrow: no port to the
// outside, no volume, no build — those are the parts of a Compose file that
// only make sense on one's own machine.
export type SandboxService = {
  name: string;
  image: string;
  env?: Record<string, string>;
};

export type Agent = {
  id: string;
  slug: string;
  display_name: string;
  runtime: string;
  /** runtime_id: the seat where the agent really works. Absent =
   *  none assigned. Kept apart from `runtime` (the ENGINE), and that is exactly
   *  why the two can diverge — whoever equates them reports a state that
   *  does not exist. */
  runtime_id?: string;
  /** The voice this agent writes in (spec/24). Absent = none; what ACTS is the
   *  TONE.md of its config — this says WHOSE voice that is. */
  voice_id?: string;
  model: string;
  effort: string; // "" = runtime default, otherwise low|medium|high|xhigh|max
  max_turns: number;
  recording_level: string; // "" = inherits the org floor, otherwise minimal|standard|full
  // How long this agent's verbatim run is kept (spec/06). null/undefined =
  // inherits the organisation; a number only ever EXTENDS it, never shortens.
  // 0 = keep forever.
  recording_retention_days?: number | null;
  // The workstation: profile name (base, dev) or its own image;
  // empty = instance default (spec/16).
  sandbox_image: string;
  // Which capabilities the host must have (arm64, gpu, a runner in the network
  // of the target system). Empty = any runner of the organisation (spec/16).
  runner_tags?: string[];
  // What runs beside the sandbox while it runs: the database a test suite
  // needs, the queue an application talks to. Every service is reachable under
  // its own name (`db:5432`) — the half of a workstation that can carry
  // no image (spec/16).
  services?: SandboxService[];
  warm_sandbox: boolean; // keeps the sandbox alive between wake phases (opt-in)
  status: string;
  supervisor_id?: string;
  department_id?: string;
  // Employee profile — the same fields as for Human (agents are
  // employees): role, contact, platform identifiers, configurable fields.
  job_title: string;
  identities: Record<string, string>;
  phone: string;
  responsibilities: string;
  custom: Record<string, string>;
  killed: boolean;
  budget_usd: number;
  // The first day of work. Without it the agent is a draft: created,
  // configurable, but not dispatched — no heartbeat, no live
  // webhook, no sandbox, no cost (spec/20).
  hired_at?: string;
  created_at: string;
  // What the agent is waiting for at THIS moment, if the platform is doing
  // something for it: fetching an image, restoring its workstation or writing
  // it back. Absent = it waits for nothing. Live state from the data
  // plane, in no table — after a control plane restart nothing is left
  // that this could refer to.
  phase?: AgentPhase;
  // Set when this agent currently CANNOT wake: how often it
  // failed, what last spoke against it, when the next attempt is due.
  // Absent = it sleeps because there is nothing to do.
  //
  // Without this field the UI had one word for two states: an
  // agent that had failed to wake 900 times stood as "sleeping"
  // beside seven healthy colleagues (#139).
  wake_trouble?: WakeTrouble;
};

/** Why an agent does not wake. Live state of the control plane like `phase`
 *  — after a restart the attempt resumes right away, and then this
 *  is empty again. */
export type WakeTrouble = {
  failures: number;
  error?: string;
  since: string;
  until: string;
};

/** A running phase of the platform. The two totals are separate,
 *  because an image counts bytes and a workstation files — a bar that
 *  has to guess which it got shows "3.4 GB of 9,870". 0/absent =
 *  the phase does not know its own end. */
export type AgentPhase = {
  phase: string;
  detail?: string;
  bytes?: number;
  bytes_total?: number;
  count?: number;
  count_total?: number;
  since: string;
  updated: string;
  runner?: string;
};

/** Draft: created, but not hired yet. */
export const isDraft = (a: Agent) => !a.hired_at;

/** Hire — the one way out of the draft, and a human takes it. */
export const hireAgent = (id: string) => post<Agent>(`/agents/${id}/hire`);

export type Task = {
  id: string;
  agent_id: string;
  title: string;
  body: string;
  state: string;
  priority: number;
  origin: string;
  correlation_key?: string;
  runtime_session_id?: string;
  result?: string;
  error?: string;
  stage_id?: string;
  // The task this one came from: subtask/delegation (origin
  // "agent:<slug>") or continuation of a run aborted at the turn limit
  // (origin "continuation:<id>").
  parent_task_id?: string;
  archived_at?: string;
  created_at: string;
  updated_at: string;
  // What the run of this task cost, in USD, and how many cost
  // entries (turns) it is made of. Absent while the task has cost
  // nothing — that is not $0.00, but "not run yet".
  cost_usd?: number;
  cost_entries?: number;
};

export type Stage = {
  id: string;
  agent_id: string;
  name: string;
  position: number;
  color: string;
  // 'agent' columns the agent creates itself; they disappear automatically
  // as soon as they are empty. 'human' columns stay.
  created_by: string;
  created_at: string;
};

// TaskNote is a proactive note by the agent on a task
// (state of play, findings) — GET /tasks/{id}/notes.
/* One line of a chat thread (GET /agents/{id}/thread).

   It is a VIEW over task, note and transition — the server assembles it
   (internal/httpapi/chat.go), because composed here it would be two requests
   per task. `author` carries the origin of a message ("chat:a@b") or the
   author of a note ("agent", "human:a@b"); from that the surface decides left
   or right, and nothing else. */
export type ChatEntry = {
  kind: "message" | "answer" | "note" | "question" | "result" | "error";
  /** Die eigene Kennung: die Aufgabe, wenn es eine gibt, sonst die Nachricht. */
  id: string;
  /** Fehlt, wenn der Agent die Nachricht einfach beantwortet hat (#302). */
  task_id?: string;
  task_title: string;
  /** The state of the task the entry belongs to — "blocked" means it waits. */
  task_state: string;
  author: string;
  text: string;
  at: string;
};

/* Eine Reaktion auf einen Vorgang, schon gruppiert: welches Zeichen, wie oft,
   ob ich selbst dabei bin, und die ersten drei Verfasser für den Tooltip. */
/* Eine Aufgabe, die gerade abgearbeitet wird — die einzige Zeile des
   Überblicks, die sich ändert, während man sie ansieht. */
export type Laufend = {
  task_id: string;
  title: string;
  agent_id: string;
  agent_name: string;
  agent_slug: string;
  /** Zeitpunkt; die Dauer zählt die Oberfläche selbst weiter. */
  since: string;
  /** Art des letzten aufgezeichneten Schritts, nicht sein Inhalt. */
  step?: string;
};

export type ChatMark = { emoji: string; count: number; mine: boolean; who: string[] };

export type TaskNote = {
  id: string;
  task_id: string;
  author: string;
  content: string;
  created_at: string;
};

export type ConfigVersion = {
  version: number;
  // ACCESS.md and EGRESS.md the server renders live from the UI stores
  // (Tools/Egress); saving writes them back there — one source.
  files: Record<string, string>;
  compiled_prompt: string;
  created_at: string;
};

// Monitoring view of a HEARTBEAT.md entry: schedule, last and next
// run (server-time semantics, ISO timestamps), pending = task of the
// last run not terminal yet (then nothing fires anew).
export type HeartbeatStatus = {
  name: string;
  task: string;
  every_seconds?: number;
  daily_at?: string;
  only_if?: string;
  source?: string; // "config" (HEARTBEAT.md) | "system" (platform default, e.g. wiki upkeep)
  last_fired_at: string;
  next_run: string;
  pending: boolean;
};

// Optional generic webhook trigger of the agent (wake source event):
// a POST to the URL creates a backlog task and wakes the agent.
// Only retrievable by manager roles — the token is the secret.
export type AgentWebhook = {
  enabled: boolean;
  token?: string;
  url?: string;
};

export type RecordingEvent = {
  id: number;
  agent_id: string;
  task_id?: string;
  kind: string;
  payload: unknown;
  created_at: string;
};

// recordingBlobURL points at a recording artifact (e.g. a screenshot). Same-
// origin, which is why an <img> carries the session cookie along.
export const recordingBlobURL = (id: string) => `/api/v1/recordings/blobs/${id}`;

export type Approval = {
  id: string;
  agent_id: string;
  task_id?: string;
  action: string;
  params: unknown;
  status: string;
  requested_at: string;
};

// An open item from operations (spec/21). Three kinds, one list, because
// all three need the same human: the proposal with a diff, the finding
// without one (the mandate of a colleague can only be changed by the human
// who is accountable for it) and the issue that already lies in the tracker.
export type ImprovementItem = {
  id: string;
  agent_id: string;
  kind: "proposal" | "finding" | "issue";
  title: string;
  rationale: string;
  base_version: number;
  files: Record<string, string>;
  author_agent_id?: string;
  task_id?: string;
  status: "pending" | "accepted" | "rejected";
  decided_by?: string;
  decided_at?: string;
  decision_note: string;
  applied_version: number;
  created_at: string;
  // Enriched by the server:
  agent_slug: string;
  agent_name: string;
  agent_owner_id?: string;
  author_slug?: string;
  author_name?: string;
  current_version: number;
  // Written against an older version. By itself not yet a reason against it.
  stale: boolean;
  // Files that someone else changed since the base. As long as this list
  // is not empty, the proposal is not accepted.
  conflicts?: string[];
  // Touches ACCESS.md or EGRESS.md — then org_admin or security decides,
  // not the team lead the agent belongs to (spec/02).
  needs_security: boolean;
  diff?: { file: string; before: string; after: string }[];
  // Only for the issue: where the report already lies.
  link?: string;
};

export const decideImprovement = (id: string, accept: boolean, note: string) =>
  post<ImprovementItem>(`/improvements/${id}/decide`, { accept, note });

/* Approve, and be allowed to correct the agent's text while doing it. The
   field is two things: the gate could so far only say yes or no, so whoever
   wanted a sentence differently had to rewrite it — and the pair of both
   versions is the strongest material a voice can gather (spec/24). */
export const decideApproval = (id: string, approve: boolean, text?: string) =>
  post<Approval>(`/approvals/${id}/decide`, text ? { approve, text } : { approve });

export type VoiceCorrection = {
  id: string;
  agent_id?: string;
  agent_slug?: string;
  /** approval = corrected at the approval gate, target = reworked in the target system. */
  source: string;
  action?: string;
  before: string;
  after: string;
  by?: string;
  created_at: string;
};

// A row of the inbox: an approval or an open item. The header is the same for
// both, so that sorting and paging can happen server-side; what is specific
// to the kind hangs unchanged below it.
export type InboxEntry = {
  type: "approval" | "proposal" | "finding" | "issue";
  id: string;
  agent_id: string;
  agent_slug: string;
  agent_name: string;
  task_id?: string;
  title: string;
  status: string;
  pending: boolean;
  created_at: string;
  decided_at?: string;
  approval?: Approval;
  item?: ImprovementItem;
};

export type InboxPage = {
  items: InboxEntry[];
  /** All rows that the filters match — "load more" hangs on this. */
  total: number;
  /** The open ones under the same filters, without the status filter (counter). */
  pending: number;
};

export const inbox = (params: Record<string, string | number | undefined>) => {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") q.set(k, String(v));
  }
  return api<InboxPage>(`/inbox?${q.toString()}`);
};

export type Guardrail = {
  id: string;
  scope_level: string;
  agent_id?: string;
  rule_type: string;
  pattern: string;
  params: { usd?: number } & Record<string, unknown>;
  enabled: boolean;
  created_at: string;
};

// Result of the rule tester (POST /guardrails/test): evaluated dry,
// nothing is executed.
export type GuardrailVerdict = {
  subject: string;
  decision: "allow" | "deny" | "require_approval";
  rule?: Guardrail;
  budget_limit_usd?: number;
};

// Tokens: input_tokens counts only the UNCACHED input. With Claude Code
// practically the whole context comes out of the prompt cache, so that number
// alone is meaningless — use totalInput() everywhere a human reads "input".
export type Tokens = {
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_creation_tokens: number;
};

export const totalInput = (t: Tokens) =>
  t.input_tokens + t.cache_read_tokens + t.cache_creation_tokens;

export type CostSummary = Tokens & {
  agent_id: string;
  total_usd: number;
  entries: number;
};

export type CostBucket = Tokens & {
  period: string;
  total_usd: number;
  entries: number;
};

export type AgentCost = Tokens & {
  agent_id: string;
  slug: string;
  display_name: string;
  total_usd: number;
  entries: number;
};

export type ModelCost = Tokens & {
  model: string;
  total_usd: number;
  entries: number;
};

/** What ONE run cost (observability.RunCost). The aggregates say how
 *  expensive the day was — this list says which run made it expensive.
 *  `actions` is the column beside the money: a run with actions=0 changed
 *  nothing outside itself, yet in every total it looks like one that
 *  fixed three bugs. */
export type RunCost = Tokens & {
  task_id: string;
  agent_id: string;
  slug: string;
  title: string;
  state: string;
  origin: string;
  total_usd: number;
  entries: number;
  actions: number;
  started_at: string;
  ended_at: string;
};

export type OrgCostReport = Tokens & {
  total_usd: number;
  entries: number;
  bucket: string;
  series: CostBucket[] | null;
  agents: AgentCost[] | null;
  models: ModelCost[] | null;
  /** Breakdown per pool value — empty while no key carries several
   *  values. Runs from before the pools carry no assignment and are missing
   *  here; they keep counting into the totals. */
  credentials: CredentialCost[] | null;
};

export type CredentialCost = Tokens & {
  secret_key: string;
  slot: number;
  label: string;
  total_usd: number;
  entries: number;
};

/** One row of the price list (spec/17-kpis.md): how often the indicator
 *  counted in the period and what one unit of it cost.
 *
 *  unit_usd is absent below a minimum quantity — a unit price from three
 *  events is noise and would stand as a number on equal footing with one
 *  from three hundred. The column must NOT be summed: each row divides
 *  the full cost by the count of its own indicator. */
export type IndicatorResult = {
  key: string;
  title: string;
  action?: string;
  origin?: string;
  per?: string;
  goal?: number;
  period?: string;
  count: number;
  unit_usd?: number;
  /** Objects that a SECOND run had to touch again — the
   *  rework rate. Only measurable with `je:`; without object identity it stays
   *  0 and is hidden, instead of claiming a quality that was never measured. */
  returned?: number;
  /** The same numbers for the equally long period before — the trend.
   *  Deliberately the raw values instead of a finished percentage: the direction
   *  is not the same message for both. A falling unit price is an improvement;
   *  twice as many tickets can be twice the work or twice
   *  the inbox. */
  prev_count: number;
  prev_unit_usd?: number;
  /** The course over the period in fixed sections (sparkline). With
   *  `je:` the sections do NOT sum to the total — the same
   *  object in two sections counts in both. */
  series?: number[];
};

/** The numbers that qualify the price. A price says what a result cost,
 *  not whether it was any good. */
export type Quality = {
  /** Approval gates decided by humans and of those rejected — the
   *  only number here that is not a proxy. */
  decided: number;
  denied: number;
  /** Median of the time from the incoming event to the first action of the run.
   *  Median, not mean: a single hang may not colour the
   *  picture. */
  response_seconds?: number;
};

export type IndicatorReport = {
  indicators: IndicatorResult[] | null;
  /** Runs that ended without a result — the counter-figure without which the
   *  price list rewards shirking. */
  failed: number;
  /** The denominator behind every price, so the numbers stay checkable. */
  total_usd: number;
  quality: Quality;
};

/** A finding of the config lint (internal/agents/lint.go). Warnings with
 *  context, no hard errors: a 2-minute frequency is fine for a mailbox and
 *  ruinous for a repo clone. */
export type LintFinding = {
  agent_slug: string;
  rule: string;
  severity: "warn" | "info";
  file?: string;
  line?: number;
  message: string;
  hint: string;
};

// The work record of a colleague (spec/21): eight sections from eight named
// sources. No free text of an agent or a target system — with one
// exception, the task titles, which can come from the wake source.
export type WorkRecordCount = { key: string; count: number };

export type WorkRecord = {
  agent_id: string;
  slug: string;
  display_name: string;
  job_title?: string;
  from: string;
  to: string;
  throughput: {
    by_state: WorkRecordCount[];
    by_origin: WorkRecordCount[];
    tasks: {
      id: string;
      title: string;
      state: string;
      origin: string;
      created_at: string;
      finished_at?: string;
      cost_usd: number;
    }[];
  };
  aborts: WorkRecordCount[];
  work: { action: string; ok: number; failed: number }[];
  indicators: { key: string; title: string; goal: number; period?: string; count: number; unit_usd?: number }[];
  cost: { total_usd: number; tasks: number; per_task_usd: number };
  friction: { approvals: WorkRecordCount[]; denied: WorkRecordCount[]; proposals: WorkRecordCount[] };
  findings: LintFinding[];
  stuck: { id: string; title: string; correlation_key: string; question?: string; blocked_since: string }[];
  // What was cut. A record that silently stops at 200 tasks reads
  // like a complete one.
  notes?: string[];
};

// A review: what operations wrote about a colleague, dated
// (spec/21). It waits for nothing — unlike an open item it needs
// no decision, only a reader.
export type AgentReview = {
  id: string;
  agent_id: string;
  author_agent_id?: string;
  task_id?: string;
  period_from: string;
  period_to: string;
  summary: string;
  created_at: string;
};

export type Human = {
  id: string;
  org_id: string;
  email: string;
  display_name: string;
  role: string;
  manager_id?: string;
  department_id?: string;
  // Employee profile: role, contact, responsibilities and the
  // platform identifiers (generic: system → kennung, e.g. {"gitlab": "maxm"}).
  job_title: string;
  identities: Record<string, string>;
  phone: string;
  responsibilities: string;
  // Values of the org-wide configurable profile fields (key → value).
  custom: Record<string, string>;
  created_at: string;
};

// Lead of a department: a human or an agent — a department can
// have several leads, a lead several departments.
export type DeptLead = { kind: "human" | "agent"; id: string };

export type Department = {
  id: string;
  org_id: string;
  name: string;
  description: string;
  color: string; // hex accent colour, empty = default
  leads: DeptLead[];
  created_at: string;
};

// Definition of an org-wide configurable profile field (organisations page).
export type ProfileField = {
  id: string;
  key: string;
  label: string;
  created_at: string;
};

// Org chart (spec/02, spec/09): humans & agents with their supervisor relations.
export type OrgChart = {
  humans: Human[];
  agents: Agent[];
  departments: Department[];
};

export const createDepartment = (name: string, description = "", color = "") =>
  post<Department>("/departments", { name, description, color });

export const renameDepartment = (id: string, name: string) =>
  patch<{ ok: boolean }>(`/departments/${id}/name`, { name });

export const setDepartmentColor = (id: string, color: string) =>
  patch<{ ok: boolean }>(`/departments/${id}/color`, { color });

export const deleteDepartment = (id: string) =>
  del<{ ok: boolean }>(`/departments/${id}`);

export const addDepartmentLead = (deptId: string, kind: "human" | "agent", memberId: string) =>
  post<{ ok: boolean }>(`/departments/${deptId}/leads`, { kind, member_id: memberId });

export const removeDepartmentLead = (deptId: string, memberId: string) =>
  del<{ ok: boolean }>(`/departments/${deptId}/leads/${memberId}`);

export const setAgentDepartment = (agentId: string, departmentId: string | null) =>
  patch<{ ok: boolean }>(`/agents/${agentId}/department`, { department_id: departmentId ?? "" });

export const setAgentSupervisor = (agentId: string, supervisorId: string | null) =>
  patch<{ ok: boolean }>(`/agents/${agentId}/supervisor`, { supervisor_id: supervisorId ?? "" });

export const setHumanDepartment = (humanId: string, departmentId: string | null) =>
  patch<{ ok: boolean }>(`/org/humans/${humanId}/department`, { department_id: departmentId ?? "" });

export const setHumanManager = (humanId: string, managerId: string | null) =>
  patch<{ ok: boolean }>(`/org/humans/${humanId}/manager`, { manager_id: managerId ?? "" });

export type Organization = {
  id: string;
  name: string;
  /** What this company does — master data, see spec/20. */
  description: string;
  /** Where the source of this platform lies (spec/21): target system and
   *  project. covey Doctor reads it there and reports there.
   *  Empty = not set up, and then nothing about it is in the prompt either. */
  // Computed by the server: an account for the platform's repository is
  // stored, so covey/create_issue can file. The address alone files nothing.
  platform_repo_can_file?: boolean;
  platform_repo_system: string;
  platform_repo_project: string;
  fleet_killed: boolean;
  human_count: number;
  agent_count: number;
  created_at: string;
};

/** A seat as the instance management sees it: in which organisation,
 *  in which role. */
export type Seat = {
  org_id: string;
  org_name: string;
  role: string;
};

/** A seat of the signed-in account — where the org switcher can lead (#262). */
export type Membership = {
  human_id: string;
  org_id: string;
  org_name: string;
  role: string;
};

/** A login of this installation. The level `platform_role` belongs to the
 *  instance, the roles in `seats` belong each to one organisation — that is
 *  the same difference as between Principal.PlatformRole and Principal.Role. */
export type Account = {
  id: string;
  email: string;
  display_name: string;
  email_verified_at?: string;
  platform_role: string;
  created_at: string;
  last_login_at?: string;
  seats: Seat[];
};

/** A flag of the installation together with its default value. The default
 *  comes along so the UI can show "unchanged" without keeping a second
 *  copy of the same table. */
export type Setting = {
  key: string;
  value: string;
  default: string;
  /** A secret flag (the SMTP password): `value` is then always empty,
   *  and `set` says the only thing the outside may learn — whether one
   *  is stored. */
  secret?: boolean;
  set?: boolean;
  /** The installation writes about itself (the result of the test mail).
   *  Shown, not offered. */
  read_only?: boolean;
};

/** A waitlist code — without plaintext, that one exists only in the moment of
 *  creation. */
export type WaitlistCode = {
  hash: string;
  label: string;
  max_uses: number;
  used_count: number;
  expires_at?: string;
  org_id?: string;
  email_pattern?: string;
  created_at: string;
  revoked_at?: string;
};

export type SetupStep = {
  text: string;
  items?: string[];
};

/** An engine: the code that drives the LLM loop. It declares which
 *  credentials it knows and how it needs them — and what it can do. */
/** The state of the setup (spec/20): what stands, and what is to be chosen. */
export type SetupState = {
  engine_done: boolean;
  org_done: boolean;
  people_done: boolean;
  people_id?: string;
  engines: RuntimeInfo[];
  org_name: string;
  org_description: string;
  /** Can the control plane personalise the HR department (stage 2)? */
  llm_available: boolean;
};

export type RuntimeInfo = {
  name: string;
  label: string;
  description: string;
  credentials: EngineCredential[];
  /** effort_levels: the reasoning-effort levels of this engine, ascending.
   *  Missing/empty = the engine does not know the knob — then it is not
   *  offered either. */
  /** models: the model ids this engine really drives. If the field is missing,
   *  it is NOT declared (engine in front of a single provider) — then the model
   *  stays free text. If it is there, it is at the same time the statement
   *  that the engine has no default. */
  capabilities: { resume: boolean; skills_dir?: string; effort_levels?: string[]; models?: string[] };
  setup: SetupStep[];
};

/** Exactly one of env_var and path is set: some engines take their
 *  credential as an environment variable, others as a file (spec/19). */
export type EngineCredential = {
  kind: "api_key" | "subscription";
  label: string;
  secret: string;
  env_var?: string;
  path?: string;
};

/** A runtime is a named workstation: engine plus the capacity to
 *  operate it. Agents are assigned to it (spec/18). */
export type RuntimeInstance = {
  id: string;
  engine: string;
  display_name: string;
  model: string;
  creds: RuntimeCredential[];
  bindings: RuntimeBinding[];
  /** Whether the engine can continue a session. Without that it carries no
   *  agent that waits for an answer. */
  can_carry_blocking: boolean;
};

/** ord IS the merit order: first the paid seats, then metered capacity. */
export type RuntimeCredential = {
  ord: number;
  kind: "api_key" | "subscription";
  secret_key: string;
  secret_slot: number;
  label: string;
  cooldown_until?: string;
  cooldown_reason?: string;
  /** Paused by hand since — holds until someone resumes. */
  paused_at?: string;
  limit: SecretLimit;
  usage: { ord: number; usd: number; tokens: number; runs: number };
  window_secs: number;
  /** true = every token costs money; false = a quota that is paid
   *  for anyway. Decides what the numbers mean. */
  metered: boolean;
  /** The number of the PROVIDER, where the engine can ask it — a measurement
   *  instead of our extrapolation. Percent 0..100, negative = not reported. */
  reported?: {
    window_percent: number;
    week_percent: number;
    window_resets?: string;
    week_resets?: string;
    stale: boolean;
  };
};

export type RuntimeBinding = {
  agent_id: string;
  ord: number;
  home_ord?: number;
  reason: string;
  bound_at: string;
};

// Target-system plugin: compiled built-in (registry), uploaded
// JSON manifest (kind=custom) or attached MCP server (kind=mcp),
// enableable per organisation.
export type TargetPlugin = {
  name: string;
  label: string;
  description: string;
  kind: "builtin" | "custom" | "mcp" | "wasm";
  // Category for the store filter — declared by the plugin itself (see
  // internal/target: CategoryTicketing …), empty/unknown = "other".
  category?: string;
  enabled: boolean;
  manifest?: { url?: string; tools?: MCPTool[]; auth?: { header?: string; format?: string } };
  updated_at?: string;
  setup_doc?: string;
  // The scopes the plugin understands in ACCESS.md — the UI offers
  // exactly these, instead of letting someone type a word that is then quietly
  // ignored. Empty for manifest/MCP plugins.
  scopes?: string[];
  // Where the plugin came from when it was installed from a catalogue
  // (spec/22). Empty = uploaded by hand or shipped.
  source?: string;
  source_version?: string;
  source_digest?: string;
};

// An entry in the plugin catalogue (GET /marketplace). The catalogue sits behind
// a configurable URL; what stands here is the entry plus what only
// this instance knows — whether it is installed and whether another version
// is ready.
export type MarketplaceEntry = {
  name: string;
  label: string;
  description: string;
  category?: string;
  kind: "builtin" | "custom" | "mcp" | "wasm";
  publisher: string;
  homepage: string;
  license: string;
  deprecated?: string;
  // The badge, embedded as a data:-URI. Never an address on a foreign
  // server: an image from there would be a tracking pixel that fires on every
  // call of the store page. The API admits only data:image/svg+xml|png|webp.
  icon?: string;
  version?: string;
  notes?: string;
  // Shipped from this covey version on — enable instead of install.
  builtin_since?: string;
  installed: boolean;
  installed_version?: string;
  update_available: boolean;
  // The name is already taken here, but not from this catalogue.
  installed_elsewhere?: boolean;
};

export type MarketplaceView = {
  enabled: boolean;
  source?: string;
  fetched_at?: string;
  entries: MarketplaceEntry[];
  // Stands BESIDE the entries, not instead of them: an unreachable
  // catalogue does not empty the page, but does not look healthy either.
  error?: string;
};

// A target system as an agent sees it (GET /agents/{id}/systems):
// plugin, access from ACCESS.md and the actions in the wording of its prompt.
// access=false means: the broker refuses the agent every request here,
// no matter whether the plugin is enabled for the organisation.
export type AgentSystem = {
  name: string;
  label: string;
  description?: string;
  kind: "builtin" | "custom" | "mcp";
  category?: string;
  enabled: boolean;
  access: boolean;
  scopes?: string[];
  /** Tool allowlist of the agent (MCP only); empty = all. */
  tools?: string[];
  /** Action list as it stands in the system prompt. */
  doc?: string;
};

// A tool offered by the MCP server (discovered from tools/list).
export type MCPTool = {
  name: string;
  description?: string;
  input_schema?: unknown;
};

// Egress: per-agent allowlist over reusable templates + own hosts,
// plus monitoring. defaults are always allowed (code/ENV).
export type EgressHost = { id: string; pattern: string; note: string };

export type EgressTemplate = {
  id: string;
  name: string;
  description: string;
  hosts: EgressHost[];
  agents: { id: string; slug: string; display_name: string }[];
  created_at: string;
};

// Status: enforcement flag, configurable base allowlist of the org (applies to
// alle Agenten) and ENV additions only changeable per config.
export type EgressStatus = { enforced: boolean; defaults: EgressHost[]; env: string[] };

// Built-in catalogue: curated host sets from the code, one click to take
// them over as an org's own template.
export type EgressBuiltin = {
  slug: string;
  name: string;
  description: string;
  hosts: { pattern: string; note: string }[];
  imported: boolean;
  template_id?: string;
};

export type EgressStats = {
  allowed_24h: number;
  blocked_24h: number;
  top_blocked: { host: string; count: number }[];
};

export type AgentEgress = { template_ids: string[]; hosts: EgressHost[] };

export type EgressLogEntry = {
  id: number;
  agent_id?: string;
  agent_slug: string;
  host: string;
  method: string;
  allowed: boolean;
  created_at: string;
};

// Request log (platform → Requests): the HTTP requests at the edges —
// incoming webhooks ("in") and outgoing target-system calls ("out").
// Bodies are cut short and redacted and only come along in the detail fetch.
export type RequestLogEntry = {
  id: number;
  created_at: string;
  agent_id?: string;
  agent_slug?: string;
  task_id?: string;
  direction: "in" | "out";
  system: string;
  method: string;
  url: string;
  status: number;
  duration_ms: number;
  req_bytes: number;
  resp_bytes: number;
  error?: string;
  remote?: string;
  req_body?: string;
  resp_body?: string;
  bodies_shown?: boolean;
};

export type RequestLogPage = {
  enabled: boolean;
  bodies: boolean;
  retention_hours: number;
  dropped: number;
  systems: string[];
  entries: RequestLogEntry[];
};

// A wiki page (spec/05): title + body (content) + [[wikilinks]]. content
// still carries the body (backwards compatibility of manual upkeep).
export type MemoryEntry = {
  id: string;
  slug: string;
  title: string;
  content: string;
  links?: string[];
  source?: string;
  type?: string; // kunde | projekt | system | person | problem | thema; empty = unclassified
  tags?: string[];
  score?: number;
  created_at: string;
  updated_at: string;
};

// A quality finding about the wiki of an agent (spec/05).
export type WikiFinding = {
  kind: "orphan" | "dead_link" | "untyped" | "episodic" | "duplicate" | "stub";
  slug: string;
  title?: string;
  detail?: string;
  score?: number;
  related?: string[];
};

// Metrics plus findings — the quality view on a wiki.
export type WikiHealth = {
  pages: number;
  links: number;
  orphans: number;
  dead_links: number;
  untyped: number;
  episodic: number;
  duplicate: number;
  stubs: number;
  findings: WikiFinding[];
};

// What an agent did to a page in the dream (spec/05). `before` carries
// the state before — undoing hangs on it.
export type DreamAction = {
  id: string;
  kind: "retitle" | "merge";
  page_slug?: string;
  before?: string;
  after?: string;
  reason?: string;
  undone_at?: string;
};

// A dream: the nightly (or hand-triggered) tidy-up run of the
// memory, with everything it did.
export type Dream = {
  id: string;
  agent_id: string;
  trigger: "manual" | "nightly";
  status: "running" | "done" | "error";
  error?: string;
  phase?: string;
  looked_at: number;
  skipped: number;
  // Dream narrative — decoration beside the protocol, not in its place.
  story?: string;
  started_at: string;
  finished_at?: string;
  actions: DreamAction[];
};

// An entry of the wiki log (log.md equivalent, spec/05).
export type WikiLogEntry = {
  id: number;
  op: string; // ingest | write | merge | delete
  page_slug?: string;
  summary: string;
  created_at: string;
};

// Secret preview: by default a viewable variable — value carries the
// full plaintext. With sensitive=true the value stays write-only, prefix
// shows only the first characters. agent_ids are the explicit assignments
// of an org secret — empty means: reaches no agent.
export type SecretPreview = {
  key: string;
  prefix: string;
  sensitive: boolean;
  value?: string;
  agent_ids: string[];
  values: SecretPoolValue[];
} & SecretLifetime;

// What the platform knows about a value, beyond the value itself (#176): when
// the target system stops accepting it, whether it already turned it away, what
// the last connection test saw. All optional — a value nothing is
// known about carries nothing.
export type SecretLifetime = {
  expires_at?: string;
  rejected_at?: string;
  rejected_reason?: string;
  probed_at?: string;
  probe_error?: string;
  probe_identity?: string;
  credential_id?: string;
  rotatable?: boolean;
  warned_at?: string;
};

// A key may carry several values (spec/04): several subscription seats, several
// bot accounts. Which agent sits on which one is decided by the selection in
// the control plane — sticky, until the value is exhausted or rejected.
export type SecretPoolValue = {
  slot: number;
  label: string;
  prefix: string;
  value?: string;
  sensitive: boolean;
  cooldown_until?: string;
  cooldown_reason?: string;
  limit: SecretLimit;
  updated_at: string;
} & SecretLifetime;

// window_secs = 0 means: no limit. amount is money or tokens depending on unit.
export type SecretLimit = {
  amount: number;
  unit: "usd" | "tokens";
  window_secs: number;
};

export type SecretPool = {
  key: string;
  values: (SecretPoolValue & {
    usage: { slot: number; usd: number; tokens: number; runs: number };
    window_secs: number;
  })[];
  bindings: {
    agent_id: string;
    slot: number;
    home_slot?: number;
    reason: string;
    bound_at: string;
  }[];
};

// A live check of known credentials right after saving.
export type SecretCheck = {
  checked: boolean;
  valid: boolean;
  hint?: string;
};

// A skill is an ability of the agent: a directory with SKILL.md and
// whatever else belongs. Only description stays permanently in the context of
// every run, the rest is loaded when the runtime pulls the skill.
//
// agent_id empty = skill of the org library; assigned_to are then the agents
// it is linked to (empty means: it reaches nobody). origin delivers the
// agent's view: "agent" (belongs to it) or "library" (linked).
export type SkillFile = { path: string; content: string };
export type Skill = {
  id: string;
  org_id: string;
  agent_id?: string;
  name: string;
  description: string;
  assigned_to?: string[];
  updated_at: string;
  files?: SkillFile[];
  origin?: "agent" | "library";
};

export const SKILL_ENTRY = "SKILL.md";

export type AgentTemplate = {
  id: string;
  org_id: string;
  name: string;
  description: string;
  bundle: unknown;
  created_by?: string;
  created_at: string;
  updated_at: string;
  /** Shipped, read-only template (embedded into the binary). */
  builtin?: boolean;
};

// Origin of the running binary (GET /version, internal/buildinfo): which
// build runs here? The foot of the sidebar shows it — after a deploy the
// first question. built_at is RFC3339 (UTC), commit/built_at can be empty
// when a build ran without git context.
export type BuildInfo = {
  version: string;
  commit: string;
  built_at: string;
  dirty: boolean;
  go: string;
  // Public source of this binary (AGPL-3.0). Comes from the server so a
  // fork shows its own address instead of that of the origin.
  source: string;
  // The same address as a target-system address: the default that covey
  // Doctor reports to as long as the organisation names no repository of its
  // own (spec/21). Empty when the source lies on no plugin that can check
  // out — then there is no default.
  source_system?: string;
  source_project?: string;
};

export const buildInfo = () => api<BuildInfo>("/version");


// First steps: the state of the organisation, not a progress the UI
// remembers (GET /onboarding). done=true → the checklist has
// nothing left to say and disappears.
export type OnboardingState = {
  steps: Array<{ key: string; done: boolean }>;
  done: boolean;
  // What stands between the platform and a running sandbox (missing
  // docker socket, sandbox image not built). Not a step of the list: here
  // nobody ticks away a missing image, the messages come finished
  // worded from the server and address the operator.
  data_plane?: { ready: boolean; problems?: string[] };
};

// An entry of the audit trail (GET /audit): who when touched what on the
// platform. Without request contents — those would hold secret values.
export type AuditEntry = {
  id: number;
  actor_email: string;
  actor_role: string;
  method: string;
  path: string;
  status: number;
  client_ip?: string;
  created_at: string;
};

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

/** A 404 from the API. On a detail page it means: not in the organisation this
 *  session works in — which a retry does not change (#263). */
export const isNotFound = (err: unknown) => err instanceof ApiError && err.status === 404;

/* What is to happen when the server rejects a request with 401: the
   session expired (or was ended elsewhere). That is not an error of ONE
   page, but the end of the whole UI — which is why the reaction
   does not hang on the calling component but here in one place.
   App.tsx logs out and switches to the login. Without this the shell
   stayed and filled itself with error messages. */
let abgelaufenMelden: (() => void) | null = null;

export function setUnauthorizedHandler(fn: (() => void) | null) {
  abgelaufenMelden = fn;
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  // With FormData the browser sets the Content-Type itself — including the
  // multipart boundary, which we do not even know. Overwriting it made
  // the upload unreadable.
  const isForm = init?.body instanceof FormData;
  const res = await fetch(`/api/v1${path}`, {
    headers: isForm ? undefined : { "Content-Type": "application/json" },
    ...init,
  });
  if (!res.ok) {
    /* The endpoints under /auth/ are excepted: there the 401 is the
       normal answer ("not signed in", "wrong password") and is handled by
       the login itself — a global abort would turn the login mask
       against itself. */
    if (res.status === 401 && !path.startsWith("/auth/")) abgelaufenMelden?.();
    let msg = res.statusText;
    try {
      const body = await res.json();
      if (body.error) msg = body.error;
    } catch {
      /* not JSON */
    }
    throw new ApiError(res.status, msg);
  }
  // 204/empty body (e.g. DELETE endpoints): res.json() would throw.
  if (res.status === 204) return undefined as T;
  return res.json();
}

export const post = <T>(path: string, body?: unknown) =>
  api<T>(path, { method: "POST", body: body ? JSON.stringify(body) : "{}" });
export const put = <T>(path: string, body: unknown) =>
  api<T>(path, { method: "PUT", body: JSON.stringify(body) });
export const patch = <T>(path: string, body: unknown) =>
  api<T>(path, { method: "PATCH", body: JSON.stringify(body) });
export const del = <T>(path: string) => api<T>(path, { method: "DELETE" });
export const upload = <T>(path: string, form: FormData) =>
  api<T>(path, { method: "POST", body: form });

// --- Workstation: the persistent home of an agent as a file tree ---

// How a file is to be shown. The server decides this in one place
// (internal/sandboxfs) — the UI then only picks the rendering.
export type PreviewKind = "text" | "markdown" | "image" | "pdf" | "csv" | "binary";

export type FileEntry = {
  name: string;
  /** Path relative to the home, "/" as separator. */
  path: string;
  is_dir: boolean;
  size: number;
  mode: string;
  mod_time: string;
  /** Target, when the entry is a symlink. */
  symlink?: string;
  /** The link points out of the home — visible, but not openable. */
  outside?: boolean;
  /** Preview kind by file name; empty = only decidable when opened. */
  preview?: PreviewKind;
};

export type FileListing = {
  // read_only: the home is read from the last snapshot because its runner
  // is not connected (spec/16). Writing is refused then.
  read_only?: boolean;
  read_only_reason?: string;
  path: string;
  /** false = the home was never created (agent never woken). */
  exists: boolean;
  truncated: boolean;
  entries: FileEntry[];
};

/** How much space the home of the agent takes — and which working copies
 *  eat it. Nothing measured this before: checkouts pile up in the
 *  persistent home until a run dies on a full overlay. */
export type FilesUsage = {
  exists: boolean;
  total_bytes: number;
  free_bytes: number;
  checkout_bytes: number;
  checkouts: { name: string; bytes: number; mod_time: string }[];
};

export type FileContent = {
  path: string;
  size: number;
  mode: string;
  mod_time: string;
  binary: boolean;
  truncated: boolean;
  /** text/markdown/csv carry content; image/pdf come over the preview endpoint. */
  preview: PreviewKind;
  content: string;
};

// A workplace from the catalogue of the server (spec/16): the image in which an
// agent works, plus what only the instance knows about it — which image lies
// behind it, where the address comes from and whether it already sits on a runner.
export type WorkplaceProvides = {
  profile: string;
  summary: string;
  tools: { name: string; version?: string; note?: string }[];
  sdk_dirs?: Record<string, string>;
  notes?: string[];
};

export type Workplace = {
  name: string;
  label: string;
  description: string;
  /** The address that is actually started; pinned to the digest from the catalogue. */
  image: string;
  /** The name under which the same image was published ("base-v0.4.0"). */
  tag?: string;
  platforms?: string[];
  build: string;
  dockerfile: string;
  default?: boolean;
  // available is missing when nobody could be asked — that is something
  // else than "not there" and must not look like it.
  available?: boolean;
  in_use: number;
  /* Where the address comes from: from the published catalogue, from an
     environment variable of this instance, or from the compiled-in
     default. Without this someone would have to guess between three sources
     when an image is not what they expected. */
  source?: "catalog" | "env" | "builtin";
  /* What the image says about itself — the same file the agent reads in
     its sandbox. Without it, "which workplace do I give this agent"
     is only answerable by reading a Dockerfile, and an agent that does not
     see its tools fetches them a second time (#102). Missing for an own
     workplace: there the organisation named the image,
     and what is in it, the platform does not know. */
  provides?: WorkplaceProvides;
  /* What fetching this image last cost — measured (the phase
     `image` from the recording), not estimated. Absent as long as nobody
     fetched it on a known host; that is something else than "costs
     nothing". */
  last_pull?: { bytes?: number; ms?: number; at: string };
  /** Brought in from the catalogue of the project or of this organisation. */
  kind?: "catalog" | "own";
  /** Who works here — named, not counted. */
  agents?: { id: string; slug: string; display_name: string }[];
  /* Which of them still runs an OLDER image: a sandbox keeps the image it
     started with, and a warm agent never starts again. A plugin fix therefore
     reaches it only at the next cold start — made visible, not fixed behind
     anybody's back (#217). */
  stale?: { id: string; slug: string; display_name: string }[];
};

/* Voices: an author's style as an object of the organisation (spec/24). Four
   parts — the profile (the bands the style gate measures), the passages (which
   stand in the prompt), the card (the description in words, written by a model
   and released by a person) and the contrast (what the author never does,
   measured against AI text). */
export type VoiceExemplar = { role: string; text: string; from?: string };
export type VoiceContrast = { metric: string; label: string; author: number; other: number };
export type VoiceDocument = {
  id: string;
  name: string;
  /** author = the author's own texts, reference = AI text the contrast is measured against. */
  kind: "author" | "reference";
  words: number;
  created_at: string;
};
export type Voice = {
  id: string;
  name: string;
  language: string;
  /** Counts the BUILDS. 0 = never built, and then nobody carries it. */
  version: number;
  exemplars: VoiceExemplar[];
  contrast: VoiceContrast[];
  notes: string[];
  /** The draft of the last build; released_card is what acts. */
  card: string;
  released_card: string;
  released_at?: string;
  words: number;
  documents: number;
  built_at?: string;
  agents?: { id: string; slug: string; display_name: string }[];
};
export type VoiceDetail = Voice & {
  corpus: VoiceDocument[];
  /** The rendered TONE.md — what the agent actually gets. */
  tone: string;
};

export const createWorkplace = (w: { name: string; label: string; description: string; image: string }) =>
  post<Workplace>("/workplaces", w);
export const deleteWorkplace = (name: string) => del<{ ok: boolean }>(`/workplaces/${name}`);

// Which images may run beside a sandbox (spec/16). The list belongs
// to the organisation: NAMING an image is not the privilege, extending the
// list is.
export type ServiceImagePattern = {
  id: string;
  pattern: string;
  note: string;
  created_at: string;
};
export const listServiceImages = () => api<ServiceImagePattern[]>("/service-images");
export const addServiceImage = (pattern: string, note: string) =>
  post<ServiceImagePattern>("/service-images", { pattern, note });
export const deleteServiceImage = (id: string) => del<{ ok: boolean }>(`/service-images/${id}`);

// AI assistant for adjusting agents (config copilot, FR-001).
export type AssistMessage = { role: "user" | "assistant"; content: string };
export type AssistProposal = { file: string; content: string };
export type AssistReply = { reply: string; proposals: AssistProposal[] };

export const assistStatus = () =>
  api<{ available: boolean }>("/assist/status");
export const configAssist = (agentId: string, messages: AssistMessage[], files: Record<string, string>) =>
  post<AssistReply>(`/agents/${agentId}/config/assist`, { messages, files });

/* The roles in display order. The label stands NOT here, but in the language
   files under role.<role> — a list of German labels at this place was the
   reason why the English UI showed "Plattform-Admin" — today the top org role
   is called org_admin. */
export const ROLES = [
  "org_admin",
  "agent_owner",
  "security",
  "auditor",
  "controlling",
] as const;


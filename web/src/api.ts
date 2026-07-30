// Schmaler, typisierter Client für die Covey-API (Session-Cookie-Auth).

export type Principal = {
  ID: string;
  OrgID: string;
  Email: string;
  DisplayName: string;
  Role: string;
};

export type Agent = {
  id: string;
  slug: string;
  display_name: string;
  runtime: string;
  model: string;
  max_turns: number;
  recording_level: string; // "" = erbt Org-Boden, sonst minimal|standard|full
  warm_sandbox: boolean; // hält die Sandbox zwischen Wach-Phasen live (opt-in)
  status: string;
  supervisor_id?: string;
  department_id?: string;
  // Mitarbeiter-Profil — dieselben Felder wie bei Human (Agenten sind
  // Mitarbeiter): Funktion, Kontakt, Plattform-Kennungen, konfigurierbare Felder.
  job_title: string;
  identities: Record<string, string>;
  phone: string;
  responsibilities: string;
  custom: Record<string, string>;
  killed: boolean;
  budget_usd: number;
  created_at: string;
};

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
  // Aufgabe, aus der diese hervorging: Teilaufgabe/Delegation (origin
  // "agent:<slug>") oder Fortsetzung eines am Turn-Limit abgebrochenen Laufs
  // (origin "continuation:<id>").
  parent_task_id?: string;
  archived_at?: string;
  created_at: string;
  updated_at: string;
};

export type Stage = {
  id: string;
  agent_id: string;
  name: string;
  position: number;
  color: string;
  // 'agent'-Spalten legt der Agent selbst an; sie verschwinden automatisch,
  // sobald sie leer sind. 'human'-Spalten bleiben stehen.
  created_by: string;
  created_at: string;
};

// TaskNote ist eine proaktive Notiz des Agenten an einer Aufgabe
// (Zwischenstände, Befunde) — GET /tasks/{id}/notes.
export type TaskNote = {
  id: string;
  task_id: string;
  author: string;
  content: string;
  created_at: string;
};

export type ConfigVersion = {
  version: number;
  // ACCESS.md und EGRESS.md rendert der Server live aus den UI-Stores
  // (Tools/Egress); Speichern schreibt sie dorthin zurück — eine Quelle.
  files: Record<string, string>;
  compiled_prompt: string;
  created_at: string;
};

// Monitoring-Sicht auf einen HEARTBEAT.md-Eintrag: Zeitplan, letzter und
// nächster Lauf (Serverzeit-Semantik, ISO-Timestamps), pending = Aufgabe des
// letzten Laufs noch nicht terminal (dann wird nicht neu gefeuert).
export type HeartbeatStatus = {
  name: string;
  task: string;
  every_seconds?: number;
  daily_at?: string;
  only_if?: string;
  source?: string; // "config" (HEARTBEAT.md) | "system" (Plattform-Default, z.B. Wiki-Pflege)
  last_fired_at: string;
  next_run: string;
  pending: boolean;
};

// Optionaler generischer Webhook-Trigger des Agenten (Wake-Quelle Event):
// POST auf die URL legt eine Backlog-Aufgabe an und weckt den Agenten.
// Nur für Manager-Rollen abrufbar — das Token ist das Geheimnis.
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

// recordingBlobURL zeigt auf ein Recording-Artefakt (z. B. Screenshot). Same-
// origin, deshalb trägt ein <img> das Session-Cookie automatisch mit.
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

// Ergebnis des Regel-Testers (POST /guardrails/test): trocken ausgewertet,
// nichts wird ausgeführt.
export type GuardrailVerdict = {
  subject: string;
  decision: "allow" | "deny" | "require_approval";
  rule?: Guardrail;
  budget_limit_usd?: number;
};

export type CostSummary = {
  agent_id: string;
  total_usd: number;
  input_tokens: number;
  output_tokens: number;
  entries: number;
};

export type CostBucket = {
  period: string;
  total_usd: number;
  input_tokens: number;
  output_tokens: number;
  entries: number;
};

export type AgentCost = {
  agent_id: string;
  slug: string;
  display_name: string;
  total_usd: number;
  input_tokens: number;
  output_tokens: number;
  entries: number;
};

export type ModelCost = {
  model: string;
  total_usd: number;
  input_tokens: number;
  output_tokens: number;
  entries: number;
};

export type OrgCostReport = {
  total_usd: number;
  input_tokens: number;
  output_tokens: number;
  entries: number;
  bucket: string;
  series: CostBucket[] | null;
  agents: AgentCost[] | null;
  models: ModelCost[] | null;
};

export type Human = {
  id: string;
  org_id: string;
  email: string;
  display_name: string;
  role: string;
  manager_id?: string;
  department_id?: string;
  // Mitarbeiter-Profil: Funktion, Kontakt, Zuständigkeiten und die
  // Plattform-Kennungen (generisch: system → kennung, z. B. {"gitlab": "maxm"}).
  job_title: string;
  identities: Record<string, string>;
  phone: string;
  responsibilities: string;
  // Werte der org-weit konfigurierbaren Profilfelder (key → wert).
  custom: Record<string, string>;
  created_at: string;
};

// Leitung einer Abteilung: ein Mensch oder ein Agent — eine Abteilung kann
// mehrere Leitungen haben, eine Leitung mehrere Abteilungen.
export type DeptLead = { kind: "human" | "agent"; id: string };

export type Department = {
  id: string;
  org_id: string;
  name: string;
  description: string;
  color: string; // Hex-Akzentfarbe, leer = Standard
  leads: DeptLead[];
  created_at: string;
};

// Definition eines org-weit konfigurierbaren Profilfelds (Organisationen-Seite).
export type ProfileField = {
  id: string;
  key: string;
  label: string;
  created_at: string;
};

// Org-Chart (spec/02, spec/09): Menschen & Agenten samt Vorgesetzten-Beziehungen.
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
  fleet_killed: boolean;
  human_count: number;
  agent_count: number;
  created_at: string;
};

export type SetupStep = {
  text: string;
  items?: string[];
};

export type RuntimeInfo = {
  name: string;
  label: string;
  description: string;
  needs_credential: boolean;
  setup: SetupStep[];
};

// Zielsystem-Plugin: kompiliertes Built-in (Registry), hochgeladenes
// JSON-Manifest (kind=custom) oder angebundener MCP-Server (kind=mcp),
// pro Organisation aktivierbar.
export type TargetPlugin = {
  name: string;
  label: string;
  description: string;
  kind: "builtin" | "custom" | "mcp";
  // Kategorie fürs Store-Filter — vom Plugin selbst deklariert (siehe
  // internal/target: CategoryTicketing …), leer/unbekannt = "other".
  category?: string;
  enabled: boolean;
  manifest?: { url?: string; tools?: MCPTool[]; auth?: { header?: string; format?: string } };
  updated_at?: string;
  setup_doc?: string;
};

// Ein vom MCP-Server angebotenes Werkzeug (aus tools/list entdeckt).
export type MCPTool = {
  name: string;
  description?: string;
  input_schema?: unknown;
};

// Egress: per-Agent-Allowlist über wiederverwendbare Templates + eigene Hosts,
// plus Monitoring. defaults sind fest erlaubt (Code/ENV).
export type EgressHost = { id: string; pattern: string; note: string };

export type EgressTemplate = {
  id: string;
  name: string;
  description: string;
  hosts: EgressHost[];
  agents: { id: string; slug: string; display_name: string }[];
  created_at: string;
};

// Status: Enforcement-Flag, konfigurierbare Basis-Allowlist der Org (gilt für
// alle Agenten) und nur per Config änderbare ENV-Zusätze.
export type EgressStatus = { enforced: boolean; defaults: EgressHost[]; env: string[] };

// Built-in-Katalog: kuratierte Host-Sets aus dem Code, per Klick als
// org-eigenes Template übernehmbar.
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

// Request-Log (Plattform → Requests): die HTTP-Requests an den Rändern —
// eingehende Webhooks ("in") und ausgehende Zielsystem-Aufrufe ("out").
// Bodies sind gekappt und redigiert und kommen erst im Detail-Abruf mit.
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

// Eine Wiki-Seite (spec/05): title + body (content) + [[wikilinks]]. content
// trägt weiter den Body (Rückwärtskompatibilität der manuellen Pflege).
export type MemoryEntry = {
  id: string;
  slug: string;
  title: string;
  content: string;
  links?: string[];
  source?: string;
  score?: number;
  created_at: string;
  updated_at: string;
};

// Ein Eintrag des Wiki-Protokolls (log.md-Äquivalent, spec/05).
export type WikiLogEntry = {
  id: number;
  op: string; // ingest | write | merge | delete
  page_slug?: string;
  summary: string;
  created_at: string;
};

// Secret-Vorschau: per Default eine einsehbare Variable — value trägt den
// vollen Klartext. Bei sensitive=true bleibt der Wert write-only, prefix
// zeigt nur die ersten Zeichen. agent_ids sind die expliziten Zuweisungen
// eines Org-Secrets — leer heißt: erreicht keinen Agenten.
export type SecretPreview = {
  key: string;
  prefix: string;
  sensitive: boolean;
  value?: string;
  agent_ids: string[];
};

// Ein Live-Check bekannter Credentials direkt nach dem Speichern.
export type SecretCheck = {
  checked: boolean;
  valid: boolean;
  hint?: string;
};

export type AgentTemplate = {
  id: string;
  org_id: string;
  name: string;
  description: string;
  bundle: unknown;
  created_by?: string;
  created_at: string;
  updated_at: string;
  /** Mitgelieferte, schreibgeschützte Vorlage (fest ins Binary eingebettet). */
  builtin?: boolean;
};

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const body = await res.json();
      if (body.error) msg = body.error;
    } catch {
      /* kein JSON */
    }
    throw new ApiError(res.status, msg);
  }
  // 204/leerer Body (z. B. DELETE-Endpunkte): res.json() würde werfen.
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

// KI-Assistent zum Anpassen von Agenten (Config-Copilot, FR-001).
export type AssistMessage = { role: "user" | "assistant"; content: string };
export type AssistProposal = { file: string; content: string };
export type AssistReply = { reply: string; proposals: AssistProposal[] };

export const assistStatus = () =>
  api<{ available: boolean }>("/assist/status");
export const configAssist = (agentId: string, messages: AssistMessage[], files: Record<string, string>) =>
  post<AssistReply>(`/agents/${agentId}/config/assist`, { messages, files });

export const roleLabel: Record<string, string> = {
  platform_admin: "Plattform-Admin",
  agent_owner: "Agent-Owner",
  security: "Security",
  auditor: "Auditor",
  controlling: "Controlling",
};

export const statusLabel: Record<string, string> = {
  sleeping: "schläft",
  triggered: "geweckt",
  triage: "triage",
  working: "arbeitet",
  killed: "gestoppt",
  open: "offen",
  in_progress: "in Arbeit",
  blocked: "wartet",
  done: "erledigt",
  failed: "fehlgeschlagen",
  cancelled: "verworfen",
  pending: "ausstehend",
  approved: "freigegeben",
  denied: "abgelehnt",
};

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
  status: string;
  supervisor_id?: string;
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
  created_at: string;
};

export type ConfigVersion = {
  version: number;
  files: Record<string, string>;
  compiled_prompt: string;
  created_at: string;
};

export type RecordingEvent = {
  id: number;
  task_id?: string;
  kind: string;
  payload: unknown;
  created_at: string;
};

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
  enabled: boolean;
};

export type CostSummary = {
  agent_id: string;
  total_usd: number;
  input_tokens: number;
  output_tokens: number;
  entries: number;
};

export type Human = {
  id: string;
  org_id: string;
  email: string;
  display_name: string;
  role: string;
  manager_id?: string;
  created_at: string;
};

// Org-Chart (spec/02, spec/09): Menschen & Agenten samt Vorgesetzten-Beziehungen.
export type OrgChart = {
  humans: Human[];
  agents: Agent[];
};

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

export type MemoryEntry = {
  id: string;
  content: string;
  score?: number;
  created_at: string;
};

// Secret-Vorschau (write-only API): Name + kurzes Wert-Präfix. agent_ids sind
// die expliziten Zuweisungen eines Org-Secrets — leer heißt: alle Agenten.
export type SecretPreview = {
  key: string;
  prefix: string;
  agent_ids: string[];
};

// Ein Live-Check bekannter Credentials direkt nach dem Speichern.
export type SecretCheck = {
  checked: boolean;
  valid: boolean;
  hint?: string;
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
  return res.json();
}

export const post = <T>(path: string, body?: unknown) =>
  api<T>(path, { method: "POST", body: body ? JSON.stringify(body) : "{}" });
export const put = <T>(path: string, body: unknown) =>
  api<T>(path, { method: "PUT", body: JSON.stringify(body) });
export const patch = <T>(path: string, body: unknown) =>
  api<T>(path, { method: "PATCH", body: JSON.stringify(body) });
export const del = <T>(path: string) => api<T>(path, { method: "DELETE" });

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

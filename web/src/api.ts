// Schmaler, typisierter Client für die Covey-API (Session-Cookie-Auth).

export type Principal = {
  ID: string;
  OrgID: string;
  Email: string;
  DisplayName: string;
  Role: string;
  /* Das Konto hinter der Anmeldung — eine Person, über Organisationen hinweg. */
  AccountID: string;
  /* Die Instanz-Ebene: "user" oder "system_admin". Ausdrücklich KEINE
     Organisations-Rolle — org_admin vergibt jede Organisation an sich
     selbst, das hier niemand (FR-003, Befund F). */
  PlatformRole: string;
};

/** Verwaltet diese Person die Installation selbst? */
export const istSystemAdmin = (me: Principal) => me.PlatformRole === "system_admin";

export type Agent = {
  id: string;
  slug: string;
  display_name: string;
  runtime: string;
  /** runtime_id: der Sitz, auf dem der Agent wirklich arbeitet. Fehlt =
   *  keiner zugewiesen. Getrennt von `runtime` (der ENGINE), und genau darum
   *  können die beiden auseinanderlaufen — wer sie gleichsetzt, zeigt einen
   *  Zustand an, den es so nicht gibt. */
  runtime_id?: string;
  model: string;
  effort: string; // "" = Runtime-Default, sonst low|medium|high|xhigh|max
  max_turns: number;
  recording_level: string; // "" = inherits the org floor, otherwise minimal|standard|full
  // How long this agent's verbatim run is kept (spec/06). null/undefined =
  // inherits the organisation; a number only ever EXTENDS it, never shortens.
  // 0 = keep forever.
  recording_retention_days?: number | null;
  // Der Arbeitsplatz: Profilname (base, dev) oder ein eigenes Image;
  // leer = Voreinstellung der Instanz (spec/16).
  sandbox_image: string;
  // Welche Fähigkeiten der Host haben muss (arm64, gpu, ein Runner im Netz des
  // Zielsystems). Leer = jeder Runner der Organisation (spec/16).
  runner_tags?: string[];
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
  // Der erste Arbeitstag. Fehlt er, ist der Agent ein Entwurf: angelegt,
  // konfigurierbar, aber nicht dispatcht — kein Heartbeat, kein scharfer
  // Webhook, keine Sandbox, keine Kosten (spec/20).
  hired_at?: string;
  created_at: string;
};

/** Entwurf: angelegt, aber noch nicht eingestellt. */
export const isDraft = (a: Agent) => !a.hired_at;

/** Einstellen — der eine Weg aus dem Entwurf, und ihn geht ein Mensch. */
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
  // Aufgabe, aus der diese hervorging: Teilaufgabe/Delegation (origin
  // "agent:<slug>") oder Fortsetzung eines am Turn-Limit abgebrochenen Laufs
  // (origin "continuation:<id>").
  parent_task_id?: string;
  archived_at?: string;
  created_at: string;
  updated_at: string;
  // Was der Lauf dieser Aufgabe gekostet hat, in USD, und aus wie vielen
  // Kostenbuchungen (Turns) er sich zusammensetzt. Fehlt, solange die Aufgabe
  // nichts gekostet hat — das ist nicht 0,00 $, sondern „noch nicht gelaufen".
  cost_usd?: number;
  cost_entries?: number;
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

// Ein offener Punkt aus dem Betrieb (spec/21). Drei Sorten, eine Liste, weil
// alle drei denselben Menschen brauchen: der Vorschlag mit Diff, der Befund
// ohne einen (den Auftrag eines Kollegen kann nur der Mensch ändern, der ihn
// verantwortet) und das Issue, das schon im Tracker liegt.
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
  // Vom Server angereichert:
  agent_slug: string;
  agent_name: string;
  agent_owner_id?: string;
  author_slug?: string;
  author_name?: string;
  current_version: number;
  // Gegen eine ältere Version geschrieben. Für sich noch kein Hinderungsgrund.
  stale: boolean;
  // Dateien, die seit der Basis von jemand anderem geändert wurden. Solange
  // die Liste nicht leer ist, wird der Vorschlag nicht angenommen.
  conflicts?: string[];
  // Fasst ACCESS.md oder EGRESS.md an — dann entscheidet org_admin oder
  // security, nicht der Teamleiter, dem der Agent gehört (spec/02).
  needs_security: boolean;
  diff?: { file: string; before: string; after: string }[];
  // Nur beim Issue: wo der Bericht schon liegt.
  link?: string;
};

export const decideImprovement = (id: string, accept: boolean, note: string) =>
  post<ImprovementItem>(`/improvements/${id}/decide`, { accept, note });

export const decideApproval = (id: string, approve: boolean) =>
  post<Approval>(`/approvals/${id}/decide`, { approve });

// Eine Zeile des Posteingangs: Freigabe oder offener Punkt. Der Kopf ist für
// beide gleich, damit serverseitig sortiert und geblättert werden kann; das
// Sortenspezifische hängt unverändert darunter.
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
  /** Alle Zeilen, auf die die Filter passen — daran hängt „mehr laden". */
  total: number;
  /** Die offenen unter denselben Filtern, ohne den Statusfilter (Zähler). */
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

// Ergebnis des Regel-Testers (POST /guardrails/test): trocken ausgewertet,
// nichts wird ausgeführt.
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

/** Was EIN Lauf gekostet hat (observability.RunCost). Die Aggregate sagen, wie
 *  teuer der Tag war — diese Liste sagt, welcher Lauf ihn teuer gemacht hat.
 *  `actions` ist die Spalte neben dem Geld: ein Lauf mit actions=0 hat nichts
 *  außerhalb seiner selbst verändert, sieht in jeder Summe aber aus wie einer,
 *  der drei Fehler behoben hat. */
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
  /** Aufschlüsselung pro Pool-Wert — leer, solange kein Schlüssel mehrere
   *  Werte trägt. Läufe von vor den Pools tragen keine Zuordnung und fehlen
   *  hier; sie zählen weiter in die Summen. */
  credentials: CredentialCost[] | null;
};

export type CredentialCost = Tokens & {
  secret_key: string;
  slot: number;
  label: string;
  total_usd: number;
  entries: number;
};

/** Eine Zeile der Preisliste (spec/17-kpis.md): wie oft die Kennzahl im
 *  Zeitraum zählte und was eine Einheit davon gekostet hat.
 *
 *  unit_usd fehlt unter einer Mindestmenge — ein Stückpreis aus drei
 *  Ereignissen ist Rauschen und stünde als Zahl gleichberechtigt neben einem
 *  aus dreihundert. Die Spalte darf man NICHT aufsummieren: jede Zeile teilt
 *  die vollen Kosten durch die Anzahl ihrer eigenen Kennzahl. */
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
  /** Objekte, die ein ZWEITER Lauf nochmal anfassen musste — die
   *  Nacharbeitsquote. Nur mit `je:` messbar; ohne Objekt-Identität bleibt sie
   *  0 und wird ausgeblendet, statt eine nie gemessene Qualität zu behaupten. */
  returned?: number;
  /** Dieselben Zahlen für den gleich langen Zeitraum davor — der Trend.
   *  Bewusst die Rohwerte statt einer fertigen Prozentzahl: die Richtung ist
   *  nicht für beide dieselbe Nachricht. Ein sinkender Stückpreis ist eine
   *  Verbesserung; doppelt so viele Tickets können doppelte Leistung oder
   *  doppelter Posteingang sein. */
  prev_count: number;
  prev_unit_usd?: number;
  /** Der Verlauf über den Zeitraum in festen Abschnitten (Sparkline). Mit
   *  `je:` summieren sich die Abschnitte NICHT zur Gesamtzahl — dasselbe
   *  Objekt in zwei Abschnitten zählt in beiden. */
  series?: number[];
};

/** Die Zahlen, die den Preis qualifizieren. Ein Preis sagt, was ein Ergebnis
 *  gekostet hat, nicht ob es taugte. */
export type Quality = {
  /** Von Menschen entschiedene Approval-Gates und davon abgelehnte — die
   *  einzige Zahl hier, die kein Proxy ist. */
  decided: number;
  denied: number;
  /** Median der Zeit vom eingehenden Ereignis bis zur ersten Aktion des Laufs.
   *  Median, nicht Mittelwert: ein einzelner Hänger darf das Bild nicht
   *  färben. */
  response_seconds?: number;
};

export type IndicatorReport = {
  indicators: IndicatorResult[] | null;
  /** Läufe, die ohne Ergebnis endeten — die Gegenzahl, ohne die die
   *  Preisliste Drückebergerei belohnt. */
  failed: number;
  /** Der Nenner hinter jedem Preis, damit die Zahlen prüfbar sind. */
  total_usd: number;
  quality: Quality;
};

/** Ein Befund des Config-Lints (internal/agents/lint.go). Warnungen mit
 *  Kontext, keine harten Fehler: eine 2-Minuten-Frequenz ist für ein Postfach
 *  in Ordnung und für einen Repo-Klon ruinös. */
export type LintFinding = {
  agent_slug: string;
  rule: string;
  severity: "warn" | "info";
  file?: string;
  line?: number;
  message: string;
  hint: string;
};

// Die Arbeitsakte eines Kollegen (spec/21): acht Abschnitte aus acht benannten
// Quellen. Kein Freitext eines Agenten oder eines Zielsystems — mit einer
// Ausnahme, den Aufgabentiteln, die aus der Weck-Quelle stammen können.
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
  // Was gekürzt wurde. Eine Akte, die still bei 200 Aufgaben aufhört, liest
  // sich wie eine vollständige.
  notes?: string[];
};

// Ein Review: was der Betrieb über einen Kollegen geschrieben hat, datiert
// (spec/21). Es wartet auf nichts — anders als ein offener Punkt braucht es
// keine Entscheidung, sondern einen Leser.
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
  /** Was dieses Unternehmen macht — Stammdaten, siehe spec/20. */
  description: string;
  /** Wo der Quelltext dieser Plattform liegt (spec/21): Zielsystem und
   *  Projekt. Covey Doctor liest ihn dort und meldet dorthin.
   *  Leer = nicht eingerichtet, und dann steht davon auch nichts im Prompt. */
  platform_repo_system: string;
  platform_repo_project: string;
  fleet_killed: boolean;
  human_count: number;
  agent_count: number;
  created_at: string;
};

/** Ein Sitz, wie ihn die Instanz-Verwaltung sieht: in welcher Organisation,
 *  in welcher Rolle. */
export type Seat = {
  org_id: string;
  org_name: string;
  role: string;
};

/** Eine Anmeldung dieser Installation. Die Ebene `platform_role` gehört der
 *  Instanz, die Rollen in `seats` gehören je einer Organisation — das ist
 *  derselbe Unterschied wie zwischen Principal.PlatformRole und Principal.Role. */
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

/** Ein Schalter der Installation samt seines Vorgabewerts. Der Vorgabewert
 *  kommt mit, damit die Oberfläche "unverändert" zeigen kann, ohne eine zweite
 *  Kopie derselben Tabelle zu führen. */
export type Setting = {
  key: string;
  value: string;
  default: string;
};

/** Ein Wartelisten-Code — ohne Klartext, den gibt es nur im Moment der
 *  Erzeugung. */
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

/** Eine Engine: der Code, der die LLM-Schleife fährt. Sie deklariert, welche
 *  Credentials sie kennt und wie sie sie braucht — und was sie kann. */
/** Der Zustand der Einrichtung (spec/20): was steht, und was zu wählen ist. */
export type SetupState = {
  engine_done: boolean;
  org_done: boolean;
  people_done: boolean;
  people_id?: string;
  engines: RuntimeInfo[];
  org_name: string;
  org_description: string;
  /** Kann die Control Plane die Personalabteilung personalisieren (Stufe 2)? */
  llm_available: boolean;
};

export type RuntimeInfo = {
  name: string;
  label: string;
  description: string;
  credentials: EngineCredential[];
  /** effort_levels: die Denkaufwand-Stufen dieser Engine, aufsteigend.
   *  Fehlt/leer = die Engine kennt den Regler nicht — dann wird er auch nicht
   *  angeboten. */
  /** models: die Modell-Ids, die diese Engine wirklich fährt. Fehlt das Feld,
   *  ist es NICHT deklariert (Engine vor einem einzelnen Anbieter) — dann bleibt
   *  das Modell ein Freitext. Ist es da, ist es zugleich die Aussage, dass die
   *  Engine keinen Default hat. */
  capabilities: { resume: boolean; skills_dir?: string; effort_levels?: string[]; models?: string[] };
  setup: SetupStep[];
};

/** Genau eines von env_var und path ist gesetzt: die einen Engines nehmen ihr
 *  Credential als Umgebungsvariable, die anderen als Datei (spec/19). */
export type EngineCredential = {
  kind: "api_key" | "subscription";
  label: string;
  secret: string;
  env_var?: string;
  path?: string;
};

/** Eine Runtime ist ein benannter Arbeitsplatz: Engine plus die Kapazität, sie
 *  zu betreiben. Agenten werden ihr zugewiesen (spec/18). */
export type RuntimeInstance = {
  id: string;
  engine: string;
  display_name: string;
  model: string;
  creds: RuntimeCredential[];
  bindings: RuntimeBinding[];
  /** Ob die Engine eine Sitzung fortsetzen kann. Ohne das trägt sie keinen
   *  Agenten, der auf eine Antwort wartet. */
  can_carry_blocking: boolean;
};

/** ord IST die Merit Order: erst die bezahlten Sitze, dann metered Kapazität. */
export type RuntimeCredential = {
  ord: number;
  kind: "api_key" | "subscription";
  secret_key: string;
  secret_slot: number;
  label: string;
  cooldown_until?: string;
  cooldown_reason?: string;
  limit: SecretLimit;
  usage: { ord: number; usd: number; tokens: number; runs: number };
  window_secs: number;
  /** true = jedes Token kostet Geld; false = ein Kontingent, das ohnehin
   *  bezahlt ist. Entscheidet, was die Zahlen bedeuten. */
  metered: boolean;
  /** Die Zahl des ANBIETERS, wo die Engine sie erfragen kann — eine Messung
   *  statt unserer Hochrechnung. Prozente 0..100, negativ = nicht gemeldet. */
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

// Zielsystem-Plugin: kompiliertes Built-in (Registry), hochgeladenes
// JSON-Manifest (kind=custom) oder angebundener MCP-Server (kind=mcp),
// pro Organisation aktivierbar.
export type TargetPlugin = {
  name: string;
  label: string;
  description: string;
  kind: "builtin" | "custom" | "mcp" | "wasm";
  // Kategorie fürs Store-Filter — vom Plugin selbst deklariert (siehe
  // internal/target: CategoryTicketing …), leer/unbekannt = "other".
  category?: string;
  enabled: boolean;
  manifest?: { url?: string; tools?: MCPTool[]; auth?: { header?: string; format?: string } };
  updated_at?: string;
  setup_doc?: string;
  // Die Scopes, die das Plugin in ACCESS.md versteht — die Oberfläche bietet
  // genau diese an, statt jemanden ein Wort tippen zu lassen, das dann still
  // ignoriert wird. Leer bei Manifest-/MCP-Plugins.
  scopes?: string[];
  // Woher das Plugin kam, wenn es aus einem Katalog installiert wurde
  // (spec/22). Leer = von Hand hochgeladen oder mitgeliefert.
  source?: string;
  source_version?: string;
  source_digest?: string;
};

// Ein Eintrag im Plugin-Katalog (GET /marketplace). Der Katalog liegt hinter
// einer konfigurierbaren URL; was hier steht, ist der Eintrag plus das, was nur
// diese Instanz weiß — ob er installiert ist und ob eine andere Version
// bereitliegt.
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
  // Das Signet, eingebettet als data:-URI. Nie eine Adresse auf einem fremden
  // Server: ein Bild von dort wäre ein Zählpixel, das bei jedem Aufruf der
  // Store-Seite feuert. Die API lässt nur data:image/svg+xml|png|webp durch.
  icon?: string;
  version?: string;
  notes?: string;
  // Ab dieser Covey-Fassung mitgeliefert — aktivieren statt installieren.
  builtin_since?: string;
  installed: boolean;
  installed_version?: string;
  update_available: boolean;
  // Der Name ist hier schon belegt, aber nicht aus diesem Katalog.
  installed_elsewhere?: boolean;
};

export type MarketplaceView = {
  enabled: boolean;
  source?: string;
  fetched_at?: string;
  entries: MarketplaceEntry[];
  // Steht NEBEN den Einträgen, nicht statt ihrer: ein nicht erreichbarer
  // Katalog leert die Seite nicht, sieht aber auch nicht gesund aus.
  error?: string;
};

// Ein Zielsystem aus der Sicht eines Agenten (GET /agents/{id}/systems):
// Plugin, Zugang aus ACCESS.md und die Aktionen im Wortlaut seines Prompts.
// access=false heißt: der Broker verweigert dem Agenten hier jede Anfrage,
// egal ob das Plugin für die Organisation aktiviert ist.
export type AgentSystem = {
  name: string;
  label: string;
  description?: string;
  kind: "builtin" | "custom" | "mcp";
  category?: string;
  enabled: boolean;
  access: boolean;
  scopes?: string[];
  /** Werkzeug-Allowlist des Agenten (nur MCP); leer = alle. */
  tools?: string[];
  /** Aktionsliste, wie sie im System-Prompt steht. */
  doc?: string;
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
  type?: string; // kunde | projekt | system | person | problem | thema; leer = nicht eingeordnet
  tags?: string[];
  score?: number;
  created_at: string;
  updated_at: string;
};

// Ein Qualitätsbefund über das Wiki eines Agenten (spec/05).
export type WikiFinding = {
  kind: "orphan" | "dead_link" | "untyped" | "episodic" | "duplicate" | "stub";
  slug: string;
  title?: string;
  detail?: string;
  score?: number;
  related?: string[];
};

// Kennzahlen plus Befunde — die Qualitätssicht auf ein Wiki.
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

// Was ein Agent im Traum mit einer Seite gemacht hat (spec/05). `before` trägt
// den Zustand davor — daran hängt das Rückgängigmachen.
export type DreamAction = {
  id: string;
  kind: "retitle" | "merge";
  page_slug?: string;
  before?: string;
  after?: string;
  reason?: string;
  undone_at?: string;
};

// Ein Traum: der nächtliche (oder von Hand angestoßene) Aufräumlauf des
// Gedächtnisses, samt allem, was er getan hat.
export type Dream = {
  id: string;
  agent_id: string;
  trigger: "manual" | "nightly";
  status: "running" | "done" | "error";
  error?: string;
  phase?: string;
  looked_at: number;
  skipped: number;
  // Traumerzählung — Zierrat neben dem Protokoll, nicht an dessen Stelle.
  story?: string;
  started_at: string;
  finished_at?: string;
  actions: DreamAction[];
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
  values: SecretPoolValue[];
};

// Ein Schlüssel darf mehrere Werte tragen (spec/04): mehrere Abo-Sitze, mehrere
// Bot-Konten. Welcher Agent auf welchem sitzt, entscheidet die Auswahl in der
// Control Plane — klebrig, bis der Wert erschöpft oder abgewiesen ist.
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
};

// window_secs = 0 heißt: kein Limit. amount ist je nach unit Geld oder Tokens.
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

// Ein Live-Check bekannter Credentials direkt nach dem Speichern.
export type SecretCheck = {
  checked: boolean;
  valid: boolean;
  hint?: string;
};

// Ein Skill ist eine Fähigkeit des Agenten: ein Verzeichnis mit SKILL.md und
// beliebigem Beiwerk. Nur description steht dauerhaft im Kontext jedes Laufs,
// der Rest wird geladen, wenn die Runtime den Skill zieht.
//
// agent_id leer = Skill der Org-Bibliothek; assigned_to sind dann die Agenten,
// denen er verlinkt ist (leer heißt: er erreicht niemanden). origin liefert die
// Agenten-Sicht mit: "agent" (gehört ihm) oder "library" (verlinkt).
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
  /** Mitgelieferte, schreibgeschützte Vorlage (fest ins Binary eingebettet). */
  builtin?: boolean;
};

// Herkunft des laufenden Binaries (GET /version, internal/buildinfo): welcher
// Stand läuft hier? Der Fuß der Sidebar zeigt sie — nach einem Deploy die
// erste Frage. built_at ist RFC3339 (UTC), commit/built_at können leer sein,
// wenn ein Build ohne Git-Kontext lief.
export type BuildInfo = {
  version: string;
  commit: string;
  built_at: string;
  dirty: boolean;
  go: string;
  // Öffentliche Quelle dieses Binaries (AGPL-3.0). Kommt vom Server, damit ein
  // Fork seine eigene Adresse zeigt statt der des Ursprungs.
  source: string;
  // Dieselbe Adresse als Zielsystem-Adresse: die Voreinstellung, an die Covey
  // Doctor meldet, solange die Organisation kein eigenes Repository nennt
  // (spec/21). Leer, wenn die Quelle auf keinem Plugin liegt, das auschecken
  // kann — dann gibt es keine Voreinstellung.
  source_system?: string;
  source_project?: string;
};

export const buildInfo = () => api<BuildInfo>("/version");


// Erste Schritte: der Zustand der Organisation, nicht ein Fortschritt, den
// sich die Oberfläche merkt (GET /onboarding). done=true → die Checkliste hat
// nichts mehr zu sagen und verschwindet.
export type OnboardingState = {
  steps: Array<{ key: string; done: boolean }>;
  done: boolean;
  // Was zwischen der Plattform und einer laufenden Sandbox steht (fehlender
  // Docker-Socket, ungebautes Sandbox-Image). Kein Schritt der Liste: hier
  // klickt niemand ein fehlendes Image weg, die Meldungen kommen fertig
  // formuliert vom Server und richten sich an den Betreiber.
  data_plane?: { ready: boolean; problems?: string[] };
};

// Ein Eintrag der Audit-Spur (GET /audit): wer wann was an der Plattform
// angefasst hat. Ohne Request-Inhalte — darin stünden Secret-Werte.
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

/* Was passieren soll, wenn der Server eine Anfrage mit 401 abweist: die
   Sitzung ist abgelaufen (oder anderswo beendet worden). Das ist kein Fehler
   EINER Seite, sondern das Ende der ganzen Oberfläche — deshalb hängt die
   Reaktion nicht an der aufrufenden Komponente, sondern hier an einer Stelle.
   App.tsx meldet sich an und schaltet auf die Anmeldung um. Ohne das blieb die
   Hülle stehen und füllte sich mit Fehlermeldungen. */
let abgelaufenMelden: (() => void) | null = null;

export function setUnauthorizedHandler(fn: (() => void) | null) {
  abgelaufenMelden = fn;
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  // Bei FormData setzt der Browser den Content-Type selbst — samt der
  // multipart-Grenze, die wir gar nicht kennen. Ihn zu überschreiben machte
  // den Upload unlesbar.
  const isForm = init?.body instanceof FormData;
  const res = await fetch(`/api/v1${path}`, {
    headers: isForm ? undefined : { "Content-Type": "application/json" },
    ...init,
  });
  if (!res.ok) {
    /* Die Endpunkte unter /auth/ sind ausgenommen: dort ist die 401 die
       normale Antwort ("nicht angemeldet", "falsches Passwort") und wird von
       der Anmeldung selbst behandelt — ein globaler Abbruch würde die
       Anmeldemaske gegen sich selbst richten. */
    if (res.status === 401 && !path.startsWith("/auth/")) abgelaufenMelden?.();
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
export const upload = <T>(path: string, form: FormData) =>
  api<T>(path, { method: "POST", body: form });

// --- Arbeitsplatz: das persistente Home eines Agenten als Dateibaum ---

// Wie eine Datei zu zeigen ist. Der Server entscheidet das an einer Stelle
// (internal/sandboxfs) — die Oberfläche wählt danach nur noch die Darstellung.
export type PreviewKind = "text" | "markdown" | "image" | "pdf" | "csv" | "binary";

export type FileEntry = {
  name: string;
  /** Pfad relativ zum Home, „/" als Trenner. */
  path: string;
  is_dir: boolean;
  size: number;
  mode: string;
  mod_time: string;
  /** Ziel, wenn der Eintrag ein Symlink ist. */
  symlink?: string;
  /** Der Link zeigt aus dem Home heraus — sichtbar, aber nicht zu öffnen. */
  outside?: boolean;
  /** Vorschau-Art nach Dateiname; leer = erst beim Öffnen entscheidbar. */
  preview?: PreviewKind;
};

export type FileListing = {
  // read_only: das Home wird aus dem letzten Snapshot gelesen, weil sein Runner
  // nicht verbunden ist (spec/16). Schreiben ist dann abgelehnt.
  read_only?: boolean;
  read_only_reason?: string;
  path: string;
  /** false = das Home wurde noch nie angelegt (Agent nie geweckt). */
  exists: boolean;
  truncated: boolean;
  entries: FileEntry[];
};

/** Wieviel Platz das Home des Agenten belegt — und welche Arbeitskopien ihn
 *  fressen. Vorher hat das nichts gemessen: Checkouts stapeln sich im
 *  persistenten Home, bis ein Lauf am vollen Overlay stirbt. */
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
  /** text/markdown/csv tragen content; image/pdf kommen über den preview-Endpunkt. */
  preview: PreviewKind;
  content: string;
};

// Ein Arbeitsplatz aus dem Katalog des Servers (spec/16): das Image, in dem ein
// Agent arbeitet, plus das, was nur die Instanz dazu weiß — welches Image dahinter
// liegt, woher die Adresse stammt und ob sie schon auf einem Runner liegt.
export type Workplace = {
  name: string;
  label: string;
  description: string;
  /** Die Adresse, die tatsächlich gestartet wird; aus dem Katalog auf den Digest gepinnt. */
  image: string;
  /** Der Name, unter dem dasselbe Image veröffentlicht wurde ("base-v0.4.0"). */
  tag?: string;
  platforms?: string[];
  build: string;
  dockerfile: string;
  default?: boolean;
  // available fehlt, wenn niemand gefragt werden konnte — das ist etwas
  // anderes als „nicht da" und darf nicht so aussehen.
  available?: boolean;
  in_use: number;
  /* Woher die Adresse stammt: aus dem veröffentlichten Katalog, aus einer
     Umgebungsvariable dieser Instanz, oder aus der kompilierten
     Voreinstellung. Ohne diese Angabe müsste jemand zwischen drei Quellen
     raten, wenn ein Image nicht das ist, was er erwartet hat. */
  source?: "catalog" | "env" | "builtin";
  /** Aus dem Katalog des Projekts oder von dieser Organisation mitgebracht. */
  kind?: "catalog" | "own";
  /** Wer hier arbeitet — benannt, nicht gezählt. */
  agents?: { id: string; slug: string; display_name: string }[];
};

export const createWorkplace = (w: { name: string; label: string; description: string; image: string }) =>
  post<Workplace>("/workplaces", w);
export const deleteWorkplace = (name: string) => del<{ ok: boolean }>(`/workplaces/${name}`);

// KI-Assistent zum Anpassen von Agenten (Config-Copilot, FR-001).
export type AssistMessage = { role: "user" | "assistant"; content: string };
export type AssistProposal = { file: string; content: string };
export type AssistReply = { reply: string; proposals: AssistProposal[] };

export const assistStatus = () =>
  api<{ available: boolean }>("/assist/status");
export const configAssist = (agentId: string, messages: AssistMessage[], files: Record<string, string>) =>
  post<AssistReply>(`/agents/${agentId}/config/assist`, { messages, files });

/* Die Rollen in Anzeigereihenfolge. Die Beschriftung steht NICHT hier, sondern
   in den Sprachdateien unter role.<rolle> — eine Liste deutscher Beschriftungen
   an dieser Stelle war der Grund, warum die englische Oberfläche
   "Plattform-Admin" anzeigte — heute heißt die oberste Org-Rolle org_admin. */
export const ROLES = [
  "org_admin",
  "agent_owner",
  "security",
  "auditor",
  "controlling",
] as const;


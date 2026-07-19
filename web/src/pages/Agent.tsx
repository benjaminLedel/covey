import { useEffect, useRef, useState, type CSSProperties } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import {
  api,
  post,
  patch,
  del,
  put,
  statusLabel,
  type Agent,
  type AgentEgress as AgentEgressCfg,
  type AgentWebhook,
  type ConfigVersion,
  type CostSummary,
  type EgressStatus,
  type EgressTemplate,
  type HeartbeatStatus,
  type MCPTool,
  type MemoryEntry,
  type OrgChart,
  type Principal,
  type RecordingEvent,
  type RuntimeInfo,
  type SecretCheck,
  type SecretPreview,
  type Stage,
  type TargetPlugin,
  type Task,
  type TaskNote,
} from "../api";
import { ActivityFeed } from "../components/ActivityFeed";
import { PersonLink } from "../components/person";
import { AddHostForm, EgressLogTable, HostChips } from "../components/EgressBits";
import { generateAgentName } from "../names";

const canManage = (role: string) => role === "platform_admin" || role === "agent_owner";
const canKill = (role: string) => canManage(role) || role === "security";
const canSecrets = (role: string) => role === "platform_admin" || role === "security";

export default function AgentPage({ me }: { me: Principal }) {
  const { id } = useParams<{ id: string }>();
  const qc = useQueryClient();
  const agent = useQuery({ queryKey: ["agent", id], queryFn: () => api<Agent>(`/agents/${id}`) });
  const [tab, setTab] = useState<
    "backlog" | "heartbeat" | "webhook" | "recording" | "config" | "memory" | "secrets" | "egress" | "tools" | "einstellungen"
  >("backlog");
  const [recTask, setRecTask] = useState<{ id: string; title: string } | null>(null);

  const act = useMutation({
    mutationFn: (action: string) => post(`/agents/${id}/${action}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["agent", id] });
      qc.invalidateQueries({ queryKey: ["agents"] });
    },
  });

  if (agent.isLoading) return null;
  if (agent.isError || !agent.data) return <p className="danger-text">Agent nicht gefunden.</p>;
  const a = agent.data;

  return (
    <div>
      <div className="text-sm secondary mb-3">
        <Link to="/" style={{ color: "inherit" }}>
          Agenten
        </Link>{" "}
        / <b style={{ color: "var(--text-primary)", fontWeight: 500 }}>{a.display_name}</b>
      </div>

      <div className="flex items-center gap-3 mb-5 flex-wrap">
        <h1 className="text-[22px]">{a.display_name}</h1>
        <span className={`badge st-${a.killed ? "killed" : a.status}`}>
          {a.killed ? "gestoppt" : statusLabel[a.status] ?? a.status}
        </span>
        {(a.status === "working" || a.status === "triage" || a.status === "triggered") && (
          <span className="live-dot" title="Sandbox aktiv" />
        )}
        <span className="muted text-xs mono">
          runtime: {a.runtime}
          {a.model && ` · ${a.model}`}
        </span>
        <Supervisor agent={a} editable={canManage(me.Role)} />
        <span className="ml-auto" />
        {canManage(me.Role) && (
          <button className="btn sm" onClick={() => act.mutate("wake")}>
            Wecken
          </button>
        )}
        {canKill(me.Role) &&
          (a.killed ? (
            <button className="btn sm" onClick={() => act.mutate("resume")}>
              Fortsetzen
            </button>
          ) : (
            <button className="btn sm danger" onClick={() => act.mutate("kill")} title="Kill-Switch">
              Stoppen
            </button>
          ))}
      </div>

      <CostBar agentId={a.id} budget={a.budget_usd} />

      <div className="flex gap-1 mb-4 mt-5" style={{ borderBottom: "0.5px solid var(--border)" }}>
        {(
          [
            ["backlog", "Backlog"],
            ["heartbeat", "Heartbeat"],
            ...(canManage(me.Role) ? ([["webhook", "Webhook"]] as const) : []),
            ["recording", "Recording"],
            ["memory", "Gedächtnis"],
            ["tools", "Tools"],
            ["egress", "Egress"],
            ...(canSecrets(me.Role) ? ([["secrets", "Secrets"]] as const) : []),
            ["config", "Config"],
            ["einstellungen", "Einstellungen"],
          ] as const
        ).map(([key, label]) => (
          <button
            key={key}
            onClick={() => {
              if (key === "recording") setRecTask(null);
              setTab(key);
            }}
            className="btn sm"
            style={{
              border: "none",
              borderRadius: "8px 8px 0 0",
              borderBottom: tab === key ? "2px solid var(--text-accent)" : "2px solid transparent",
              color: tab === key ? "var(--text-primary)" : "var(--text-secondary)",
            }}
          >
            {label}
          </button>
        ))}
      </div>

      {tab === "backlog" && (
        <Backlog
          agentId={a.id}
          canManage={canManage(me.Role)}
          onShowRecording={(id, title) => {
            setRecTask({ id, title });
            setTab("recording");
          }}
        />
      )}
      {tab === "heartbeat" && <Heartbeats agentId={a.id} canManage={canManage(me.Role)} />}
      {tab === "webhook" && canManage(me.Role) && <WebhookTrigger agentId={a.id} />}
      {tab === "recording" && (
        <Recording agentId={a.id} taskFilter={recTask} onClearFilter={() => setRecTask(null)} />
      )}
      {tab === "config" && <Config agentId={a.id} canManage={canManage(me.Role)} />}
      {tab === "memory" && <Memories agentId={a.id} canManage={canManage(me.Role)} />}
      {tab === "egress" && <AgentEgress agentId={a.id} canEdit={canSecrets(me.Role)} />}
      {tab === "tools" && <AgentTools agentId={a.id} canEdit={canSecrets(me.Role)} />}
      {tab === "secrets" && canSecrets(me.Role) && <AgentSecrets agentId={a.id} />}
      {tab === "einstellungen" && <AgentSettings agent={a} editable={canManage(me.Role)} />}
    </div>
  );
}

// Einstellungen-Reiter: die Stammdaten des Agenten an einem Ort — Name,
// Runtime, Modell, Turn-Limit, Budget. Änderungen greifen beim nächsten
// Task-Dispatch; laufende Sessions bleiben unberührt (spec/12).
function AgentSettings({ agent, editable }: { agent: Agent; editable: boolean }) {
  const qc = useQueryClient();
  const runtimes = useQuery({
    queryKey: ["runtimes"],
    queryFn: () => api<RuntimeInfo[]>("/runtimes"),
  });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["agent", agent.id] });
    qc.invalidateQueries({ queryKey: ["agents"] });
  };
  const setName = useMutation({
    mutationFn: (displayName: string) => patch(`/agents/${agent.id}/name`, { display_name: displayName }),
    onSuccess: invalidate,
  });
  const setRuntime = useMutation({
    mutationFn: (runtime: string) => patch(`/agents/${agent.id}/runtime`, { runtime }),
    onSuccess: invalidate,
  });
  const setModel = useMutation({
    mutationFn: (model: string) => patch(`/agents/${agent.id}/model`, { model }),
    onSuccess: invalidate,
  });
  const setMaxTurns = useMutation({
    mutationFn: (maxTurns: number) => patch(`/agents/${agent.id}/max-turns`, { max_turns: maxTurns }),
    onSuccess: invalidate,
  });
  const setBudget = useMutation({
    mutationFn: (budgetUSD: number) => post(`/agents/${agent.id}/budget`, { budget_usd: budgetUSD }),
    onSuccess: invalidate,
  });
  const anyError = [setName, setRuntime, setModel, setMaxTurns, setBudget].find((m) => m.isError);

  const rtList = runtimes.data ?? [];
  const row: CSSProperties = {
    display: "grid",
    gridTemplateColumns: "180px minmax(200px, 320px) 1fr",
    alignItems: "center",
    gap: 12,
    padding: "10px 0",
    borderBottom: "0.5px solid var(--border)",
  };

  return (
    <div className="card" style={{ maxWidth: 760, padding: "6px 18px 14px" }}>
      <div style={row}>
        <span className="text-sm">Name</span>
        <span className="flex items-center gap-2">
          <input
            key={`name:${agent.display_name}`}
            defaultValue={agent.display_name}
            disabled={!editable || setName.isPending}
            onBlur={(e) => {
              const v = e.target.value.trim();
              if (v && v !== agent.display_name) setName.mutate(v);
            }}
            onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
            style={{ flex: 1 }}
          />
          {editable && (
            <button
              className="btn sm"
              title="Zufälligen Namen würfeln"
              disabled={setName.isPending}
              onClick={() => setName.mutate(generateAgentName().name)}
            >
              🎲
            </button>
          )}
        </span>
        <span className="muted text-xs">Anzeigename — der Slug bleibt unveränderlich.</span>
      </div>
      <div style={row}>
        <span className="text-sm">Slug</span>
        <span className="mono text-sm">{agent.slug}</span>
        <span className="muted text-xs">Stabiler Anker für Korrelation, Home-Verzeichnis und Referenzen.</span>
      </div>
      <div style={row}>
        <span className="text-sm">Runtime</span>
        <select
          value={agent.runtime}
          disabled={!editable || setRuntime.isPending}
          onChange={(e) => setRuntime.mutate(e.target.value)}
          className="mono"
        >
          {rtList.length === 0 && <option value={agent.runtime}>{agent.runtime}</option>}
          {rtList.map((rt) => (
            <option key={rt.name} value={rt.name}>
              {rt.name}
            </option>
          ))}
        </select>
        <span className="muted text-xs">
          Greift beim nächsten Task-Dispatch. Einrichtung je Runtime: Seite „Runtimes".
        </span>
      </div>
      <div style={row}>
        <span className="text-sm">Modell</span>
        <input
          key={`model:${agent.model}`}
          defaultValue={agent.model}
          placeholder="leer = Runtime-Default"
          disabled={!editable || setModel.isPending}
          onBlur={(e) => {
            const v = e.target.value.trim();
            if (v !== agent.model) setModel.mutate(v);
          }}
          onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
          className="mono"
        />
        <span className="muted text-xs">Modell-ID oder Alias, z. B. claude-opus-4-8 oder sonnet.</span>
      </div>
      <div style={row}>
        <span className="text-sm">Max. Turns je Lauf</span>
        <input
          key={`turns:${agent.max_turns}`}
          type="number"
          min={0}
          defaultValue={agent.max_turns || ""}
          placeholder="0 = Default (30)"
          disabled={!editable || setMaxTurns.isPending}
          onBlur={(e) => {
            const v = Math.max(0, Math.trunc(Number(e.target.value) || 0));
            if (v !== agent.max_turns) setMaxTurns.mutate(v);
          }}
          onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
          className="mono"
        />
        <span className="muted text-xs">
          Runaway-Guard: bricht einen Lauf nach n Turns ab. Für Aufgaben mit Repo-Checkout großzügig wählen.
        </span>
      </div>
      <div style={{ ...row, borderBottom: "none" }}>
        <span className="text-sm">Budget (USD)</span>
        <input
          key={`budget:${agent.budget_usd}`}
          type="number"
          min={0}
          step="0.01"
          defaultValue={agent.budget_usd || ""}
          placeholder="0 = kein Deckel"
          disabled={!editable || setBudget.isPending}
          onBlur={(e) => {
            const v = Math.max(0, Number(e.target.value) || 0);
            if (v !== agent.budget_usd) setBudget.mutate(v);
          }}
          onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
          className="mono"
        />
        <span className="muted text-xs">
          Kill-Switch bei Überschreitung — die Aufgabe geht zurück ins Backlog.
        </span>
      </div>
      {!editable && (
        <p className="muted text-xs mt-2">Ihre Rolle kann Einstellungen ansehen, aber nicht ändern.</p>
      )}
      {anyError && <p className="danger-text text-xs mt-2">{String(anyError.error)}</p>}
    </div>
  );
}

// Supervisor: der Platz des Agenten im Org-Chart (spec/02) — an wen er
// berichtet und eskaliert. Manager-Rollen ordnen hier direkt zu.
function Supervisor({ agent, editable }: { agent: Agent; editable: boolean }) {
  const qc = useQueryClient();
  const chart = useQuery({ queryKey: ["orgchart"], queryFn: () => api<OrgChart>("/org/chart") });
  const mut = useMutation({
    mutationFn: (supervisorId: string) => patch(`/agents/${agent.id}/supervisor`, { supervisor_id: supervisorId }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["agent", agent.id] });
      qc.invalidateQueries({ queryKey: ["agents"] });
      qc.invalidateQueries({ queryKey: ["orgchart"] });
    },
  });
  const humans = chart.data?.humans ?? [];
  const current = humans.find((h) => h.id === agent.supervisor_id);

  if (!editable) {
    return (
      <span className="muted text-xs">
        berichtet an: {current ? <PersonLink human={current} /> : "—"}
      </span>
    );
  }
  return (
    <span className="flex items-center gap-2 text-xs muted">
      berichtet an:
      <select
        value={agent.supervisor_id ?? ""}
        onChange={(e) => mut.mutate(e.target.value)}
        disabled={mut.isPending || chart.isLoading}
        style={{ width: "auto", padding: "3px 8px", fontSize: 12 }}
      >
        <option value="">— niemanden —</option>
        {humans.map((h) => (
          <option key={h.id} value={h.id}>
            {h.display_name}
          </option>
        ))}
      </select>
    </span>
  );
}

function CostBar({ agentId, budget }: { agentId: string; budget: number }) {
  const cost = useQuery({
    queryKey: ["cost", agentId],
    queryFn: () => api<CostSummary>(`/agents/${agentId}/cost`),
  });
  const c = cost.data;
  if (!c) return null;
  return (
    <div className="card flex gap-8 text-sm">
      <div>
        <div className="muted text-xs">Kosten gesamt</div>
        <div className="font-medium">{c.total_usd.toFixed(4)} $</div>
      </div>
      <div>
        <div className="muted text-xs">Tokens (in / out)</div>
        <div className="font-medium">
          {c.input_tokens.toLocaleString("de-DE")} / {c.output_tokens.toLocaleString("de-DE")}
        </div>
      </div>
      <div>
        <div className="muted text-xs">LLM-Läufe</div>
        <div className="font-medium">{c.entries}</div>
      </div>
      <div>
        <div className="muted text-xs">Budget</div>
        <div className="font-medium">{budget > 0 ? `${budget.toFixed(2)} $` : "kein Deckel"}</div>
      </div>
    </div>
  );
}

function Backlog({
  agentId,
  canManage,
  onShowRecording,
}: {
  agentId: string;
  canManage: boolean;
  onShowRecording: (taskId: string, title: string) => void;
}) {
  const qc = useQueryClient();
  const [showArchive, setShowArchive] = useState(false);
  const tasks = useQuery({
    queryKey: ["backlog", agentId, showArchive],
    queryFn: () => api<Task[]>(`/agents/${agentId}/backlog${showArchive ? "?archived=1" : ""}`),
  });
  const invalBacklog = () => qc.invalidateQueries({ queryKey: ["backlog", agentId] });
  const cleanup = useMutation({
    mutationFn: () => post<{ archived: number }>(`/agents/${agentId}/backlog/cleanup`),
    onSuccess: invalBacklog,
  });
  const cleanupCount = (tasks.data ?? []).filter(
    (t) => terminalStates.includes(t.state) && !t.archived_at,
  ).length;
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const create = useMutation({
    mutationFn: () => post(`/agents/${agentId}/tasks`, { title, body }),
    onSuccess: () => {
      setTitle("");
      setBody("");
      qc.invalidateQueries({ queryKey: ["backlog", agentId] });
    },
  });

  const stages = useQuery({
    queryKey: ["stages", agentId],
    queryFn: () => api<Stage[]>(`/agents/${agentId}/stages`),
  });
  const move = useMutation({
    mutationFn: ({ taskId, stageId }: { taskId: string; stageId: string | null }) =>
      post(`/tasks/${taskId}/stage`, { stage_id: stageId }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backlog", agentId] }),
  });
  const [dragTask, setDragTask] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);

  const stageList = stages.data ?? [];
  const known = new Set(stageList.map((s) => s.id));
  const inStage = (t: Task, id: string | null) =>
    id === null ? !t.stage_id || !known.has(t.stage_id) : t.stage_id === id;
  const orphans = (tasks.data ?? []).filter((t) => inStage(t, null));
  // Spalten: optionale „Ohne Stage" zuerst, dann die definierten Stages.
  const columns: { id: string | null; name: string; color: string }[] = [
    ...(orphans.length ? [{ id: null, name: "Ohne Stage", color: "var(--text-muted)" }] : []),
    ...stageList.map((s) => ({ id: s.id, name: s.name, color: s.color || "var(--text-secondary)" })),
  ];

  const drop = (stageId: string | null) => {
    if (dragTask) move.mutate({ taskId: dragTask, stageId });
    setDragTask(null);
  };

  return (
    <div>
      {canManage && (
        <form
          className="card mb-4"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate();
          }}
        >
          <label>Neue Aufgabe</label>
          <input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="Titel"
            className="mb-2"
            required
          />
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Beschreibung / Kontext"
            rows={2}
            className="mb-2"
          />
          <button className="btn primary sm" disabled={create.isPending}>
            Ins Backlog
          </button>
        </form>
      )}
      {tasks.data && stages.data && (
        <>
          <div className="flex items-center gap-2 mb-3">
            <span className="muted text-xs">
              {stageList.length === 0
                ? "Noch keine Stages — der Agent legt sie beim Arbeiten selbst an, oder du hier."
                : canManage
                  ? "Der Zustands-Badge kommt vom Agenten; die Spalten sind deine Organisation — ziehe Aufgaben frei."
                  : "Stages werden vom Agenten und von Verwaltern gepflegt."}
            </span>
            <button className="btn sm" style={{ marginLeft: "auto" }} onClick={() => setShowArchive((v) => !v)}>
              {showArchive ? "Archiv ausblenden" : "Archiv anzeigen"}
            </button>
            {canManage && (
              <>
                <button
                  className="btn sm"
                  disabled={cleanupCount === 0 || cleanup.isPending}
                  title="Archiviert alle erledigten, fehlgeschlagenen und verworfenen Aufgaben — nichts wird gelöscht."
                  onClick={() => cleanup.mutate()}
                >
                  Aufräumen{cleanupCount > 0 ? ` (${cleanupCount})` : ""}
                </button>
                <button className="btn sm" onClick={() => setEditing((v) => !v)}>
                  {editing ? "Fertig" : "Spalten bearbeiten"}
                </button>
              </>
            )}
          </div>

          {editing && canManage && <StageEditor agentId={agentId} stages={stageList} />}

          {columns.length === 0 ? (
            <p className="muted">Backlog ist leer.</p>
          ) : (
            <div className="kanban" style={{ ["--kcols" as string]: columns.length }}>
              {columns.map((col) => {
                const all = (tasks.data ?? [])
                  .filter((t) => inStage(t, col.id))
                  .sort((a, b) => b.created_at.localeCompare(a.created_at));
                const items = all.slice(0, COLUMN_LIMIT);
                const hidden = all.length - items.length;
                return (
                  <div
                    className={`kcol${dragTask ? " droppable" : ""}`}
                    key={col.id ?? "__none"}
                    onDragOver={(e) => {
                      if (dragTask) e.preventDefault();
                    }}
                    onDrop={() => drop(col.id)}
                  >
                    <div className="kh">
                      <span className="dot" style={{ background: col.color }} />
                      {col.name}
                      <span className="n">{all.length}</span>
                    </div>
                    {items.map((t) => (
                      <TaskCard
                        key={t.id}
                        task={t}
                        agentId={agentId}
                        canManage={canManage}
                        onShowRecording={onShowRecording}
                        onDragStart={() => setDragTask(t.id)}
                        onDragEnd={() => setDragTask(null)}
                      />
                    ))}
                    {all.length === 0 && <div className="kc-empty">—</div>}
                    {hidden > 0 && (
                      <div className="kc-empty">+{hidden} ältere ausgeblendet</div>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </>
      )}
    </div>
  );
}

const STAGE_PALETTE = [
  "var(--text-muted)",
  "var(--text-accent)",
  "var(--text-warning)",
  "var(--text-success)",
  "var(--text-danger)",
  "var(--text-pro)",
];

function StageEditor({ agentId, stages }: { agentId: string; stages: Stage[] }) {
  const qc = useQueryClient();
  const inval = () => qc.invalidateQueries({ queryKey: ["stages", agentId] });
  const invalBoth = () => {
    inval();
    qc.invalidateQueries({ queryKey: ["backlog", agentId] });
  };
  const [name, setName] = useState("");
  const create = useMutation({
    mutationFn: () =>
      post(`/agents/${agentId}/stages`, {
        name,
        color: STAGE_PALETTE[stages.length % STAGE_PALETTE.length],
      }),
    onSuccess: () => {
      setName("");
      inval();
    },
  });
  const update = useMutation({
    mutationFn: (s: Stage) => patch(`/stages/${s.id}`, { name: s.name, color: s.color, position: s.position }),
    onSuccess: inval,
  });
  const remove = useMutation({
    mutationFn: (id: string) => del(`/stages/${id}`),
    onSuccess: invalBoth,
  });
  const reorder = useMutation({
    mutationFn: (order: string[]) => post(`/agents/${agentId}/stages/reorder`, { order }),
    onSuccess: inval,
  });

  const swap = (i: number, j: number) => {
    if (j < 0 || j >= stages.length) return;
    const ids = stages.map((s) => s.id);
    [ids[i], ids[j]] = [ids[j], ids[i]];
    reorder.mutate(ids);
  };
  const cycleColor = (s: Stage) => {
    const idx = STAGE_PALETTE.indexOf(s.color);
    update.mutate({ ...s, color: STAGE_PALETTE[(idx + 1) % STAGE_PALETTE.length] });
  };

  return (
    <div className="card mb-4">
      <label>Spalten (Stages)</label>
      {stages.map((s, i) => (
        <div key={s.id} className="flex items-center gap-2 mb-2">
          <button
            type="button"
            className="dot-btn"
            title="Farbe wechseln"
            onClick={() => cycleColor(s)}
          >
            <span className="dot" style={{ background: s.color || "var(--text-secondary)" }} />
          </button>
          <input
            className="flex-1"
            defaultValue={s.name}
            onBlur={(e) => {
              if (e.target.value && e.target.value !== s.name) update.mutate({ ...s, name: e.target.value });
            }}
          />
          <button type="button" className="btn sm" onClick={() => swap(i, i - 1)} disabled={i === 0}>
            ↑
          </button>
          <button
            type="button"
            className="btn sm"
            onClick={() => swap(i, i + 1)}
            disabled={i === stages.length - 1}
          >
            ↓
          </button>
          <button type="button" className="btn sm danger" onClick={() => remove.mutate(s.id)}>
            Löschen
          </button>
        </div>
      ))}
      <form
        className="flex gap-2 mt-2"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <input
          className="flex-1"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Neue Spalte …"
          required
        />
        <button className="btn primary sm" disabled={create.isPending}>
          Hinzufügen
        </button>
      </form>
    </div>
  );
}

function relTime(iso: string): string {
  const min = Math.round((Date.now() - new Date(iso).getTime()) / 60000);
  if (min < 1) return "gerade eben";
  if (min < 60) return `vor ${min} Min`;
  const h = Math.round(min / 60);
  if (h < 24) return `vor ${h} Std`;
  return new Date(iso).toLocaleDateString("de-DE");
}

const terminalStates = ["done", "failed", "cancelled"];

// Pro Spalte nur die neuesten Aufgaben zeigen — hält lange Backlogs übersichtlich.
const COLUMN_LIMIT = 7;

function TaskCard({
  task,
  agentId,
  canManage,
  onShowRecording,
  onDragStart,
  onDragEnd,
}: {
  task: Task;
  agentId: string;
  canManage: boolean;
  onShowRecording: (taskId: string, title: string) => void;
  onDragStart?: () => void;
  onDragEnd?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const qc = useQueryClient();
  const invalBacklog = () => qc.invalidateQueries({ queryKey: ["backlog", agentId] });
  // Notizen erst laden, wenn die Karte aufgeklappt ist.
  const notes = useQuery({
    queryKey: ["task-notes", task.id],
    queryFn: () => api<TaskNote[]>(`/tasks/${task.id}/notes`),
    enabled: open,
  });
  const cancel = useMutation({
    mutationFn: () => post(`/tasks/${task.id}/cancel`),
    onSuccess: invalBacklog,
  });
  const retry = useMutation({
    mutationFn: () => post(`/tasks/${task.id}/retry`),
    onSuccess: invalBacklog,
  });
  const archive = useMutation({
    mutationFn: () => post(`/tasks/${task.id}/archive`),
    onSuccess: invalBacklog,
  });
  const archived = !!task.archived_at;
  const terminal = terminalStates.includes(task.state);
  const subtitle =
    task.state === "blocked"
      ? task.correlation_key
        ? `wartet auf ${task.correlation_key}`
        : "blockiert"
      : relTime(task.updated_at);
  return (
    <div
      className={`kc${open ? " expanded" : ""}${canManage && !archived ? " draggable" : ""}`}
      draggable={canManage && !archived}
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onClick={() => setOpen((v) => !v)}
      style={archived ? { opacity: 0.55 } : undefined}
    >
      <div className="kc-title">
        <span className={`badge st-${task.state} kc-state`}>{statusLabel[task.state] ?? task.state}</span>
        <span className="font-medium min-w-0 truncate">{task.title}</span>
        {archived && <span className="muted text-[11px] shrink-0">archiviert</span>}
        <span className="kc-prio">P{task.priority}</span>
      </div>
      <div className="t">{subtitle} · {task.origin}</div>
      {open && (
        <div className="kc-detail fade">
          {task.body && (
            <pre className="voice whitespace-pre-wrap m-0 mb-2 secondary" style={{ fontFamily: "var(--voice)" }}>{task.body}</pre>
          )}
          {task.correlation_key && (
            <p>
              <span className="muted">wartet auf:</span> <span className="mono">{task.correlation_key}</span>
            </p>
          )}
          {task.runtime_session_id && (
            <p>
              <span className="muted">runtime-session:</span> <span className="mono">{task.runtime_session_id}</span>
            </p>
          )}
          {task.result && <p style={{ color: "var(--text-success)" }}>{task.result}</p>}
          {task.error && <p className="danger-text">{task.error}</p>}
          {(notes.data?.length ?? 0) > 0 && (
            <div className="mt-2 mb-2">
              <div className="muted text-xs mb-1">Notizen des Agenten</div>
              {notes.data!.map((n) => (
                <div key={n.id} className="text-xs mb-1" style={{ borderLeft: "2px solid var(--border)", paddingLeft: 8 }}>
                  <span className="secondary">{n.content}</span>{" "}
                  <span className="muted">· {relTime(n.created_at)}</span>
                </div>
              ))}
            </div>
          )}
          <div className="flex gap-2 mt-1">
            <button
              className="btn sm"
              onClick={(e) => {
                e.stopPropagation();
                onShowRecording(task.id, task.title);
              }}
            >
              Aufzeichnung dieser Aufgabe
            </button>
            {canManage && (task.state === "failed" || task.state === "cancelled") && (
              <button
                className="btn sm"
                disabled={retry.isPending}
                title="Setzt die Aufgabe zurück auf „offen“ — der Agent nimmt sie sich erneut vor."
                onClick={(e) => {
                  e.stopPropagation();
                  retry.mutate();
                }}
              >
                Erneut einplanen
              </button>
            )}
            {canManage && terminal && !archived && (
              <button
                className="btn sm"
                disabled={archive.isPending}
                title="Blendet die Aufgabe aus dem aktiven Backlog aus — Historie und Recording bleiben erhalten."
                onClick={(e) => {
                  e.stopPropagation();
                  archive.mutate();
                }}
              >
                Archivieren
              </button>
            )}
            {canManage && !terminal && (
              <button
                className="btn sm danger"
                disabled={cancel.isPending}
                onClick={(e) => {
                  e.stopPropagation();
                  if (confirm(`Aufgabe „${task.title}“ verwerfen?`)) cancel.mutate();
                }}
              >
                Verwerfen
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function Recording({
  agentId,
  taskFilter,
  onClearFilter,
}: {
  agentId: string;
  taskFilter?: { id: string; title: string } | null;
  onClearFilter?: () => void;
}) {
  // Lesbare Erzählansicht (Mockup-Stil) als Standard; die Rohdaten-Liste
  // bleibt als lückenloser Audit-Blick umschaltbar erhalten.
  const [view, setView] = useState<"feed" | "raw">("feed");
  const events = useQuery({
    queryKey: ["recording", agentId, taskFilter?.id ?? null],
    queryFn: () =>
      api<RecordingEvent[] | null>(
        `/agents/${agentId}/recording${taskFilter ? `?task_id=${taskFilter.id}` : ""}`,
      ),
    refetchInterval: 5000,
  });
  const list = events.data ?? [];
  // Der Feed erzählt chronologisch (Neuestes unten). Beim Öffnen und bei
  // neuen Events springt die Ansicht ans Ende — außer man hat bewusst
  // hochgescrollt, um Älteres zu lesen.
  const endRef = useRef<HTMLDivElement>(null);
  const didInitialScroll = useRef(false);
  const lastID = list.length > 0 ? list[list.length - 1].id : 0;
  useEffect(() => {
    if (view !== "feed" || lastID === 0) return;
    const nearBottom =
      window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 400;
    if (!didInitialScroll.current || nearBottom) {
      endRef.current?.scrollIntoView({ block: "end" });
      didInitialScroll.current = true;
    }
  }, [view, lastID]);
  return (
    <div>
      <div className="flex items-center gap-2 mb-3">
        {taskFilter && (
          <>
            <span className="badge st-triage">Aufgabe: {taskFilter.title}</span>
            <button className="btn sm" onClick={onClearFilter}>
              Alle Ereignisse
            </button>
          </>
        )}
        <span className="flex-1" />
        <div className="seg" role="tablist" aria-label="Ansicht">
          <button
            className={view === "feed" ? "active" : ""}
            onClick={() => setView("feed")}
            title="Erzählende Ansicht: Gedanken, Tool-Aufrufe und Gates"
          >
            Lesbar
          </button>
          <button
            className={view === "raw" ? "active" : ""}
            onClick={() => setView("raw")}
            title="Alle Ereignisse einzeln, mit Roh-JSON — lückenlos"
          >
            Rohdaten
          </button>
        </div>
      </div>
      <div className="card">
        {list.length === 0 && (
          <p className="muted m-0">
            {taskFilter ? "Keine Aufzeichnung für diese Aufgabe." : "Noch keine Aufzeichnung."}
          </p>
        )}
        {list.length > 0 &&
          (view === "feed" ? (
            <ActivityFeed events={list} truncated={list.length >= 500} />
          ) : (
            [...list].reverse().map((e) => <RecordingItem key={e.id} event={e} />)
          ))}
        <div ref={endRef} />
      </div>
    </div>
  );
}

// summarize übersetzt ein Recording-Event in eine menschenlesbare Zeile.
// Die Rohdaten bleiben pro Eintrag aufklappbar — lückenlos, aber lesbar.
function summarize(e: RecordingEvent): { text: string; mono?: boolean; muted?: boolean; danger?: boolean } {
  const p = (typeof e.payload === "object" && e.payload !== null ? e.payload : {}) as Record<string, any>;
  switch (e.kind) {
    case "lifecycle": {
      if (p.status === "task_done") return { text: "Aufgabe abgeschlossen" };
      const label = statusLabel[p.status as string] ?? p.status;
      return { text: `Status → ${label}`, muted: p.status === "sleeping" };
    }
    case "credential": {
      const ttl = p.ttl_secs ? `, gültig ${Math.round(p.ttl_secs / 60)} min` : "";
      if (p.granted) return { text: `Zugang zu ${p.system} kurzlebig ausgestellt${p.proactive ? " (proaktiv)" : ""}${ttl}` };
      return { text: `Zugang zu ${p.system} verweigert${p.reason ? ` — ${p.reason}` : ""}`, danger: true };
    }
    case "approval": {
      const decision =
        p.decision === "auto-allow" ? "automatisch erlaubt" : (statusLabel[p.decision as string] ?? p.decision);
      return { text: `${p.action} — ${decision}`, danger: p.decision === "denied" };
    }
    case "guardrail":
      return {
        text: `Guard-Rail ${p.rule ?? ""}: ${p.action ?? p.system ?? ""}${p.pattern ? ` (Muster ${p.pattern})` : ""}`,
        danger: true,
      };
    case "action":
      return { text: `${p.action ?? "Aktion"} im Zielsystem`, mono: true };
    case "runtime":
      return summarizeRuntime(p);
  }
  return { text: JSON.stringify(e.payload), mono: true };
}

// summarizeRuntime versteht die stream-json-Zeilen von Claude Code sowie die
// simplen {type,text}-Events der Mock-Runtime.
function summarizeRuntime(p: Record<string, any>): { text: string; mono?: boolean; muted?: boolean; danger?: boolean } {
  if (typeof p.text === "string" && !p.message) return { text: p.text };
  switch (p.type) {
    case "system":
      return { text: `Runtime gestartet${p.model ? ` — Modell ${p.model}` : ""}`, muted: true };
    case "rate_limit_event":
      return { text: "Rate-Limit-Status der API aktualisiert", muted: true };
    case "assistant": {
      const blocks: any[] = Array.isArray(p.message?.content) ? p.message.content : [];
      const parts: string[] = [];
      for (const b of blocks) {
        if (b.type === "text" && b.text) parts.push(truncate(b.text, 400));
        if (b.type === "tool_use") parts.push(`⚙ ${b.name}(${truncate(compactInput(b.input), 120)})`);
      }
      return parts.length ? { text: parts.join("  ·  ") } : { text: "Antwort der Runtime", muted: true };
    }
    case "user": {
      const blocks: any[] = Array.isArray(p.message?.content) ? p.message.content : [];
      const res = blocks.find((b) => b.type === "tool_result");
      if (res) {
        const body = typeof res.content === "string" ? res.content : JSON.stringify(res.content);
        return { text: `Tool-Ergebnis: ${truncate(body, 200)}`, mono: true, muted: true };
      }
      return { text: "Eingabe an die Runtime", muted: true };
    }
    case "result": {
      const cost = p.total_cost_usd ? ` · $${Number(p.total_cost_usd).toFixed(4)}` : "";
      const tok = p.usage?.input_tokens ? ` · ${p.usage.input_tokens}→${p.usage.output_tokens} Tokens` : "";
      if (p.is_error) return { text: `Fehlgeschlagen: ${truncate(p.result ?? "", 300)}`, danger: true };
      return { text: `Ergebnis: ${truncate(p.result ?? "", 400)}${cost}${tok}` };
    }
  }
  return { text: JSON.stringify(p), mono: true };
}

const truncate = (s: string, n: number) => (s.length > n ? s.slice(0, n) + "…" : s);

const compactInput = (input: unknown) => {
  if (!input || typeof input !== "object") return "";
  return Object.entries(input as Record<string, unknown>)
    .map(([k, v]) => `${k}: ${typeof v === "string" ? v : JSON.stringify(v)}`)
    .join(", ");
};

function RecordingItem({ event }: { event: RecordingEvent }) {
  const s = summarize(event);
  const raw = typeof event.payload === "string" ? event.payload : JSON.stringify(event.payload, null, 2);
  return (
    <div className="timeline-item">
      <span className={`kind-tag kind-${event.kind}`}>{event.kind}</span>
      <details className="flex-1 min-w-0 rec-details">
        <summary
          className={`rec-summary break-words ${s.mono ? "mono" : ""}`}
          style={{
            color: s.danger ? "var(--text-warning, #b45309)" : s.muted ? "var(--text-secondary)" : undefined,
            whiteSpace: "pre-wrap",
          }}
        >
          {s.text}
        </summary>
        <pre className="rec-raw">{raw}</pre>
      </details>
      <span className="muted shrink-0 text-[11px]">
        {new Date(event.created_at).toLocaleTimeString("de-DE")}
      </span>
    </div>
  );
}

// --- Heartbeat: grafische Sicht auf HEARTBEAT.md (Zeitplan + nächste Läufe) ---

// fmtDelta: kompakte deutsche Relativdauer ("42 s", "12 min", "3 h 20 min", "2 d 4 h").
function fmtDelta(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s} s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 ? `${h} h ${m % 60} min` : `${h} h`;
  const d = Math.floor(h / 24);
  return h % 24 ? `${d} d ${h % 24} h` : `${d} d`;
}

function scheduleLabel(hb: HeartbeatStatus): string {
  if (hb.every_seconds) return `alle ${fmtDelta(hb.every_seconds * 1000)}`;
  return `täglich ${hb.daily_at}`;
}

// fmtRunChip: "14:30" heute, sonst "Mo 09:00".
function fmtRunChip(d: Date): string {
  const time = d.toLocaleTimeString("de-DE", { hour: "2-digit", minute: "2-digit" });
  if (d.toDateString() === new Date().toDateString()) return time;
  return `${d.toLocaleDateString("de-DE", { weekday: "short" })} ${time}`;
}

// upcomingRuns projiziert die nächsten Läufe eines Heartbeats in die Zukunft:
// ab next_run im Zeitplan-Raster weiter (Intervall bzw. 24 h). Ein überfälliger
// next_run kollabiert zu einem "jetzt fällig"-Lauf. Rein anzeigend — die
// Wahrheit entsteht serverseitig beim Tick.
function upcomingRuns(hb: HeartbeatStatus, horizonMs: number): Date[] {
  const now = Date.now();
  const step = (hb.every_seconds ?? 24 * 3600) * 1000;
  let t = new Date(hb.next_run).getTime();
  const runs: Date[] = [];
  if (t <= now) {
    runs.push(new Date(now));
    while (t <= now) t += step;
  }
  while (t <= now + horizonMs && runs.length < 48) {
    runs.push(new Date(t));
    t += step;
  }
  return runs;
}

// HeartbeatTimeline: die nächsten Läufe als Punkte auf einer Zeitachse jetzt → Horizont.
function HeartbeatTimeline({ runs, horizonMs }: { runs: Date[]; horizonMs: number }) {
  const now = Date.now();
  return (
    <div>
      <div style={{ position: "relative", height: 14 }}>
        <div
          style={{
            position: "absolute",
            top: 6,
            left: 0,
            right: 0,
            height: 2,
            background: "var(--border)",
            borderRadius: 1,
          }}
        />
        {runs.map((r, i) => (
          <span
            key={i}
            title={r.toLocaleString("de-DE")}
            style={{
              position: "absolute",
              top: 3,
              left: `calc(${Math.min(99, Math.max(0, ((r.getTime() - now) / horizonMs) * 100))}% - 4px)`,
              width: 8,
              height: 8,
              borderRadius: "50%",
              background: "var(--text-accent)",
            }}
          />
        ))}
      </div>
      <div className="flex muted" style={{ fontSize: 10, justifyContent: "space-between" }}>
        <span>jetzt</span>
        <span>+{Math.round(horizonMs / 3600000)} h</span>
      </div>
    </div>
  );
}

function HeartbeatCard({
  hb,
  horizonMs,
  agentId,
  canManage,
}: {
  hb: HeartbeatStatus;
  horizonMs: number;
  agentId: string;
  canManage: boolean;
}) {
  const qc = useQueryClient();
  const fire = useMutation({
    mutationFn: () => post<Task>(`/agents/${agentId}/heartbeats/${encodeURIComponent(hb.name)}/fire`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["heartbeats", agentId] });
      qc.invalidateQueries({ queryKey: ["backlog", agentId] });
    },
  });
  const runs = upcomingRuns(hb, horizonMs);
  const next = new Date(hb.next_run);
  const overdue = next.getTime() <= Date.now();
  return (
    <div className="card mb-4">
      <div className="flex items-center gap-2 mb-2 flex-wrap">
        <span className="font-medium">{hb.name}</span>
        <span className="badge">{scheduleLabel(hb)}</span>
        {hb.pending && (
          <span className="badge st-blocked" title="Die Aufgabe des letzten Laufs ist noch nicht abgeschlossen — solange wird kein neuer Lauf angelegt.">
            Aufgabe offen
          </span>
        )}
        <span className="ml-auto muted text-xs">
          letzter Lauf vor {fmtDelta(Date.now() - new Date(hb.last_fired_at).getTime())}
        </span>
        {canManage && (
          <button
            className="btn sm"
            disabled={fire.isPending || hb.pending}
            title={
              hb.pending
                ? "Die Aufgabe des letzten Laufs ist noch offen — erst abschließen oder abbrechen."
                : "Legt die Aufgabe sofort im Backlog an und weckt den Agenten. Der Zeitplan rechnet ab jetzt weiter."
            }
            onClick={() => fire.mutate()}
          >
            {fire.isPending ? "läuft …" : "Jetzt ausführen"}
          </button>
        )}
      </div>
      {fire.isError && (
        <p className="text-xs mb-2" style={{ color: "var(--text-danger)" }}>
          {(fire.error as Error).message}
        </p>
      )}
      <p className="muted text-xs mb-2" style={{ maxWidth: 680 }}>
        {hb.task}
      </p>
      <p className="text-xs font-medium mb-2">
        {overdue
          ? hb.pending
            ? "fällig — wartet auf Abschluss der offenen Aufgabe"
            : "fällig — der nächste Tick legt die Aufgabe an"
          : `nächster Lauf in ${fmtDelta(next.getTime() - Date.now())}`}
        {runs.length > 0 && (
          <span className="muted font-normal">
            {" "}
            ({runs.slice(0, 3).map(fmtRunChip).join(" · ")}
            {runs.length > 3 ? " · …" : ""})
          </span>
        )}
      </p>
      <HeartbeatTimeline runs={runs} horizonMs={horizonMs} />
    </div>
  );
}

function Heartbeats({ agentId, canManage }: { agentId: string; canManage: boolean }) {
  const horizonMs = 24 * 3600 * 1000;
  const hbs = useQuery({
    queryKey: ["heartbeats", agentId],
    queryFn: () => api<HeartbeatStatus[]>(`/agents/${agentId}/heartbeats`),
    refetchInterval: 15000,
  });
  if (hbs.isLoading) return null;
  const list = hbs.data ?? [];
  return (
    <div>
      <p className="muted text-xs mb-3" style={{ maxWidth: 680 }}>
        Wiederkehrende Aufgaben aus <span className="mono">HEARTBEAT.md</span> (Tab Config). Die
        Control Plane legt fällige Läufe automatisch als Backlog-Aufgabe an — die Zeitachse zeigt
        die nächsten 24 Stunden. Tageszeiten gelten in Serverzeit.
      </p>
      {list.length === 0 && (
        <div className="kc-empty">
          Keine Heartbeats definiert. Im Tab Config in der Datei{" "}
          <span className="mono">HEARTBEAT.md</span> z.&nbsp;B.{" "}
          <span className="mono">- alle: 30m titel: Posteingang sichten aufgabe: …</span> anlegen.
        </div>
      )}
      {list.map((hb) => (
        <HeartbeatCard key={hb.name} hb={hb} horizonMs={horizonMs} agentId={agentId} canManage={canManage} />
      ))}
    </div>
  );
}

// --- Webhook: optionaler generischer Trigger je Agent (spec/03) ---
// POST /api/trigger/{token} legt eine Backlog-Aufgabe an und weckt den
// Agenten — für Systeme ohne Zielsystem-Plugin (CI, Cron, Zapier, Monitoring).
function WebhookTrigger({ agentId }: { agentId: string }) {
  const qc = useQueryClient();
  const [copied, setCopied] = useState(false);
  const wh = useQuery({
    queryKey: ["webhook", agentId],
    queryFn: () => api<AgentWebhook>(`/agents/${agentId}/webhook`),
  });
  const refresh = () => qc.invalidateQueries({ queryKey: ["webhook", agentId] });
  const enable = useMutation({ mutationFn: () => post<AgentWebhook>(`/agents/${agentId}/webhook`), onSuccess: refresh });
  const disable = useMutation({ mutationFn: () => del(`/agents/${agentId}/webhook`), onSuccess: refresh });

  if (wh.isLoading) return null;
  const data = wh.data;
  const url = data?.url?.startsWith("/") ? window.location.origin + data.url : data?.url;

  return (
    <div>
      <p className="muted text-xs mb-3" style={{ maxWidth: 680 }}>
        Optionaler Webhook-Trigger: ein <span className="mono">POST</span> auf die URL legt eine
        Backlog-Aufgabe an und weckt den Agenten — für Fremdsysteme ohne Zielsystem-Plugin (CI,
        Cron, Zapier, Monitoring). Payload optional als JSON{" "}
        <span className="mono">{'{"title", "body", "priority", "dedup_key"}'}</span>; alles andere
        landet als Text im Aufgaben-Body. Das Token in der URL ist das Geheimnis — Rotation macht
        die alte URL ungültig.
      </p>
      {!data?.enabled && (
        <div className="kc-empty">
          <p className="mb-3">Kein Webhook aktiv.</p>
          <button className="btn primary sm" disabled={enable.isPending} onClick={() => enable.mutate()}>
            Webhook aktivieren
          </button>
        </div>
      )}
      {data?.enabled && url && (
        <div>
          <label>Trigger-URL</label>
          <div className="flex items-center gap-2 mb-3 flex-wrap">
            <span className="mono text-xs" style={{ wordBreak: "break-all" }}>
              {url}
            </span>
            <button
              className="btn sm"
              onClick={() => {
                navigator.clipboard.writeText(url).then(() => {
                  setCopied(true);
                  setTimeout(() => setCopied(false), 1500);
                });
              }}
            >
              {copied ? "Kopiert ✓" : "Kopieren"}
            </button>
          </div>
          <label>Beispiel</label>
          <pre className="code text-xs mb-3" style={{ whiteSpace: "pre-wrap", wordBreak: "break-all" }}>
            {`curl -X POST ${url} \\\n  -H 'Content-Type: application/json' \\\n  -d '{"title": "Build fehlgeschlagen", "body": "Pipeline #123 ist rot.", "dedup_key": "pipeline-123"}'`}
          </pre>
          <div className="flex items-center gap-3">
            <button className="btn sm" disabled={enable.isPending} onClick={() => enable.mutate()} title="Neues Token, alte URL wird ungültig">
              Token rotieren
            </button>
            <button className="btn sm danger" disabled={disable.isPending} onClick={() => disable.mutate()}>
              Deaktivieren
            </button>
          </div>
        </div>
      )}
      {(enable.isError || disable.isError) && (
        <p className="danger-text text-xs mt-3">{((enable.error ?? disable.error) as Error).message}</p>
      )}
    </div>
  );
}

function Config({ agentId, canManage }: { agentId: string; canManage: boolean }) {
  const qc = useQueryClient();
  const cfg = useQuery({
    queryKey: ["config", agentId],
    queryFn: () => api<ConfigVersion>(`/agents/${agentId}/config`),
    retry: false,
  });
  const [draft, setDraft] = useState<Record<string, string> | null>(null);
  // HEARTBEAT.md immer anbieten, auch wenn ältere Configs sie nicht kennen —
  // sonst gäbe es keinen Weg, sie über die UI anzulegen.
  const files = { "SOUL.md": "", "HEARTBEAT.md": "", ...(draft ?? cfg.data?.files ?? { "ACCESS.md": "" }) };

  const save = useMutation({
    mutationFn: () => put(`/agents/${agentId}/config`, { files }),
    onSuccess: () => {
      setDraft(null);
      qc.invalidateQueries({ queryKey: ["config", agentId] });
      // ACCESS.md (tools) und EGRESS.md schreiben in die UI-Stores durch —
      // die Reiter Tools und Egress sofort nachziehen.
      qc.invalidateQueries({ queryKey: ["agent-tools", agentId] });
      qc.invalidateQueries({ queryKey: ["egress", "agent", agentId] });
      qc.invalidateQueries({ queryKey: ["heartbeats", agentId] });
    },
  });

  return (
    <div>
      <p className="muted text-xs mb-3" style={{ maxWidth: 680 }}>
        Config-as-Code: jede Änderung erzeugt eine neue Version
        {cfg.data && cfg.data.version > 0 && <> — aktuell v{cfg.data.version}</>}. ACCESS.md hält
        Zugänge und Tool-Zuweisung, EGRESS.md die Egress-Konfiguration — beide sind synchron mit
        den Reitern Tools und Egress: Änderungen hier wirken dort und umgekehrt. Referenzen, nie
        Secrets. HEARTBEAT.md definiert wiederkehrende Aufgaben, die automatisch im Backlog
        landen — je Zeile <span className="mono">- alle: 30m titel: … aufgabe: …</span> oder{" "}
        <span className="mono">- täglich: 09:00 titel: … aufgabe: …</span>.
      </p>
      {Object.entries(files)
        .sort(([x], [y]) => x.localeCompare(y))
        .map(([name, content]) => (
          <div key={name} className="mb-3">
            <label className="mono">{name}</label>
            <textarea
              className="code"
              rows={Math.min(14, Math.max(4, content.split("\n").length + 1))}
              value={content}
              readOnly={!canManage}
              onChange={(e) => setDraft({ ...files, [name]: e.target.value })}
            />
          </div>
        ))}
      {canManage && (
        <div className="flex items-center gap-3">
          <button className="btn primary sm" disabled={!draft || save.isPending} onClick={() => save.mutate()}>
            Neue Version speichern
          </button>
          {save.isError && <span className="danger-text text-xs">{(save.error as Error).message}</span>}
        </div>
      )}
    </div>
  );
}

// effectiveEgress: Basis-Allowlist der Org + ENV-Zusätze + Hosts der
// zugewiesenen Templates + eigene Hosts, dedupliziert — Muster → Quelle.
// Von AgentEgress (Egress-Reiter) und UIConfigSummary (Config-Reiter) geteilt,
// damit beide Ansichten dieselbe Wahrheit zeigen.
function effectiveEgress(
  status: EgressStatus | undefined,
  templates: EgressTemplate[],
  cfg: AgentEgressCfg | undefined,
): Map<string, string> {
  const assigned = new Set(cfg?.template_ids ?? []);
  const effective = new Map<string, string>();
  for (const d of status?.defaults ?? []) effective.set(d.pattern, "Basis");
  for (const p of status?.env ?? []) if (!effective.has(p)) effective.set(p, "ENV");
  for (const t of templates.filter((t) => assigned.has(t.id)))
    for (const h of t.hosts) if (!effective.has(h.pattern)) effective.set(h.pattern, t.name);
  for (const h of cfg?.hosts ?? []) if (!effective.has(h.pattern)) effective.set(h.pattern, "eigener Host");
  return effective;
}

// AgentSecrets: agent-eigene Secrets (haben Vorrang) plus die Sicht darauf,
// welche Org-Secrets diesen Agenten erreichen. Werte bleiben write-only.
function AgentSecrets({ agentId }: { agentId: string }) {
  const qc = useQueryClient();
  const own = useQuery({
    queryKey: ["agent-secrets", agentId],
    queryFn: () => api<SecretPreview[]>(`/agents/${agentId}/secrets`),
    retry: false,
  });
  const org = useQuery({ queryKey: ["secrets"], queryFn: () => api<SecretPreview[]>("/secrets"), retry: false });
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [check, setCheck] = useState<({ key: string } & SecretCheck) | null>(null);
  const inval = () => qc.invalidateQueries({ queryKey: ["agent-secrets", agentId] });

  const save = useMutation({
    mutationFn: () =>
      put<{ ok: boolean; check: SecretCheck }>(
        `/agents/${agentId}/secrets/${encodeURIComponent(key)}`,
        { value },
      ),
    onSuccess: (res) => {
      setCheck({ key, ...res.check });
      setKey("");
      setValue("");
      inval();
    },
  });
  const remove = useMutation({
    mutationFn: (k: string) => del(`/agents/${agentId}/secrets/${encodeURIComponent(k)}`),
    onSuccess: inval,
  });
  const invalOrg = () => qc.invalidateQueries({ queryKey: ["secrets"] });
  const assign = useMutation({
    mutationFn: (k: string) => put(`/secrets/${encodeURIComponent(k)}/agents/${agentId}`, {}),
    onSuccess: invalOrg,
  });
  const unassign = useMutation({
    mutationFn: (k: string) => del(`/secrets/${encodeURIComponent(k)}/agents/${agentId}`),
    onSuccess: invalOrg,
  });

  const ownKeys = new Set((own.data ?? []).map((s) => s.key));
  // Org-Secrets, die diesen Agenten erreichen: nur explizit zugewiesene.
  const inherited = (org.data ?? []).filter((s) => s.agent_ids.includes(agentId));
  const assignable = (org.data ?? []).filter((s) => !s.agent_ids.includes(agentId));

  return (
    <div>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        Hier weist du dem Agenten Secrets aus dem zentralen Vault (<Link to="/secrets">Secrets</Link>) zu
        und legst agent-eigene an. Agent-eigene Secrets gelten nur für diesen Agenten und haben
        Vorrang vor gleichnamigen zugewiesenen Org-Secrets.
      </p>

      <div className="card mb-4 flex gap-3 items-end flex-wrap">
        <div className="min-w-64">
          <label>Org-Secret zuweisen</label>
          <select
            value=""
            onChange={(e) => {
              if (e.target.value) assign.mutate(e.target.value);
            }}
            disabled={assign.isPending || assignable.length === 0}
          >
            <option value="">
              {assignable.length === 0 ? "— keine weiteren Org-Secrets —" : "— Secret auswählen —"}
            </option>
            {assignable.map((s) => (
              <option key={s.key} value={s.key}>
                {s.key}
              </option>
            ))}
          </select>
        </div>
        {assign.isError && <span className="danger-text text-xs">{(assign.error as Error).message}</span>}
      </div>

      {inherited.length > 0 && (
        <>
          <label>Zugewiesene Org-Secrets</label>
          {inherited.map((s) => (
            <div key={s.key} className="card mb-2 flex items-center gap-4" style={{ padding: "11px 15px", opacity: ownKeys.has(s.key) ? 0.55 : 1 }}>
              <span className="mono text-sm flex-1">{s.key}</span>
              {ownKeys.has(s.key) && (
                <span className="muted text-xs">durch agent-eigenes Secret überdeckt</span>
              )}
              <span className="mono muted text-xs">
                {s.prefix ? <span style={{ color: "var(--text-secondary)" }}>{s.prefix}</span> : null}
                ••••••••
              </span>
              <button className="btn sm" disabled={unassign.isPending} onClick={() => unassign.mutate(s.key)}>
                Zuweisung entfernen
              </button>
            </div>
          ))}
        </>
      )}
      {inherited.length === 0 && (
        <p className="muted mb-3" style={{ color: "var(--text-warning, #b45309)" }}>
          Kein Org-Secret zugewiesen — der Agent erreicht den Vault nicht.
        </p>
      )}

      <label className="mt-4">Agent-eigene Secrets</label>

      <form
        className="card mb-4 flex gap-3 items-end flex-wrap"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <div className="min-w-48">
          <label>Key</label>
          <input value={key} onChange={(e) => setKey(e.target.value)} className="mono" placeholder="zammad_token" required />
        </div>
        <div className="flex-1 min-w-52">
          <label>Wert</label>
          <input type="password" value={value} onChange={(e) => setValue(e.target.value)} required />
        </div>
        <button className="btn primary" disabled={save.isPending}>
          Speichern
        </button>
        {check && (
          <p
            className="text-xs w-full m-0"
            style={{ color: check.checked && !check.valid ? "var(--danger, #b91c1c)" : check.valid ? "var(--success, #15803d)" : "var(--text-secondary)" }}
          >
            {check.checked && check.valid && (
              <>
                <span className="mono">{check.key}</span> gespeichert — Credential live geprüft, gültig. ✓
              </>
            )}
            {check.checked && !check.valid && (
              <>
                <span className="mono">{check.key}</span> gespeichert, aber das Credential ist <strong>ungültig</strong>: {check.hint}
              </>
            )}
            {!check.checked && (
              <>
                <span className="mono">{check.key}</span> gespeichert.{check.hint ? ` ${check.hint}` : ""}
              </>
            )}
          </p>
        )}
      </form>

      {(own.data ?? []).map((s) => (
        <div key={s.key} className="card mb-2 flex items-center gap-4" style={{ padding: "11px 15px" }}>
          <span className="mono text-sm flex-1">{s.key}</span>
          <span className="badge st-triage">agent-eigen</span>
          <span className="mono muted text-xs">
            {s.prefix ? <span style={{ color: "var(--text-secondary)" }}>{s.prefix}</span> : null}
            ••••••••
          </span>
          <button className="btn sm" onClick={() => remove.mutate(s.key)}>
            Löschen
          </button>
        </div>
      ))}
      {own.data?.length === 0 && <p className="muted mb-3">Noch keine agent-eigenen Secrets.</p>}
    </div>
  );
}

function Memories({ agentId, canManage }: { agentId: string; canManage: boolean }) {
  const qc = useQueryClient();
  const mems = useQuery({
    queryKey: ["memories", agentId],
    queryFn: () => api<MemoryEntry[] | null>(`/agents/${agentId}/memories`),
  });
  const [draft, setDraft] = useState("");
  const [editId, setEditId] = useState<string | null>(null);
  const [editText, setEditText] = useState("");
  const invalidate = () => qc.invalidateQueries({ queryKey: ["memories", agentId] });

  const add = useMutation({
    mutationFn: () => post(`/agents/${agentId}/memories`, { content: draft }),
    onSuccess: () => {
      setDraft("");
      invalidate();
    },
  });
  const save = useMutation({
    mutationFn: () => patch(`/memories/${editId}`, { content: editText }),
    onSuccess: () => {
      setEditId(null);
      invalidate();
    },
  });
  const remove = useMutation({
    mutationFn: (id: string) => del(`/memories/${id}`),
    onSuccess: invalidate,
  });

  const list = mems.data ?? [];
  return (
    <div>
      {canManage && (
        <div className="card mb-3" style={{ padding: "12px 14px" }}>
          <textarea
            rows={2}
            placeholder="Wissen mitgeben, z. B. „Kunde Meier ist Bestandskunde und wird geduzt“ …"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
          />
          <div className="flex items-center gap-2 mt-2">
            <button
              className="btn primary sm"
              disabled={!draft.trim() || add.isPending}
              onClick={() => add.mutate()}
            >
              Merken
            </button>
            {add.isError && <span className="danger-text text-xs">{(add.error as Error).message}</span>}
          </div>
        </div>
      )}
      {list.length === 0 && <p className="muted">Noch nichts gelernt.</p>}
      {list.map((m) =>
        editId === m.id ? (
          <div key={m.id} className="card mb-2" style={{ padding: "12px 14px" }}>
            <textarea rows={2} value={editText} onChange={(e) => setEditText(e.target.value)} />
            <div className="flex items-center gap-2 mt-2">
              <button
                className="btn primary sm"
                disabled={!editText.trim() || save.isPending}
                onClick={() => save.mutate()}
              >
                Speichern
              </button>
              <button className="btn sm" onClick={() => setEditId(null)}>
                Abbrechen
              </button>
              {save.isError && <span className="danger-text text-xs">{(save.error as Error).message}</span>}
            </div>
          </div>
        ) : (
          <div key={m.id} className="card mb-2 flex justify-between gap-3" style={{ padding: "12px 14px" }}>
            <span className="voice text-[14.5px]">{m.content}</span>
            <span className="muted text-[11px] shrink-0 flex items-center gap-2">
              {new Date(m.created_at).toLocaleDateString("de-DE")}
              {canManage && (
                <>
                  <button
                    className="btn sm"
                    onClick={() => {
                      setEditId(m.id);
                      setEditText(m.content);
                    }}
                  >
                    Ändern
                  </button>
                  <button
                    className="btn sm danger"
                    disabled={remove.isPending}
                    onClick={() => remove.mutate(m.id)}
                  >
                    Vergessen
                  </button>
                </>
              )}
            </span>
          </div>
        ),
      )}
    </div>
  );
}

// AgentEgress: der Egress-Reiter auf der Agenten-Seite — welche Ziel-Hosts
// darf DIESER Agent erreichen? Oben die effektive Allowlist auf einen Blick,
// darunter Templates zuweisen, eigene Hosts pflegen und das Entscheidungs-Log.
function AgentEgress({ agentId, canEdit }: { agentId: string; canEdit: boolean }) {
  const qc = useQueryClient();

  const status = useQuery({ queryKey: ["egress", "status"], queryFn: () => api<EgressStatus>("/egress") });
  const templates = useQuery({ queryKey: ["egress", "templates"], queryFn: () => api<EgressTemplate[]>("/egress/templates") });
  const cfg = useQuery({ queryKey: ["egress", "agent", agentId], queryFn: () => api<AgentEgressCfg>(`/agents/${agentId}/egress`) });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["egress", "agent", agentId] });
    // Auch die Config: EGRESS.md wird daraus generiert (Config-Reiter).
    qc.invalidateQueries({ queryKey: ["config", agentId] });
  };

  const toggleTpl = useMutation({
    mutationFn: ({ tid, on }: { tid: string; on: boolean }) =>
      on ? put(`/agents/${agentId}/egress/templates/${tid}`, {}) : del(`/agents/${agentId}/egress/templates/${tid}`),
    onSuccess: invalidate,
  });
  const delHost = useMutation({ mutationFn: (id: string) => del(`/agents/${agentId}/egress/hosts/${id}`), onSuccess: invalidate });

  const assigned = new Set(cfg.data?.template_ids ?? []);

  // Effektive Allowlist: geteilte Logik mit dem Config-Reiter (UIConfigSummary);
  // die Quelle steht am Chip.
  const effective = effectiveEgress(status.data, templates.data ?? [], cfg.data);

  return (
    <div style={{ maxWidth: 780 }}>
      <p className="muted text-xs mb-4">
        Dieser Agent darf ausgehend nur die Hosts unten erreichen — alles andere blockt der
        Egress-Proxy fail-closed. Templates werden zentral unter{" "}
        <Link to="/egress/templates">Egress → Templates</Link> gepflegt und hier zugewiesen;
        eigene Hosts sind für Ausnahmen dieses Agenten.
      </p>

      <div className="card mb-5" style={{ padding: "13px 15px" }}>
        <p className="text-xs font-medium mb-2">Effektive Allowlist</p>
        <div className="flex flex-wrap gap-1">
          {[...effective.entries()].map(([pattern, source]) => (
            <span key={pattern} className={`chip${source === "ENV" ? " fixed" : ""}`}>
              {pattern}
              <span className="src">{source}</span>
            </span>
          ))}
          {effective.size === 0 && <span className="muted text-xs">leer — jede ausgehende Verbindung wird blockiert</span>}
        </div>
      </div>

      <p className="text-xs font-medium mb-2">Templates</p>
      <div className="grid gap-2 mb-5" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(240px, 1fr))" }}>
        {(templates.data ?? []).map((t) => (
          <label
            key={t.id}
            className="card flex items-start gap-2"
            style={{ padding: "10px 12px", margin: 0, cursor: canEdit ? "pointer" : "default", opacity: assigned.has(t.id) ? 1 : 0.75 }}
          >
            <input
              type="checkbox"
              style={{ width: "auto", marginTop: 2 }}
              checked={assigned.has(t.id)}
              disabled={!canEdit || toggleTpl.isPending}
              onChange={(e) => toggleTpl.mutate({ tid: t.id, on: e.target.checked })}
            />
            <span style={{ minWidth: 0 }}>
              <Link
                to={`/egress/templates/${t.id}`}
                className="block text-sm font-medium"
                style={{ color: "var(--text-primary)", textDecoration: "none" }}
                title="Template-Detailseite öffnen"
                onClick={(e) => e.stopPropagation()}
              >
                {t.name}
              </Link>
              <span className="block mono text-[11px] muted" style={{ overflowWrap: "anywhere" }}>
                {t.hosts.length === 0 ? "keine Hosts" : t.hosts.map((h) => h.pattern).join(", ")}
              </span>
            </span>
          </label>
        ))}
        {(templates.data ?? []).length === 0 && (
          <span className="muted text-xs">
            Noch keine Templates — zuerst unter <Link to="/egress/templates">Egress → Templates</Link> anlegen.
          </span>
        )}
      </div>

      <p className="text-xs font-medium mb-2">Eigene Hosts</p>
      <div className="flex flex-wrap gap-1 mb-2">
        <HostChips hosts={cfg.data?.hosts ?? []} canEdit={canEdit} onDelete={(id) => delHost.mutate(id)} emptyText="keine" />
      </div>
      {canEdit && (
        <div className="mb-5" style={{ maxWidth: 560 }}>
          <AddHostForm onAdd={(pattern, note) => post(`/agents/${agentId}/egress/hosts`, { pattern, note }).then(invalidate)} />
        </div>
      )}

      <p className="text-xs font-medium mt-5 mb-2">Letzte Egress-Entscheidungen</p>
      <EgressLogTable agentId={agentId} />
    </div>
  );
}

// AgentTools: welche Tools der angebundenen MCP-Server dieser Agent nutzen darf.
// Keine Auswahl (leere Allowlist) = alle Tools des Systems erlaubt; sobald ein
// System auf "nur ausgewählte" steht, greift die Allowlist fail-closed — die
// Control Plane weist nicht zugewiesene Tool-Aufrufe zentral ab.
function AgentTools({ agentId, canEdit }: { agentId: string; canEdit: boolean }) {
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<TargetPlugin[] | null>("/targets"),
  });
  const mcp = (targets.data ?? []).filter((t) => t.kind === "mcp");

  return (
    <div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        Weise diesem Agenten gezielt Tools der angebundenen{" "}
        <Link to="/targets">MCP-Server</Link> zu. Ohne Auswahl darf der Agent alle Tools eines
        aktivierten Servers nutzen; wählst du einzelne aus, sind nur diese erlaubt — alle anderen
        Aufrufe weist die Control Plane ab.
      </p>
      {mcp.length === 0 && (
        <p className="muted">
          Keine MCP-Server angebunden. Binde unter <Link to="/targets">Zielsysteme</Link> einen an.
        </p>
      )}
      <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(300px, 1fr))" }}>
        {mcp.map((p) => (
          <MCPToolAssign key={p.name} agentId={agentId} plugin={p} canEdit={canEdit} />
        ))}
      </div>
    </div>
  );
}

function MCPToolAssign({
  agentId,
  plugin,
  canEdit,
}: {
  agentId: string;
  plugin: TargetPlugin;
  canEdit: boolean;
}) {
  const qc = useQueryClient();
  const tools: MCPTool[] = plugin.manifest?.tools ?? [];
  const assigned = useQuery({
    queryKey: ["agent-tools", agentId, plugin.name],
    queryFn: () => api<string[]>(`/agents/${agentId}/tools/${plugin.name}`),
  });

  const [restrict, setRestrict] = useState<boolean | null>(null);
  const [sel, setSel] = useState<Set<string>>(new Set());

  // Serverzustand → lokaler Entwurf, sobald geladen (einmalig pro Fetch).
  const loaded = assigned.data;
  const effectiveRestrict = restrict ?? (loaded ? loaded.length > 0 : false);
  const effectiveSel = restrict === null && loaded ? new Set(loaded) : sel;

  const save = useMutation({
    mutationFn: (tools: string[]) => put(`/agents/${agentId}/tools/${plugin.name}`, { tools }),
    onSuccess: () => {
      setRestrict(null);
      qc.invalidateQueries({ queryKey: ["agent-tools", agentId, plugin.name] });
      // Auch die Config: TOOLS.md wird daraus generiert (Config-Reiter).
      qc.invalidateQueries({ queryKey: ["config", agentId] });
    },
  });

  const toggleSel = (name: string) => {
    const next = new Set(effectiveSel);
    next.has(name) ? next.delete(name) : next.add(name);
    setRestrict(true);
    setSel(next);
  };
  const setMode = (r: boolean) => {
    setRestrict(r);
    if (r && effectiveSel.size === 0) setSel(new Set(tools.map((t) => t.name)));
    else setSel(new Set(effectiveSel));
  };

  const dirty = restrict !== null;

  return (
    <div className="card" style={{ padding: "14px 16px", opacity: plugin.enabled ? 1 : 0.6 }}>
      <div className="flex items-center gap-2 mb-2">
        <span className="font-medium">{plugin.label || plugin.name}</span>
        <span className="mono text-xs muted">{plugin.name}</span>
        {!plugin.enabled && <span className="text-[11px] ml-auto" style={{ color: "var(--clay)" }}>deaktiviert</span>}
      </div>
      {tools.length === 0 ? (
        <p className="muted text-xs">Noch keine Tools entdeckt — unter Zielsysteme „Tools aktualisieren".</p>
      ) : (
        <>
          <div className="flex gap-3 text-xs mb-2">
            <label className="flex items-center gap-1">
              <input type="radio" checked={!effectiveRestrict} disabled={!canEdit} onChange={() => setMode(false)} />
              Alle Tools
            </label>
            <label className="flex items-center gap-1">
              <input type="radio" checked={effectiveRestrict} disabled={!canEdit} onChange={() => setMode(true)} />
              Nur ausgewählte
            </label>
          </div>
          <ul className="text-xs" style={{ listStyle: "none", padding: 0, margin: 0 }}>
            {tools.map((t) => (
              <li key={t.name} className="mb-1">
                <label className="flex items-start gap-2" style={{ opacity: effectiveRestrict ? 1 : 0.5 }}>
                  <input
                    type="checkbox"
                    style={{ marginTop: 2 }}
                    disabled={!canEdit || !effectiveRestrict}
                    checked={effectiveRestrict ? effectiveSel.has(t.name) : true}
                    onChange={() => toggleSel(t.name)}
                  />
                  <span>
                    <span className="mono">{t.name}</span>
                    {t.description && <span className="muted"> — {t.description.split("\n")[0]}</span>}
                  </span>
                </label>
              </li>
            ))}
          </ul>
          {canEdit && (
            <div className="flex items-center gap-2 mt-3">
              <span className="text-[11px] muted">
                {effectiveRestrict ? `${effectiveSel.size} von ${tools.length} erlaubt` : "alle erlaubt"}
              </span>
              <button
                className="btn sm primary ml-auto"
                disabled={!dirty || save.isPending || (effectiveRestrict && effectiveSel.size === 0)}
                onClick={() => save.mutate(effectiveRestrict ? [...effectiveSel] : [])}
              >
                Speichern
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

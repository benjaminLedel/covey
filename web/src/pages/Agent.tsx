import { useState, useRef, useEffect, useMemo, useCallback, type CSSProperties, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import i18n from "../i18n";
import {
  api,
  post,
  patch,
  del,
  put,
  assistStatus,
  configAssist,
  type AssistMessage,
  type AssistProposal,
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
  type WikiLogEntry,
  type WikiHealth,
  type WikiFinding,
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
import { ActivityFeed, subAgentMark } from "../components/ActivityFeed";
import { fmtDelta } from "../format";
import { TargetIcon } from "../components/TargetIcon";
import { WikiTypeIcon, WikiOpIcon } from "../components/WikiIcon";
import { Markdown } from "../components/Markdown";
import ProfileForm from "../components/ProfileForm";
import { AddHostForm, EgressLogTable, HostChips } from "../components/EgressBits";
import { SecretValue } from "./Secrets";
import { generateAgentName } from "../names";

const canManage = (role: string) => role === "platform_admin" || role === "agent_owner";
const canKill = (role: string) => canManage(role) || role === "security";
const canSecrets = (role: string) => role === "platform_admin" || role === "security";

export default function AgentPage({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const qc = useQueryClient();
  const agent = useQuery({ queryKey: ["agent", id], queryFn: () => api<Agent>(`/agents/${id}`) });
  // Tab-Zustand lebt in der URL (?tab=…) — echte Navigation: teilbare Links,
  // Browser-Vor/Zurück. Der memory-Tab führt zusätzlich ?page=<slug> mit.
  const [sp, setSp] = useSearchParams();
  const tab = ((sp.get("tab") as
    | "backlog"
    | "heartbeat"
    | "webhook"
    | "recording"
    | "config"
    | "memory"
    | "secrets"
    | "egress"
    | "tools"
    | "einstellungen") || "backlog");
  const setTab = (key: typeof tab) =>
    setSp(
      (prev) => {
        const n = new URLSearchParams(prev);
        n.set("tab", key);
        if (key !== "memory") n.delete("page"); // Wiki-Seite nur im memory-Tab
        return n;
      },
      { replace: false },
    );
  const [recTask, setRecTask] = useState<{ id: string; title: string } | null>(null);

  const act = useMutation({
    mutationFn: (action: string) => post(`/agents/${id}/${action}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["agent", id] });
      qc.invalidateQueries({ queryKey: ["agents"] });
    },
  });

  if (agent.isLoading) return null;
  if (agent.isError || !agent.data) return <p className="danger-text">{t("agent.notFound")}</p>;
  const a = agent.data;

  return (
    <div>
      <div className="text-sm secondary mb-3">
        <Link to="/" style={{ color: "inherit" }}>
          {t("agent.breadcrumb")}
        </Link>{" "}
        / <b style={{ color: "var(--text-primary)", fontWeight: 500 }}>{a.display_name}</b>
      </div>

      <div className="flex items-center gap-3 mb-5 flex-wrap">
        <h1 className="text-[22px]">{a.display_name}</h1>
        <span className={`badge st-${a.killed ? "killed" : a.status}`}>
          {t(`status.${a.killed ? "killed" : a.status}`, a.status)}
        </span>
        {(a.status === "working" || a.status === "triage" || a.status === "triggered") && (
          <span className="live-dot" title={t("agent.sandbox")} />
        )}
        <span className="muted text-xs mono">
          runtime: {a.runtime}
          {a.model && ` · ${a.model}`}
        </span>
        <span className="ml-auto" />
        {canManage(me.Role) && (
          <button className="btn sm" onClick={() => act.mutate("wake")}>
            {t("agent.wake")}
          </button>
        )}
        {canKill(me.Role) &&
          (a.killed ? (
            <button className="btn sm" onClick={() => act.mutate("resume")}>
              {t("agent.resume")}
            </button>
          ) : (
            <button className="btn sm danger" onClick={() => act.mutate("kill")} title="Kill-Switch">
              {t("agent.stop")}
            </button>
          ))}
      </div>

      <CostBar agentId={a.id} budget={a.budget_usd} />

      <div className="flex gap-1 mb-4 mt-5" style={{ borderBottom: "0.5px solid var(--border)" }}>
        {(
          [
            ["backlog", t("agent.tabs.backlog")],
            ["heartbeat", t("agent.tabs.heartbeat")],
            ...(canManage(me.Role) ? ([["webhook", t("agent.tabs.webhook")]] as const) : []),
            ["recording", t("agent.tabs.recording")],
            ["memory", t("agent.tabs.memory")],
            ["tools", t("agent.tabs.tools")],
            ["egress", t("agent.tabs.egress")],
            ...(canSecrets(me.Role) ? ([["secrets", t("agent.tabs.secrets")]] as const) : []),
            ["config", t("agent.tabs.config")],
            ["einstellungen", t("agent.tabs.settings")],
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
      {tab === "config" && (
        <Config
          agentId={a.id}
          slug={a.slug}
          displayName={a.display_name}
          canManage={canManage(me.Role)}
          canExport={canManage(me.Role) || me.Role === "security"}
        />
      )}
      {tab === "memory" && <Memories agentId={a.id} canManage={canManage(me.Role)} />}
      {tab === "egress" && <AgentEgress agentId={a.id} canEdit={canSecrets(me.Role)} />}
      {tab === "tools" && <AgentTools agentId={a.id} canEdit={canSecrets(me.Role)} />}
      {tab === "secrets" && canSecrets(me.Role) && <AgentSecrets agentId={a.id} />}
      {tab === "einstellungen" && <AgentSettings agent={a} editable={canManage(me.Role)} />}
    </div>
  );
}

function AgentSettings({ agent, editable }: { agent: Agent; editable: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
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
  const setSlug = useMutation({
    mutationFn: (slug: string) => patch(`/agents/${agent.id}/slug`, { slug }),
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
  const setRecordingLevel = useMutation({
    mutationFn: (level: string) => patch(`/agents/${agent.id}/recording-level`, { level }),
    onSuccess: invalidate,
  });
  const setWarmSandbox = useMutation({
    mutationFn: (warm: boolean) => patch(`/agents/${agent.id}/warm-sandbox`, { warm }),
    onSuccess: invalidate,
  });
  const setBudget = useMutation({
    mutationFn: (budgetUSD: number) => post(`/agents/${agent.id}/budget`, { budget_usd: budgetUSD }),
    onSuccess: invalidate,
  });
  const deleteAgent = useMutation({
    mutationFn: () => del(`/agents/${agent.id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["agents"] });
      navigate("/");
    },
  });
  const [confirmDelete, setConfirmDelete] = useState(false);

  const anyError = [setName, setSlug, setRuntime, setModel, setMaxTurns, setRecordingLevel, setBudget].find(
    (m) => m.isError,
  );

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
    <>
    <div className="card mb-4" style={{ maxWidth: 760 }}>
      <div className="text-sm font-medium mb-1">{t("agent.settings.profile")}</div>
      <p className="muted text-xs mt-0 mb-3">{t("agent.settings.profileHint")}</p>
      <ProfileForm
        human={agent}
        endpoint={`/agents/${agent.id}/profile`}
        readOnly={!editable}
        onSaved={invalidate}
      />
    </div>
    <div className="card" style={{ maxWidth: 760, padding: "6px 18px 14px" }}>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.name")}</span>
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
              title={t("agent.settings.rollDice")}
              disabled={setName.isPending}
              onClick={() => setName.mutate(generateAgentName().name)}
            >
              🎲
            </button>
          )}
        </span>
        <span className="muted text-xs">{t("agent.settings.nameHint")}</span>
      </div>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.slug")}</span>
        <span className="flex items-center gap-2">
          <input
            key={`slug:${agent.slug}`}
            defaultValue={agent.slug}
            disabled={!editable || setSlug.isPending}
            className="mono"
            onBlur={(e) => {
              const v = e.target.value.trim();
              if (v && v !== agent.slug) setSlug.mutate(v);
              else e.target.value = agent.slug;
            }}
            onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
            style={{ flex: 1 }}
          />
        </span>
        <span className="muted text-xs">
          {setSlug.isError
            ? <span style={{ color: "var(--error)" }}>{String((setSlug.error as Error)?.message ?? t("agent.settings.slugError"))}</span>
            : t("agent.settings.slugHint")}
        </span>
      </div>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.runtime")}</span>
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
        <span className="muted text-xs">{t("agent.settings.runtimeHint")}</span>
      </div>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.model")}</span>
        <input
          key={`model:${agent.model}`}
          defaultValue={agent.model}
          placeholder={t("agent.settings.modelPlaceholder")}
          disabled={!editable || setModel.isPending}
          onBlur={(e) => {
            const v = e.target.value.trim();
            if (v !== agent.model) setModel.mutate(v);
          }}
          onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
          className="mono"
        />
        <span className="muted text-xs">{t("agent.settings.modelHint")}</span>
      </div>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.maxTurns")}</span>
        <input
          key={`turns:${agent.max_turns}`}
          type="number"
          min={0}
          defaultValue={agent.max_turns || ""}
          placeholder={t("agent.settings.maxTurnsPlaceholder")}
          disabled={!editable || setMaxTurns.isPending}
          onBlur={(e) => {
            const v = Math.max(0, Math.trunc(Number(e.target.value) || 0));
            if (v !== agent.max_turns) setMaxTurns.mutate(v);
          }}
          onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
          className="mono"
        />
        <span className="muted text-xs">{t("agent.settings.maxTurnsHint")}</span>
      </div>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.recordingLevel")}</span>
        <select
          key={`reclvl:${agent.recording_level}`}
          defaultValue={agent.recording_level || ""}
          disabled={!editable || setRecordingLevel.isPending}
          onChange={(e) => {
            if (e.target.value !== (agent.recording_level || "")) setRecordingLevel.mutate(e.target.value);
          }}
        >
          <option value="">{t("agent.settings.recordingInherit")}</option>
          <option value="minimal">{t("agent.settings.recordingMinimal")}</option>
          <option value="standard">{t("agent.settings.recordingStandard")}</option>
          <option value="full">{t("agent.settings.recordingFull")}</option>
        </select>
        <span className="muted text-xs">{t("agent.settings.recordingHint")}</span>
      </div>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.warmSandbox")}</span>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={agent.warm_sandbox}
            disabled={!editable || setWarmSandbox.isPending}
            onChange={(e) => setWarmSandbox.mutate(e.target.checked)}
          />
          {agent.warm_sandbox ? t("agent.settings.warmOn") : t("agent.settings.warmOff")}
        </label>
        <span className="muted text-xs">{t("agent.settings.warmHint")}</span>
      </div>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.budget")}</span>
        <input
          key={`budget:${agent.budget_usd}`}
          type="number"
          min={0}
          step="0.01"
          defaultValue={agent.budget_usd || ""}
          placeholder={t("agent.settings.budgetPlaceholder")}
          disabled={!editable || setBudget.isPending}
          onBlur={(e) => {
            const v = Math.max(0, Number(e.target.value) || 0);
            if (v !== agent.budget_usd) setBudget.mutate(v);
          }}
          onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
          className="mono"
        />
        <span className="muted text-xs">{t("agent.settings.budgetHint")}</span>
      </div>
      <div style={{ ...row, borderBottom: "none" }}>
        <span className="text-sm">{t("agent.settings.diagnostics")}</span>
        <a
          className="btn sm"
          href={`/api/v1/agents/${agent.id}/diagnostics`}
          download={`diagnostics-${agent.slug}.json`}
        >
          {t("agent.settings.diagnosticsExport")}
        </a>
        <span className="muted text-xs">{t("agent.settings.diagnosticsHint")}</span>
      </div>
      {!editable && (
        <p className="muted text-xs mt-2">{t("agent.settings.readOnly")}</p>
      )}
      {anyError && <p className="danger-text text-xs mt-2">{String(anyError.error)}</p>}
      {editable && (
        <div style={{ marginTop: 24, paddingTop: 14, borderTop: "0.5px solid var(--border)" }}>
          <p className="text-xs muted mb-2">{t("agent.settings.dangerZone")}</p>
          {!confirmDelete ? (
            <button className="btn sm danger" onClick={() => setConfirmDelete(true)}>
              {t("agent.settings.deleteAgent")}
            </button>
          ) : (
            <div className="flex items-center gap-3">
              <span className="text-xs" style={{ color: "var(--danger, #b91c1c)" }}>
                {t("agent.settings.deleteConfirm", { name: agent.display_name })}
              </span>
              <button
                className="btn sm danger"
                disabled={deleteAgent.isPending}
                onClick={() => deleteAgent.mutate()}
              >
                {t("agent.settings.deleteYes")}
              </button>
              <button className="btn sm" onClick={() => setConfirmDelete(false)}>
                {t("agent.settings.cancel")}
              </button>
            </div>
          )}
          {deleteAgent.isError && (
            <p className="danger-text text-xs mt-2">{String((deleteAgent.error as Error)?.message ?? "Fehler")}</p>
          )}
        </div>
      )}
    </div>
    </>
  );
}

function CostBar({ agentId, budget }: { agentId: string; budget: number }) {
  const { t } = useTranslation();
  const cost = useQuery({
    queryKey: ["cost", agentId],
    queryFn: () => api<CostSummary>(`/agents/${agentId}/cost`),
  });
  const c = cost.data;
  if (!c) return null;
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  return (
    <div className="card flex gap-8 text-sm">
      <div>
        <div className="muted text-xs">{t("agent.cost.total")}</div>
        <div className="font-medium">{c.total_usd.toFixed(4)} $</div>
      </div>
      <div>
        <div className="muted text-xs">{t("agent.cost.tokens")}</div>
        <div className="font-medium">
          {c.input_tokens.toLocaleString(locale)} / {c.output_tokens.toLocaleString(locale)}
        </div>
      </div>
      <div>
        <div className="muted text-xs">{t("agent.cost.runs")}</div>
        <div className="font-medium">{c.entries}</div>
      </div>
      <div>
        <div className="muted text-xs">{t("agent.cost.budget")}</div>
        <div className="font-medium">{budget > 0 ? `${budget.toFixed(2)} $` : t("agent.cost.noCap")}</div>
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
  const { t } = useTranslation();
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
  const inStage = (tk: Task, id: string | null) =>
    id === null ? !tk.stage_id || !known.has(tk.stage_id) : tk.stage_id === id;
  const orphans = (tasks.data ?? []).filter((tk) => inStage(tk, null));
  const columns: { id: string | null; name: string; color: string }[] = [
    ...(orphans.length ? [{ id: null, name: t("agent.backlog.noStageColumn"), color: "var(--text-muted)" }] : []),
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
          <label>{t("agent.backlog.newTask")}</label>
          <input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder={t("agent.backlog.titlePlaceholder")}
            className="mb-2"
            required
          />
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder={t("agent.backlog.descPlaceholder")}
            rows={2}
            className="mb-2"
          />
          <button className="btn primary sm" disabled={create.isPending}>
            {t("agent.backlog.addToBacklog")}
          </button>
        </form>
      )}
      {tasks.data && stages.data && (
        <>
          <div className="flex items-center gap-2 mb-3">
            <span className="muted text-xs">
              {stageList.length === 0
                ? t("agent.backlog.noStages")
                : canManage
                  ? t("agent.backlog.stagesInfo")
                  : t("agent.backlog.stagesReadonly")}
            </span>
            <button className="btn sm" style={{ marginLeft: "auto" }} onClick={() => setShowArchive((v) => !v)}>
              {showArchive ? t("agent.backlog.hideArchive") : t("agent.backlog.showArchive")}
            </button>
            {canManage && (
              <>
                <button
                  className="btn sm"
                  disabled={cleanupCount === 0 || cleanup.isPending}
                  title="Archiviert alle erledigten, fehlgeschlagenen und verworfenen Aufgaben — nichts wird gelöscht."
                  onClick={() => cleanup.mutate()}
                >
                  {t("agent.backlog.cleanup")}{cleanupCount > 0 ? ` (${cleanupCount})` : ""}
                </button>
                <button className="btn sm" onClick={() => setEditing((v) => !v)}>
                  {editing ? t("agent.backlog.doneEditing") : t("agent.backlog.editColumns")}
                </button>
              </>
            )}
          </div>

          {editing && canManage && <StageEditor agentId={agentId} stages={stageList} />}

          {columns.length === 0 ? (
            <p className="muted">{t("agent.backlog.empty")}</p>
          ) : (
            <div className="kanban" style={{ ["--kcols" as string]: columns.length }}>
              {columns.map((col) => {
                const all = (tasks.data ?? [])
                  .filter((tk) => inStage(tk, col.id))
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
                      <span className="knm" title={col.name}>{col.name}</span>
                      <span className="n">{all.length}</span>
                    </div>
                    {items.map((tk) => (
                      <TaskCard
                        key={tk.id}
                        task={tk}
                        agentId={agentId}
                        canManage={canManage}
                        onShowRecording={onShowRecording}
                        onDragStart={() => setDragTask(tk.id)}
                        onDragEnd={() => setDragTask(null)}
                      />
                    ))}
                    {all.length === 0 && <div className="kc-empty">—</div>}
                    {hidden > 0 && (
                      <div className="kc-empty">{t("agent.backlog.hiddenOlder", { count: hidden })}</div>
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
  const { t } = useTranslation();
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
      <label>{t("agent.stageEditor.title")}</label>
      {stages.map((s, i) => (
        <div key={s.id} className="flex items-center gap-2 mb-2">
          <button
            type="button"
            className="dot-btn"
            title={t("agent.stageEditor.changeColor")}
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
            {t("agent.stageEditor.delete")}
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
          placeholder={t("agent.stageEditor.newColumn")}
          required
        />
        <button className="btn primary sm" disabled={create.isPending}>
          {t("agent.stageEditor.add")}
        </button>
      </form>
    </div>
  );
}

function relTime(iso: string): string {
  const min = Math.round((Date.now() - new Date(iso).getTime()) / 60000);
  if (min < 1) return i18n.t("activity.timeJustNow");
  if (min < 60) return i18n.t("activity.timeMinutes", { n: min });
  const h = Math.round(min / 60);
  if (h < 24) return i18n.t("activity.timeHours", { n: h });
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  return new Date(iso).toLocaleDateString(locale);
}

// originLabel macht die Herkunft lesbar. Zwei Formen tragen eine ID bzw. einen
// Slug im Text: "continuation:<task-id>" (Fortsetzung eines am Turn-Limit
// abgebrochenen Laufs) und "agent:<slug>" (der Agent hat die Aufgabe selbst
// angelegt oder sie wurde ihm delegiert). Alles andere bleibt, wie es ist.
function originLabel(origin: string): string {
  if (origin.startsWith("continuation:")) return i18n.t("agent.backlog.originContinuation");
  if (origin.startsWith("agent:")) return i18n.t("agent.backlog.originAgent", { slug: origin.slice(6) });
  if (origin === "heartbeat") return i18n.t("agent.backlog.originHeartbeat");
  return origin;
}

const terminalStates = ["done", "failed", "cancelled"];

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
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const qc = useQueryClient();
  const invalBacklog = () => qc.invalidateQueries({ queryKey: ["backlog", agentId] });
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
        ? t("agent.backlog.waitingOn", { key: task.correlation_key })
        : t("agent.backlog.blocked")
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
        <span className={`badge st-${task.state} kc-state`}>{t(`status.${task.state}`, task.state)}</span>
        <span className="font-medium min-w-0 truncate">{task.title}</span>
        {archived && <span className="muted text-[11px] shrink-0">{t("agent.backlog.archived")}</span>}
        <span className="kc-prio">P{task.priority}</span>
      </div>
      <div className="t">{subtitle} · {originLabel(task.origin)}</div>
      {open && (
        <div className="kc-detail fade">
          {task.body && (
            <pre className="voice whitespace-pre-wrap m-0 mb-2 secondary" style={{ fontFamily: "var(--voice)" }}>{task.body}</pre>
          )}
          {task.correlation_key && (
            <p>
              <span className="muted">{t("agent.backlog.waitingOn", { key: "" }).replace(" ", "")}:</span>{" "}
              <span className="mono">{task.correlation_key}</span>
            </p>
          )}
          {task.runtime_session_id && (
            <p>
              <span className="muted">runtime-session:</span> <span className="mono">{task.runtime_session_id}</span>
            </p>
          )}
          {task.parent_task_id && (
            <p>
              <span className="muted">{t("agent.backlog.parentTask")}:</span>{" "}
              <span className="mono">{task.parent_task_id}</span>
            </p>
          )}
          {task.result && <p style={{ color: "var(--text-success)" }}>{task.result}</p>}
          {task.error && <p className="danger-text">{task.error}</p>}
          {(notes.data?.length ?? 0) > 0 && (
            <div className="mt-2 mb-2">
              <div className="muted text-xs mb-1">{t("agent.backlog.agentNotes")}</div>
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
              {t("agent.backlog.showRecording")}
            </button>
            {canManage && (task.state === "failed" || task.state === "cancelled") && (
              <button
                className="btn sm"
                disabled={retry.isPending}
                title={t("agent.backlog.rescheduleHint")}
                onClick={(e) => {
                  e.stopPropagation();
                  retry.mutate();
                }}
              >
                {t("agent.backlog.reschedule")}
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
                {t("agent.backlog.archive")}
              </button>
            )}
            {canManage && !terminal && (
              <button
                className="btn sm danger"
                disabled={cancel.isPending}
                onClick={(e) => {
                  e.stopPropagation();
                  if (confirm(t("agent.backlog.discardConfirm", { title: task.title }))) cancel.mutate();
                }}
              >
                {t("agent.backlog.discard")}
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
  const { t } = useTranslation();
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
  return (
    <div>
      <div className="flex items-center gap-2 mb-3">
        {taskFilter && (
          <>
            <span className="badge st-triage">{t("agent.recording.taskFilter", { title: taskFilter.title })}</span>
            <button className="btn sm" onClick={onClearFilter}>
              {t("agent.recording.allEvents")}
            </button>
          </>
        )}
        <span className="flex-1" />
        <div className="seg" role="tablist" aria-label="Ansicht">
          <button
            className={view === "feed" ? "active" : ""}
            onClick={() => setView("feed")}
            title={t("agent.recording.readableTitle")}
          >
            {t("agent.recording.readableView")}
          </button>
          <button
            className={view === "raw" ? "active" : ""}
            onClick={() => setView("raw")}
            title={t("agent.recording.rawTitle")}
          >
            {t("agent.recording.rawView")}
          </button>
        </div>
      </div>
      <div className="card">
        {list.length === 0 && (
          <p className="muted m-0">
            {taskFilter ? t("agent.recording.emptyFiltered") : t("agent.recording.emptyAll")}
          </p>
        )}
        {list.length > 0 &&
          (view === "feed" ? (
            <ActivityFeed events={list} truncated={list.length >= 500} />
          ) : (
            [...list].reverse().map((e) => <RecordingItem key={e.id} event={e} />)
          ))}
      </div>
    </div>
  );
}

function summarize(e: RecordingEvent): { text: string; mono?: boolean; muted?: boolean; danger?: boolean } {
  const p = (typeof e.payload === "object" && e.payload !== null ? e.payload : {}) as Record<string, any>;
  switch (e.kind) {
    case "lifecycle": {
      if (p.status === "task_done") return { text: i18n.t("activity.taskDone") };
      const label = i18n.t(`status.${p.status as string}`, p.status as string);
      return { text: i18n.t("activity.statusChange", { label }), muted: p.status === "sleeping" };
    }
    case "credential": {
      const ttl = p.ttl_secs ? i18n.t("activity.credentialTtl", { min: Math.round(p.ttl_secs / 60) }) : "";
      if (p.granted) return {
        text: i18n.t("activity.credentialGranted", {
          system: p.system,
          proactive: p.proactive ? i18n.t("activity.credentialProactive") : "",
          ttl,
        })
      };
      return {
        text: i18n.t("activity.credentialDenied", {
          system: p.system,
          reason: p.reason ? i18n.t("activity.credentialDeniedReason", { reason: p.reason }) : "",
        }),
        danger: true,
      };
    }
    case "approval": {
      const decision =
        p.decision === "auto-allow"
          ? i18n.t("activity.approvalAuto")
          : i18n.t(`status.${p.decision as string}`, p.decision as string);
      return { text: `${p.action} — ${decision}`, danger: p.decision === "denied" };
    }
    case "guardrail":
      return {
        text: i18n.t("activity.guardrailTriggered", {
          rule: p.rule ?? "",
          what: (p.action ?? p.system) ? i18n.t("activity.guardrailWhat", { what: p.action ?? p.system ?? "" }) : "",
          pattern: p.pattern ? i18n.t("activity.guardrailPattern", { pattern: p.pattern }) : "",
        }),
        danger: true,
      };
    case "action":
      return { text: i18n.t("activity.targetAction", { action: p.action ?? "?" }), mono: true };
    case "runtime":
      return summarizeRuntime(p);
  }
  return { text: JSON.stringify(e.payload), mono: true };
}

function summarizeRuntime(p: Record<string, any>): { text: string; mono?: boolean; muted?: boolean; danger?: boolean } {
  if (typeof p.text === "string" && !p.message) return { text: p.text };
  switch (p.type) {
    case "system":
      return {
        text: i18n.t("activity.sessionStarted", {
          model: p.model ? i18n.t("activity.withModel", { model: p.model }) : "",
        }),
        muted: true,
      };
    case "rate_limit_event":
      return { text: i18n.t("activity.rateLimitUpdated"), muted: true };
    case "assistant": {
      const blocks: any[] = Array.isArray(p.message?.content) ? p.message.content : [];
      const parts: string[] = [];
      for (const b of blocks) {
        if (b.type === "text" && b.text) parts.push(truncate(b.text, 400));
        if (b.type === "tool_use") parts.push(`⚙ ${b.name}(${truncate(compactInput(b.input), 120)})`);
      }
      return parts.length ? { text: parts.join("  ·  ") } : { text: i18n.t("activity.runtimeResponse"), muted: true };
    }
    case "user": {
      const blocks: any[] = Array.isArray(p.message?.content) ? p.message.content : [];
      const res = blocks.find((b) => b.type === "tool_result");
      if (res) {
        const body = typeof res.content === "string" ? res.content : JSON.stringify(res.content);
        return { text: i18n.t("activity.toolResultText", { text: truncate(body, 200) }), mono: true, muted: true };
      }
      return { text: i18n.t("activity.runtimeInput"), muted: true };
    }
    case "result": {
      const cost = p.total_cost_usd ? ` · $${Number(p.total_cost_usd).toFixed(4)}` : "";
      const tok = p.usage?.input_tokens ? ` · ${p.usage.input_tokens}→${p.usage.output_tokens} Tokens` : "";
      if (p.is_error) return { text: i18n.t("activity.runFailedText", { text: truncate(p.result ?? "", 300) }), danger: true };
      return { text: i18n.t("activity.runResultText", { text: truncate(p.result ?? "", 400), cost, tokens: tok }) };
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
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  // Rohansicht und erzählende Ansicht müssen dasselbe sagen: Gehört die Zeile
  // zu einem Sub-Lauf, steht das hier als Badge — dort als eigener Block.
  const sub = subAgentMark(event);
  return (
    <div className="timeline-item">
      <span className={`kind-tag kind-${event.kind}`}>{event.kind}</span>
      {sub && <span className="kind-tag kind-subagent">{i18n.t("activity.subAgentBadge")}</span>}
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
        {new Date(event.created_at).toLocaleTimeString(locale)}
      </span>
    </div>
  );
}

function scheduleLabel(hb: HeartbeatStatus): string {
  if (hb.every_seconds) return i18n.t("agent.heartbeat.schedule_interval", { delta: fmtDelta(hb.every_seconds * 1000) });
  return i18n.t("agent.heartbeat.schedule_daily", { time: hb.daily_at });
}

function fmtRunChip(d: Date): string {
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  const time = d.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" });
  if (d.toDateString() === new Date().toDateString()) return time;
  return `${d.toLocaleDateString(locale, { weekday: "short" })} ${time}`;
}

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

function HeartbeatTimeline({ runs, horizonMs }: { runs: Date[]; horizonMs: number }) {
  const { t } = useTranslation();
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
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
            title={r.toLocaleString(locale)}
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
        <span>{t("agent.heartbeat.now")}</span>
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
  const { t } = useTranslation();
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
        {hb.source === "system" && (
          <span className="badge st-pro" title={t("agent.heartbeat.systemHint")}>
            {t("agent.heartbeat.system")}
          </span>
        )}
        {hb.only_if && (
          <span className="badge" title={t("agent.heartbeat.onlyIfHint", { system: hb.only_if })}>
            {t("agent.heartbeat.onlyIf", { system: hb.only_if })}
          </span>
        )}
        {hb.pending && (
          <span className="badge st-blocked" title={t("agent.heartbeat.taskOpenHint")}>
            {t("agent.heartbeat.taskOpen")}
          </span>
        )}
        <span className="ml-auto muted text-xs">
          {t("agent.heartbeat.lastRun", { delta: fmtDelta(Date.now() - new Date(hb.last_fired_at).getTime()) })}
        </span>
        {canManage && (
          <button
            className="btn sm"
            disabled={fire.isPending || hb.pending}
            title={hb.pending ? t("agent.heartbeat.firePendingHint") : t("agent.heartbeat.fireHint")}
            onClick={() => fire.mutate()}
          >
            {fire.isPending ? t("agent.heartbeat.running") : t("agent.heartbeat.fireNow")}
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
            ? t("agent.heartbeat.overdueWithPending")
            : t("agent.heartbeat.overdue")
          : t("agent.heartbeat.nextRun", { delta: fmtDelta(next.getTime() - Date.now()) })}
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
  const { t } = useTranslation();
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
        {t("agent.heartbeat.desc")}
      </p>
      {list.length === 0 && (
        <div className="kc-empty">
          {t("agent.heartbeat.noHeartbeats")}
        </div>
      )}
      {list.map((hb) => (
        <HeartbeatCard key={hb.name} hb={hb} horizonMs={horizonMs} agentId={agentId} canManage={canManage} />
      ))}
    </div>
  );
}

function WebhookTrigger({ agentId }: { agentId: string }) {
  const { t } = useTranslation();
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
        {t("agent.webhook.desc")}
      </p>
      {!data?.enabled && (
        <div className="kc-empty">
          <p className="mb-3">{t("agent.webhook.noWebhook")}</p>
          <button className="btn primary sm" disabled={enable.isPending} onClick={() => enable.mutate()}>
            {t("agent.webhook.activate")}
          </button>
        </div>
      )}
      {data?.enabled && url && (
        <div>
          <label>{t("agent.webhook.triggerUrl")}</label>
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
              {copied ? t("agent.webhook.copied") : t("agent.webhook.copy")}
            </button>
          </div>
          <label>{t("agent.webhook.example")}</label>
          <pre className="code text-xs mb-3" style={{ whiteSpace: "pre-wrap", wordBreak: "break-all" }}>
            {`curl -X POST ${url} \\\n  -H 'Content-Type: application/json' \\\n  -d '{"title": "Build fehlgeschlagen", "body": "Pipeline #123 ist rot.", "dedup_key": "pipeline-123"}'`}
          </pre>
          <div className="flex items-center gap-3">
            <button className="btn sm" disabled={enable.isPending} onClick={() => enable.mutate()} title="Neues Token, alte URL wird ungültig">
              {t("agent.webhook.rotate")}
            </button>
            <button className="btn sm danger" disabled={disable.isPending} onClick={() => disable.mutate()}>
              {t("agent.webhook.deactivate")}
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

function Config({
  agentId,
  slug,
  displayName,
  canManage,
  canExport,
}: {
  agentId: string;
  slug: string;
  displayName: string;
  canManage: boolean;
  canExport: boolean;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const cfg = useQuery({
    queryKey: ["config", agentId],
    queryFn: () => api<ConfigVersion>(`/agents/${agentId}/config`),
    retry: false,
  });
  const [draft, setDraft] = useState<Record<string, string> | null>(null);
  const [savingTemplate, setSavingTemplate] = useState(false);
  const [templateName, setTemplateName] = useState("");
  const [templateDesc, setTemplateDesc] = useState("");
  const files = { "SOUL.md": "", "HEARTBEAT.md": "", ...(draft ?? cfg.data?.files ?? { "ACCESS.md": "" }) };

  const save = useMutation({
    mutationFn: () => put(`/agents/${agentId}/config`, { files }),
    onSuccess: () => {
      setDraft(null);
      qc.invalidateQueries({ queryKey: ["config", agentId] });
      qc.invalidateQueries({ queryKey: ["agent-tools", agentId] });
      qc.invalidateQueries({ queryKey: ["egress", "agent", agentId] });
      qc.invalidateQueries({ queryKey: ["heartbeats", agentId] });
    },
  });

  const saveTemplate = useMutation({
    mutationFn: () =>
      post("/templates", { name: templateName.trim(), description: templateDesc.trim(), from_agent_id: agentId }),
    onSuccess: () => {
      setSavingTemplate(false);
      setTemplateName("");
      setTemplateDesc("");
      qc.invalidateQueries({ queryKey: ["templates"] });
    },
  });

  // Bundle-Import: überschreibt NUR die Config dieses Agenten aus einer
  // Bundle-JSON (Stammdaten, Secrets, Guard-Rails etc. bleiben unangetastet).
  const importRef = useRef<HTMLInputElement>(null);
  const [importError, setImportError] = useState("");
  const importCfg = useMutation({
    mutationFn: (bundle: unknown) => post<ConfigVersion>(`/agents/${agentId}/config/import`, bundle),
    onSuccess: () => {
      setDraft(null);
      setImportError("");
      qc.invalidateQueries({ queryKey: ["config", agentId] });
      qc.invalidateQueries({ queryKey: ["agent-tools", agentId] });
      qc.invalidateQueries({ queryKey: ["egress", "agent", agentId] });
      qc.invalidateQueries({ queryKey: ["heartbeats", agentId] });
    },
    onError: (e) => setImportError(String((e as Error)?.message)),
  });
  const pickBundle = async (f: File | undefined) => {
    if (!f) return;
    setImportError("");
    if (!window.confirm(t("agent.config.importConfirm"))) return;
    try {
      importCfg.mutate(JSON.parse(await f.text()));
    } catch {
      setImportError(t("agent.config.importInvalidJson"));
    }
  };

  return (
    <div>
      {canExport && (
        <div className="flex gap-2 mb-2 justify-end">
          {canManage && !savingTemplate && (
            <button
              className="btn sm"
              onClick={() => { setTemplateName(displayName); setSavingTemplate(true); }}
              title="Aktuelle Konfiguration als wiederverwendbare Vorlage speichern"
            >
              {t("agent.config.saveTemplate")}
            </button>
          )}
          {savingTemplate && (
            <form
              className="flex gap-2 items-center"
              onSubmit={(e) => { e.preventDefault(); saveTemplate.mutate(); }}
            >
              <input
                autoFocus
                placeholder={t("agent.config.templateName")}
                value={templateName}
                onChange={(e) => setTemplateName(e.target.value)}
                style={{ width: 180 }}
                required
              />
              <input
                placeholder={t("agent.config.templateDesc")}
                value={templateDesc}
                onChange={(e) => setTemplateDesc(e.target.value)}
                style={{ width: 180 }}
              />
              <button className="btn sm primary" disabled={saveTemplate.isPending} type="submit">
                {saveTemplate.isPending ? "…" : t("agent.config.saveBtn")}
              </button>
              <button className="btn sm" type="button" onClick={() => setSavingTemplate(false)}>
                {t("agent.config.cancel")}
              </button>
              {saveTemplate.isError && (
                <span className="danger-text text-xs">{String((saveTemplate.error as Error)?.message)}</span>
              )}
            </form>
          )}
          <a
            className="btn sm no-underline"
            href={`/api/v1/agents/${agentId}/export`}
            download={`${slug}-config.json`}
            title="Komplette Konfiguration (inkl. Stages, Guard-Rails, Egress, Secret-Namen) als JSON-Bundle herunterladen — Import auf der Agenten-Übersicht"
          >
            {t("agent.config.exportBundle")}
          </a>
          {canManage && (
            <>
              <button
                className="btn sm"
                type="button"
                disabled={importCfg.isPending}
                onClick={() => importRef.current?.click()}
                title="Ein JSON-Bundle einlesen und NUR dessen Config-Dateien in diesen Agenten übernehmen (überschreibt SOUL/HEARTBEAT/ACCESS/EGRESS/… als neue Version). Stammdaten, Secrets und Guard-Rails bleiben unangetastet."
              >
                {importCfg.isPending ? "…" : t("agent.config.importBundle")}
              </button>
              <input
                ref={importRef}
                type="file"
                accept="application/json,.json"
                style={{ display: "none" }}
                onChange={(e) => { pickBundle(e.target.files?.[0]); e.target.value = ""; }}
              />
            </>
          )}
        </div>
      )}
      {importError && <p className="danger-text text-xs mb-2" style={{ textAlign: "right" }}>{importError}</p>}
      <p className="muted text-xs mb-3" style={{ maxWidth: 680 }}>
        {t("agent.config.versionInfo", {
          version: cfg.data && cfg.data.version > 0
            ? t("agent.config.currentVersion", { v: cfg.data.version })
            : "",
        })}
      </p>
      {canManage && (
        <ConfigAssistant
          agentId={agentId}
          files={files}
          onApply={(file, content) => setDraft({ ...files, [file]: content })}
        />
      )}
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
            {t("agent.config.newVersion")}
          </button>
          {save.isError && <span className="danger-text text-xs">{(save.error as Error).message}</span>}
        </div>
      )}
    </div>
  );
}

// ConfigAssistant ist der KI-Assistent zum Anpassen von Agenten (FR-001).
// Er erscheint nur, wenn org-weit ein Claude-Credential hinterlegt ist. Seine
// Vorschläge werden in den Config-Draft übernommen (onApply) — wirksam werden
// sie erst durch bewusstes Speichern einer neuen Version.
type AssistTurn = { role: "user" | "assistant"; content: string; proposals?: AssistProposal[] };

function ConfigAssistant({
  agentId,
  files,
  onApply,
}: {
  agentId: string;
  files: Record<string, string>;
  onApply: (file: string, content: string) => void;
}) {
  const { t } = useTranslation();
  const status = useQuery({ queryKey: ["assist-status"], queryFn: assistStatus, retry: false });
  const [open, setOpen] = useState(false);
  const [input, setInput] = useState("");
  const [turns, setTurns] = useState<AssistTurn[]>([]);
  const [applied, setApplied] = useState<Set<string>>(new Set());

  const ask = useMutation({
    mutationFn: (history: AssistTurn[]) => {
      const msgs: AssistMessage[] = history.map((m) => ({ role: m.role, content: m.content }));
      return configAssist(agentId, msgs, files);
    },
    onSuccess: (res) =>
      setTurns((prev) => [...prev, { role: "assistant", content: res.reply, proposals: res.proposals }]),
  });

  if (!status.data?.available) return null; // Gating: kein Claude-Credential → keine UI.

  const send = () => {
    const text = input.trim();
    if (!text || ask.isPending) return;
    const next: AssistTurn[] = [...turns, { role: "user", content: text }];
    setTurns(next);
    setInput("");
    ask.mutate(next);
  };

  return (
    <div className="assist">
      <div className="assist-head">
        <button
          className="assist-toggle"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
        >
          <span className="caret">▶</span>
          {t("agent.config.assist.title")}
        </button>
        {open && turns.length > 0 && (
          <button className="btn sm" onClick={() => { setTurns([]); setApplied(new Set()); }}>
            {t("agent.config.assist.reset")}
          </button>
        )}
      </div>
      {open && (
        <div className="assist-body">
          <p className="muted text-xs mb-3" style={{ maxWidth: 620 }}>
            {t("agent.config.assist.hint")}
          </p>
          {turns.length > 0 && (
            <div className="assist-log mb-3">
              {turns.map((m, i) => (
                <div key={i} className={`assist-msg ${m.role === "user" ? "user" : "bot"}`}>
                  {m.role === "user" ? m.content : <Markdown text={m.content} />}
                  {m.proposals && m.proposals.length > 0 && (
                    <div className="assist-proposals">
                      {m.proposals.map((p) => {
                        const key = `${i}:${p.file}`;
                        const done = applied.has(key);
                        return (
                          <button
                            key={p.file}
                            className="btn sm primary"
                            disabled={done}
                            onClick={() => { onApply(p.file, p.content); setApplied((s) => new Set(s).add(key)); }}
                          >
                            {done
                              ? `${p.file} · ${t("agent.config.assist.applied")}`
                              : t("agent.config.assist.apply", { file: p.file })}
                          </button>
                        );
                      })}
                    </div>
                  )}
                </div>
              ))}
              {ask.isPending && <div className="assist-status">{t("agent.config.assist.thinking")}</div>}
              {ask.isError && (
                <div className="assist-status danger-text">{(ask.error as Error).message}</div>
              )}
            </div>
          )}
          <div className="assist-input">
            <textarea
              rows={2}
              placeholder={t("agent.config.assist.placeholder")}
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); send(); } }}
            />
            <button className="btn primary sm" disabled={!input.trim() || ask.isPending} onClick={send}>
              {t("agent.config.assist.send")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function effectiveEgress(
  status: EgressStatus | undefined,
  templates: EgressTemplate[],
  cfg: AgentEgressCfg | undefined,
): Map<string, string> {
  const assigned = new Set(cfg?.template_ids ?? []);
  const effective = new Map<string, string>();
  for (const d of status?.defaults ?? []) effective.set(d.pattern, "Basis");
  for (const p of status?.env ?? []) if (!effective.has(p)) effective.set(p, "ENV");
  for (const tpl of templates.filter((tpl) => assigned.has(tpl.id)))
    for (const h of tpl.hosts) if (!effective.has(h.pattern)) effective.set(h.pattern, tpl.name);
  for (const h of cfg?.hosts ?? []) if (!effective.has(h.pattern)) effective.set(h.pattern, "eigener Host");
  return effective;
}

function AgentSecrets({ agentId }: { agentId: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const own = useQuery({
    queryKey: ["agent-secrets", agentId],
    queryFn: () => api<SecretPreview[]>(`/agents/${agentId}/secrets`),
    retry: false,
  });
  const org = useQuery({ queryKey: ["secrets"], queryFn: () => api<SecretPreview[]>("/secrets"), retry: false });
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [sensitive, setSensitive] = useState(false);
  const [check, setCheck] = useState<({ key: string } & SecretCheck) | null>(null);
  const inval = () => qc.invalidateQueries({ queryKey: ["agent-secrets", agentId] });

  const save = useMutation({
    mutationFn: () =>
      put<{ ok: boolean; check: SecretCheck }>(
        `/agents/${agentId}/secrets/${encodeURIComponent(key)}`,
        { value, sensitive },
      ),
    onSuccess: (res) => {
      setCheck({ key, ...res.check });
      setKey("");
      setValue("");
      setSensitive(false);
      inval();
    },
  });
  const remove = useMutation({
    mutationFn: (k: string) => del(`/agents/${agentId}/secrets/${encodeURIComponent(k)}`),
    onSuccess: inval,
  });
  const protect = useMutation({
    mutationFn: (k: string) =>
      patch(`/agents/${agentId}/secrets/${encodeURIComponent(k)}`, { sensitive: true }),
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
  const inherited = (org.data ?? []).filter((s) => s.agent_ids.includes(agentId));
  const assignable = (org.data ?? []).filter((s) => !s.agent_ids.includes(agentId));

  return (
    <div>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        {t("agent.secrets.desc")}
      </p>

      <div className="card mb-4 flex gap-3 items-end flex-wrap">
        <div className="min-w-64">
          <label>{t("agent.secrets.assignOrg")}</label>
          <select
            value=""
            onChange={(e) => {
              if (e.target.value) assign.mutate(e.target.value);
            }}
            disabled={assign.isPending || assignable.length === 0}
          >
            <option value="">
              {assignable.length === 0 ? t("agent.secrets.noMoreOrg") : t("agent.secrets.selectSecret")}
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
          <label>{t("agent.secrets.assignedOrg")}</label>
          {inherited.map((s) => (
            <div key={s.key} className="card mb-2 flex items-center gap-4" style={{ padding: "11px 15px", opacity: ownKeys.has(s.key) ? 0.55 : 1 }}>
              <span className="mono text-sm flex-1">{s.key}</span>
              {ownKeys.has(s.key) && (
                <span className="muted text-xs">{t("agent.secrets.shadowed")}</span>
              )}
              <SecretValue secret={s} />
              <button className="btn sm" disabled={unassign.isPending} onClick={() => unassign.mutate(s.key)}>
                {t("agent.secrets.removeAssignment")}
              </button>
            </div>
          ))}
        </>
      )}
      {inherited.length === 0 && (
        <p className="muted mb-3" style={{ color: "var(--text-warning, #b45309)" }}>
          {t("agent.secrets.noAssigned")}
        </p>
      )}

      <label className="mt-4">{t("agent.secrets.ownSecrets")}</label>

      <form
        className="card mb-4 flex gap-3 items-end flex-wrap"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <div className="min-w-48">
          <label>{t("agent.secrets.key")}</label>
          <input value={key} onChange={(e) => setKey(e.target.value)} className="mono" placeholder="zammad_token" required />
        </div>
        <div className="flex-1 min-w-52">
          <label>{t("agent.secrets.value")}</label>
          <input type={sensitive ? "password" : "text"} value={value} onChange={(e) => setValue(e.target.value)} required />
        </div>
        <label className="flex items-center gap-2 text-xs" style={{ marginBottom: 7 }}>
          <input type="checkbox" checked={sensitive} onChange={(e) => setSensitive(e.target.checked)} />
          {t("agent.secrets.markSensitive")}
        </label>
        <button className="btn primary" disabled={save.isPending}>
          {t("agent.secrets.save")}
        </button>
        {check && (
          <p
            className="text-xs w-full m-0"
            style={{ color: check.checked && !check.valid ? "var(--danger, #b91c1c)" : check.valid ? "var(--success, #15803d)" : "var(--text-secondary)" }}
          >
            {check.checked && check.valid && t("agent.secrets.savedValid", { key: check.key })}
            {check.checked && !check.valid && t("agent.secrets.savedInvalid", { key: check.key, hint: check.hint })}
            {!check.checked && t("agent.secrets.savedOk", { key: check.key })}
          </p>
        )}
      </form>

      {(own.data ?? []).map((s) => (
        <div key={s.key} className="card mb-2 flex items-center gap-4" style={{ padding: "11px 15px" }}>
          <span className="mono text-sm flex-1">{s.key}</span>
          <span className="badge st-triage">{t("agent.secrets.agentOwn")}</span>
          {s.sensitive && (
            <span className="badge st-blocked" title={t("secrets.sensitiveHint")}>
              {t("secrets.sensitive")}
            </span>
          )}
          <SecretValue secret={s} />
          {!s.sensitive && (
            <button
              className="btn sm"
              disabled={protect.isPending}
              onClick={() => {
                if (confirm(t("secrets.protectConfirm", { key: s.key }))) protect.mutate(s.key);
              }}
            >
              {t("secrets.protect")}
            </button>
          )}
          <button className="btn sm" onClick={() => remove.mutate(s.key)}>
            {t("agent.secrets.delete")}
          </button>
        </div>
      ))}
      {own.data?.length === 0 && <p className="muted mb-3">{t("agent.secrets.noOwn")}</p>}
    </div>
  );
}

// Inline-Parser für einen Text-Abschnitt: löst [[Wikilinks]] in klickbare
// Links auf (rot & inert, wenn die Zielseite fehlt — Wiki-Konvention) und
// rendert **fett**. Dependency-frei, deshalb nur diese zwei Marker.
function wikiInline(
  text: string,
  key: string,
  has: (slug: string) => boolean,
  onNav: (slug: string) => void,
  missingLabel: string,
): ReactNode[] {
  const out: ReactNode[] = [];
  const re = /\[\[([^\]]+)\]\]|\*\*([^*]+)\*\*/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let i = 0;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) out.push(text.slice(last, m.index));
    if (m[1] != null) {
      const slug = m[1].trim();
      const ok = has(slug);
      out.push(
        <button
          key={`${key}-${i}`}
          type="button"
          className={`wikilink${ok ? "" : " missing"}`}
          onClick={ok ? () => onNav(slug) : undefined}
          title={ok ? undefined : missingLabel}
        >
          {slug}
        </button>,
      );
    } else if (m[2] != null) {
      out.push(<strong key={`${key}-${i}`}>{m[2]}</strong>);
    }
    last = re.lastIndex;
    i++;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

// Rendert einen Seiten-Body als leichtes Markdown (Überschriften #/##/###,
// Aufzählungen -/*, Absätze) mit klickbaren [[Wikilinks]] — eine echte
// Wiki-Seite statt Rohtext.
function WikiBody({ text, has, onNav }: { text: string; has: (slug: string) => boolean; onNav: (slug: string) => void }) {
  const { t } = useTranslation();
  const missing = t("agent.memory.missing");
  const blocks: ReactNode[] = [];
  let bullets: ReactNode[] = [];
  const flush = (k: string) => {
    if (bullets.length) {
      blocks.push(<ul key={`ul-${k}`}>{bullets}</ul>);
      bullets = [];
    }
  };
  text.split("\n").forEach((raw, idx) => {
    const line = raw.trimEnd();
    const li = /^\s*[-*]\s+(.*)$/.exec(line);
    if (li) {
      bullets.push(<li key={`li-${idx}`}>{wikiInline(li[1], `li${idx}`, has, onNav, missing)}</li>);
      return;
    }
    flush(String(idx));
    if (!line.trim()) return;
    const h = /^(#{1,3})\s+(.*)$/.exec(line);
    if (h) {
      blocks.push(
        <div key={idx} className={`wiki-h wiki-h${h[1].length}`}>
          {wikiInline(h[2], `h${idx}`, has, onNav, missing)}
        </div>,
      );
      return;
    }
    blocks.push(<p key={idx}>{wikiInline(line, `p${idx}`, has, onNav, missing)}</p>);
  });
  flush("end");
  return <div className="wiki-body voice text-[14.5px]">{blocks}</div>;
}

// Kurzvorschau für die Index-Liste: Markup entfernen, [[slug]] → slug.
function wikiPreview(text: string): string {
  return text
    .replace(/\[\[([^\]]+)\]\]/g, "$1")
    .replace(/[*#>`]/g, "")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 120);
}

// Reihenfolge der Seitentypen im Baum (spec/05). Leerer Typ kommt zuletzt: die
// nicht eingeordneten Seiten sind ein Rest, kein Anfang.
const WIKI_TYPES = ["kunde", "projekt", "system", "person", "problem", "thema", ""] as const;

// linkContext zieht den Satz heraus, in dem eine Seite auf eine andere verweist.
// Ein Backlink ohne diesen Satz zwingt zum Klicken, nur um zu sehen, warum.
function linkContext(body: string, slug: string): string {
  const re = new RegExp("[^.\\n]*\\[\\[" + slug.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "\\]\\][^.\\n]*");
  const m = re.exec(body);
  if (!m) return "";
  const s = m[0].replace(/\s+/g, " ").trim();
  return s.length > 150 ? s.slice(0, 149) + "…" : s;
}

type GraphNode = { page: MemoryEntry; x: number; y: number; vx: number; vy: number; r: number; deg: number };
type GraphEdge = { a: GraphNode; b: GraphNode };

// forceLayout ist eine kleine Kräfte-Simulation (Abstoßung zwischen allen
// Knoten, Federn entlang der Verweise, sanfte Mitte). Dependency-frei und in
// einem Rutsch gerechnet — bei ein paar hundert Seiten reicht das, und es
// erspart eine Graph-Bibliothek im Bundle.
function forceLayout(nodes: GraphNode[], edges: GraphEdge[], w: number, h: number, iters: number) {
  nodes.forEach((n, i) => {
    const a = (i / Math.max(nodes.length, 1)) * Math.PI * 2;
    n.x = w / 2 + Math.cos(a) * w * 0.3;
    n.y = h / 2 + Math.sin(a) * h * 0.3;
    n.vx = 0;
    n.vy = 0;
  });
  const k = Math.sqrt((w * h) / Math.max(nodes.length, 1)) * 0.62;
  for (let it = 0; it < iters; it++) {
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const a = nodes[i];
        const b = nodes[j];
        let dx = a.x - b.x;
        let dy = a.y - b.y;
        let d2 = dx * dx + dy * dy;
        if (d2 < 0.01) {
          dx = (i - j) * 0.1 + 0.05;
          dy = 0.05;
          d2 = 0.01;
        }
        const d = Math.sqrt(d2);
        const f = (k * k) / d2;
        a.vx += (dx / d) * f;
        a.vy += (dy / d) * f;
        b.vx -= (dx / d) * f;
        b.vy -= (dy / d) * f;
      }
    }
    edges.forEach((e) => {
      const dx = e.b.x - e.a.x;
      const dy = e.b.y - e.a.y;
      const d = Math.max(Math.sqrt(dx * dx + dy * dy), 0.01);
      const f = (d * d) / k / 14;
      e.a.vx += (dx / d) * f;
      e.a.vy += (dy / d) * f;
      e.b.vx -= (dx / d) * f;
      e.b.vy -= (dy / d) * f;
    });
    nodes.forEach((n) => {
      n.vx += (w / 2 - n.x) * 0.006;
      n.vy += (h / 2 - n.y) * 0.006;
      n.x += Math.max(-14, Math.min(14, n.vx));
      n.y += Math.max(-14, Math.min(14, n.vy));
      n.vx *= 0.82;
      n.vy *= 0.82;
      n.x = Math.max(18, Math.min(w - 18, n.x));
      n.y = Math.max(18, Math.min(h - 18, n.y));
    });
  }
}

function buildGraph(pages: MemoryEntry[]) {
  const nodes: GraphNode[] = pages.map((p) => ({ page: p, x: 0, y: 0, vx: 0, vy: 0, r: 4, deg: 0 }));
  const idx = new Map(nodes.map((n) => [n.page.slug, n]));
  const edges: GraphEdge[] = [];
  pages.forEach((p) => {
    const a = idx.get(p.slug)!;
    (p.links ?? []).forEach((l) => {
      const b = idx.get(l);
      if (b && b !== a) {
        edges.push({ a, b });
        a.deg++;
        b.deg++;
      }
    });
  });
  nodes.forEach((n) => (n.r = 4 + Math.min(n.deg, 8) * 1.5));
  return { nodes, edges };
}

// WikiGraph zeichnet die Verlinkung — die Struktur, die als Liste unsichtbar
// bleibt. Canvas statt SVG: bei mehreren hundert Knoten ist das der Unterschied
// zwischen flüssig und zäh.
function WikiGraph({
  pages,
  current,
  onOpen,
  height,
  labels = true,
}: {
  pages: MemoryEntry[];
  current?: string;
  onOpen?: (slug: string) => void;
  height: number;
  labels?: boolean;
}) {
  const { t } = useTranslation();
  const ref = useRef<HTMLCanvasElement | null>(null);
  const model = useRef<{ nodes: GraphNode[]; edges: GraphEdge[] } | null>(null);
  const [hover, setHover] = useState<GraphNode | null>(null);
  const [tip, setTip] = useState<{ x: number; y: number; text: string } | null>(null);

  const draw = useCallback(() => {
    const cv = ref.current;
    if (!cv || !cv.parentElement) return;
    const dpr = window.devicePixelRatio || 1;
    const w = cv.parentElement.clientWidth;
    if (w === 0) return;
    cv.style.height = height + "px";
    cv.width = w * dpr;
    cv.height = height * dpr;
    const c = cv.getContext("2d");
    if (!c) return;
    c.setTransform(dpr, 0, 0, dpr, 0, 0);
    if (!model.current) {
      model.current = buildGraph(pages);
      forceLayout(model.current.nodes, model.current.edges, w, height, 320);
    }
    const { nodes, edges } = model.current;
    const cs = getComputedStyle(document.documentElement);
    const cEdge = cs.getPropertyValue("--border-strong") || "#ccc";
    const cNode = cs.getPropertyValue("--text-secondary") || "#666";
    const cAcc = cs.getPropertyValue("--text-accent") || "#185fa5";
    const cMut = cs.getPropertyValue("--text-muted") || "#999";

    c.clearRect(0, 0, w, height);
    const near = new Set<GraphNode>();
    if (hover) {
      near.add(hover);
      edges.forEach((e) => {
        if (e.a === hover) near.add(e.b);
        if (e.b === hover) near.add(e.a);
      });
    }
    c.lineWidth = 1;
    edges.forEach((e) => {
      const hot = hover != null && (e.a === hover || e.b === hover);
      c.strokeStyle = hot ? cAcc : cEdge;
      c.globalAlpha = hover && !hot ? 0.25 : 1;
      c.beginPath();
      c.moveTo(e.a.x, e.a.y);
      c.lineTo(e.b.x, e.b.y);
      c.stroke();
    });
    c.globalAlpha = 1;
    nodes.forEach((n) => {
      const isSelf = n.page.slug === current;
      const isHub = n.deg >= 3;
      c.globalAlpha = hover && !near.has(n) ? 0.3 : 1;
      if (n.deg === 0) c.globalAlpha *= 0.5;
      c.fillStyle = isSelf || isHub ? cAcc : n.deg === 0 ? cMut : cNode;
      c.beginPath();
      c.arc(n.x, n.y, isSelf ? n.r + 2 : n.r, 0, 6.284);
      c.fill();
      if (labels && (isHub || isSelf || near.has(n))) {
        c.globalAlpha = 1;
        c.fillStyle = cNode;
        c.font = "11px " + (cs.getPropertyValue("--sans") || "sans-serif");
        c.textAlign = "center";
        const name = (n.page.title || n.page.slug).slice(0, 26);
        c.fillText(name, n.x, n.y - n.r - 5);
      }
    });
    c.globalAlpha = 1;
  }, [pages, current, hover, height, labels]);

  useEffect(() => {
    model.current = null;
    draw();
  }, [pages, height]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    draw();
  }, [draw]);
  useEffect(() => {
    const onResize = () => {
      model.current = null;
      draw();
    };
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, [draw]);

  const pick = (ev: React.MouseEvent<HTMLCanvasElement>): GraphNode | null => {
    const m = model.current;
    if (!m) return null;
    const r = ev.currentTarget.getBoundingClientRect();
    const x = ev.clientX - r.left;
    const y = ev.clientY - r.top;
    let best: GraphNode | null = null;
    let bd = Infinity;
    m.nodes.forEach((n) => {
      const d = (n.x - x) ** 2 + (n.y - y) ** 2;
      if (d < bd) {
        bd = d;
        best = n;
      }
    });
    return bd < 400 ? best : null;
  };

  if (pages.length === 0) {
    return <p className="muted p-4 text-[12.5px]">{t("agent.memory.graphEmpty")}</p>;
  }

  return (
    <div style={{ position: "relative" }}>
      <canvas
        ref={ref}
        style={{ display: "block", width: "100%", cursor: hover ? "pointer" : "default" }}
        onMouseMove={(ev) => {
          const n = pick(ev);
          setHover(n);
          if (n) {
            const r = ev.currentTarget.getBoundingClientRect();
            setTip({
              x: Math.min(ev.clientX - r.left + 12, r.width - 240),
              y: ev.clientY - r.top + 12,
              text: (n.page.title || n.page.slug) + " · " + t("agent.memory.refs", { count: n.deg }),
            });
          } else setTip(null);
        }}
        onMouseLeave={() => {
          setHover(null);
          setTip(null);
        }}
        onClick={(ev) => {
          const n = pick(ev);
          if (n && onOpen) onOpen(n.page.slug);
        }}
      />
      {tip && (
        <div className="wiki-tip" style={{ left: tip.x, top: tip.y }}>
          {tip.text}
        </div>
      )}
    </div>
  );
}

// Die Vorgangs-Präfixe, die die Control Plane in wiki_log.summary schreibt
// (internal/memory). Geschlossene Liste, exakt abgeglichen — eine Regel wie
// "alles bis zum ersten Doppelpunkt" schnitte mitten in Titel hinein, die
// selbst einen enthalten ("educa-ai-web !100 (#222): fertig, …").
const LOG_PREFIXES = ["neue Seite: ", "ergänzt: ", "gelöscht: ", "bearbeitet: "];

// logDetail entscheidet, ob die Zusammenfassung neben der Seite noch etwas
// beiträgt. Meist lautet sie "<Vorgang>: <Seitentitel>" — dann steht dasselbe
// zweimal in einer Zeile, und genau das machte das Protokoll unlesbar.
function logDetail(summary: string, pageName: string): string {
  let s = (summary ?? "").trim();
  for (const p of LOG_PREFIXES) {
    if (s.startsWith(p)) {
      s = s.slice(p.length).trim();
      break;
    }
  }
  const n = (pageName ?? "").trim();
  if (!s) return "";
  if (!n) return s;
  const a = s.toLowerCase();
  const b = n.toLowerCase();
  if (a === b || a.endsWith(b) || a.startsWith(b) || b.startsWith(a)) return "";
  return s;
}

function Memories({ agentId, canManage }: { agentId: string; canManage: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [view, setView] = useState<"pages" | "graph" | "log">("pages");
  // Offene Wiki-Seite lebt in der URL (?page=<slug>) — deep-linkbar, Browser-Zurück.
  const [sp, setSp] = useSearchParams();
  const selected = sp.get("page");
  const setSelected = (slug: string | null) =>
    setSp(
      (prev) => {
        const n = new URLSearchParams(prev);
        if (slug) n.set("page", slug);
        else n.delete("page");
        n.set("tab", "memory");
        return n;
      },
      { replace: false },
    );
  // Semantische Suche (spec/05, pgvector): Eingabe entprellt, dann Backend ?q=.
  const [query, setQuery] = useState("");
  const [debounced, setDebounced] = useState("");
  useEffect(() => {
    const h = setTimeout(() => setDebounced(query.trim()), 250);
    return () => clearTimeout(h);
  }, [query]);
  // Volle Seitenliste — trägt Link-Auflösung (has), Backlinks, Baum und Graph.
  const mems = useQuery({
    queryKey: ["memories", agentId],
    queryFn: () => api<MemoryEntry[] | null>(`/agents/${agentId}/memories`),
  });
  const search = useQuery({
    queryKey: ["memories-search", agentId, debounced],
    queryFn: () => api<MemoryEntry[] | null>(`/agents/${agentId}/memories?q=${encodeURIComponent(debounced)}`),
    enabled: debounced.length > 0,
  });
  const log = useQuery({
    queryKey: ["wiki-log", agentId],
    queryFn: () => api<WikiLogEntry[] | null>(`/agents/${agentId}/wiki/log`),
    enabled: view === "log",
  });
  // Qualitätsbefunde (spec/05): was am Wiki verwahrlost, soll man sehen, ohne
  // es selbst nachzuzählen.
  const health = useQuery({
    queryKey: ["wiki-health", agentId],
    queryFn: () => api<WikiHealth>(`/agents/${agentId}/wiki/health`),
  });

  const [draft, setDraft] = useState("");
  const [draftTitle, setDraftTitle] = useState("");
  const [draftType, setDraftType] = useState("");
  const [editId, setEditId] = useState<string | null>(null);
  const [editTitle, setEditTitle] = useState("");
  const [editText, setEditText] = useState("");
  const [note, setNote] = useState("");
  const [filter, setFilter] = useState<WikiFinding["kind"] | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["memories", agentId] });
    qc.invalidateQueries({ queryKey: ["wiki-log", agentId] });
    qc.invalidateQueries({ queryKey: ["wiki-health", agentId] });
  };

  const add = useMutation({
    mutationFn: () => post(`/agents/${agentId}/memories`, { content: draft, title: draftTitle, type: draftType }),
    onSuccess: () => {
      setDraft("");
      setDraftTitle("");
      setDraftType("");
      invalidate();
    },
  });
  const save = useMutation({
    mutationFn: () => patch(`/memories/${editId}`, { content: editText, title: editTitle }),
    onSuccess: () => {
      setEditId(null);
      invalidate();
    },
  });
  const remove = useMutation({
    mutationFn: (id: string) => del(`/memories/${id}`),
    onSuccess: invalidate,
  });
  const consolidate = useMutation({
    mutationFn: () => post<{ merged: number }>(`/agents/${agentId}/wiki/consolidate`),
    onSuccess: (r) => {
      setNote(r.merged > 0 ? t("agent.memory.consolidateDone", { count: r.merged }) : t("agent.memory.consolidateNone"));
      invalidate();
    },
  });

  const list = useMemo(() => mems.data ?? [], [mems.data]);
  const logs = log.data ?? [];
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  const opLabel = (op: string) =>
    ({
      ingest: t("agent.memory.opIngest"),
      write: t("agent.memory.opWrite"),
      append: t("agent.memory.opWrite"),
      merge: t("agent.memory.opMerge"),
      delete: t("agent.memory.opDelete"),
    })[op] ?? op;

  const bySlug = useMemo(() => new Map(list.map((p) => [p.slug, p])), [list]);

  // Protokoll nach Tagen gruppieren: 25 Zeitstempel untereinander liest niemand,
  // drei Tagesblöcke mit Uhrzeiten schon.
  const logDays = useMemo(() => {
    const today = new Date().toDateString();
    const yest = new Date(Date.now() - 86400000).toDateString();
    const out: { key: string; label: string; rows: WikiLogEntry[] }[] = [];
    logs.forEach((l) => {
      const d = new Date(l.created_at);
      const key = d.toDateString();
      let group = out.find((g) => g.key === key);
      if (!group) {
        const label =
          key === today
            ? t("agent.memory.today")
            : key === yest
              ? t("agent.memory.yesterday")
              : d.toLocaleDateString(locale, { day: "2-digit", month: "long", year: "numeric" });
        group = { key, label, rows: [] };
        out.push(group);
      }
      group.rows.push(l);
    });
    return out;
  }, [logs, locale, t]);
  const has = (slug: string) => bySlug.has(slug);
  const openPage = (slug: string) => {
    setView("pages");
    setEditId(null);
    setSelected(slug);
  };
  const current = selected ? (bySlug.get(selected) ?? null) : null;
  const backlinks = useMemo(
    () => (current ? list.filter((p) => p.id !== current.id && (p.links ?? []).includes(current.slug)) : []),
    [current, list],
  );
  const searching = debounced.length > 0;

  // Nachbarschaft der offenen Seite (sie selbst, ihre Ziele, ihre Rückverweise).
  // Memoisiert, weil der Graph bei neuer Array-Identität sein Layout verwirft
  // und die Simulation neu rechnet.
  const localPages = useMemo(() => {
    if (!current) return [];
    const names = new Set<string>([current.slug]);
    (current.links ?? []).forEach((l) => bySlug.has(l) && names.add(l));
    backlinks.forEach((b) => names.add(b.slug));
    return list.filter((p) => names.has(p.slug));
  }, [current, backlinks, list, bySlug]);

  // Verwaist = kein lebender Verweis hinein oder hinaus. Wird im Baum gedämpft
  // dargestellt; die Zahl steht in der Qualitätsleiste.
  const orphanSlugs = useMemo(() => {
    const inbound = new Set<string>();
    list.forEach((p) => (p.links ?? []).forEach((l) => bySlug.has(l) && inbound.add(l)));
    return new Set(
      list.filter((p) => !inbound.has(p.slug) && !(p.links ?? []).some((l) => bySlug.has(l))).map((p) => p.slug),
    );
  }, [list, bySlug]);

  // Auf einen Befund gefilterte Seitenmenge.
  const filtered = useMemo(() => {
    if (!filter) return null;
    const slugs = new Set((health.data?.findings ?? []).filter((f) => f.kind === filter).map((f) => f.slug));
    return slugs;
  }, [filter, health.data]);

  const typeLabel = (ty: string) =>
    ty === ""
      ? t("agent.memory.typeNone")
      : (
          {
            kunde: t("agent.memory.typeKunde"),
            projekt: t("agent.memory.typeProjekt"),
            system: t("agent.memory.typeSystem"),
            person: t("agent.memory.typePerson"),
            problem: t("agent.memory.typeProblem"),
            thema: t("agent.memory.typeThema"),
          } as Record<string, string>
        )[ty] ?? ty;

  const visible = useMemo(
    () => list.filter((p) => !filtered || filtered.has(p.slug)),
    [list, filtered],
  );

  // ── Baum: erste Ebene ist der Seitentyp, darunter die Seiten; eine Seite
  // lässt sich aufklappen und zeigt dann, worauf sie verweist. ────────────────
  const treeRow = (p: MemoryEntry, child: boolean) => {
    const kids = (p.links ?? []).filter((l) => bySlug.has(l));
    const isOpen = expanded.has(p.slug);
    return (
      <div key={p.slug + (child ? "-c" : "")}>
        <div className={`wiki-node${p.slug === selected ? " sel" : ""}${orphanSlugs.has(p.slug) ? " orphan" : ""}`}>
          <button
            type="button"
            className="tw"
            disabled={kids.length === 0 || child}
            aria-label={p.title || p.slug}
            onClick={() =>
              setExpanded((prev) => {
                const n = new Set(prev);
                if (n.has(p.slug)) n.delete(p.slug);
                else n.add(p.slug);
                return n;
              })
            }
          >
            {kids.length > 0 && !child ? (isOpen ? "▾" : "▸") : "·"}
          </button>
          <button type="button" className="lbl" title={wikiPreview(p.content)} onClick={() => setSelected(p.slug)}>
            {p.title || p.slug}
          </button>
          {kids.length > 0 && <span className="cnt">{kids.length}</span>}
        </div>
        {isOpen && !child && <div className="wiki-kids">{kids.map((l) => treeRow(bySlug.get(l)!, true))}</div>}
      </div>
    );
  };

  const tree = WIKI_TYPES.map((ty) => {
    const items = visible.filter((p) => (p.type ?? "") === ty);
    if (items.length === 0) return null;
    return (
      <div className="wiki-group" key={ty || "none"}>
        <div className="wiki-group-h">
          <WikiTypeIcon type={ty} size={14} />
          <span>{typeLabel(ty)}</span>
          <span className="cnt">{items.length}</span>
        </div>
        {items.map((p) => treeRow(p, false))}
      </div>
    );
  });

  // ── Qualitätsleiste ────────────────────────────────────────────────────────
  const h = health.data;
  type QualityItem = { kind: WikiFinding["kind"]; n: number; label: string; help: string };
  const quality: QualityItem[] = h
    ? ([
        { kind: "orphan", n: h.orphans, label: t("agent.memory.qOrphans", { count: h.orphans }), help: t("agent.memory.qOrphansHelp") },
        { kind: "dead_link", n: h.dead_links, label: t("agent.memory.qDeadLinks", { count: h.dead_links }), help: t("agent.memory.qDeadLinksHelp") },
        { kind: "untyped", n: h.untyped, label: t("agent.memory.qUntyped", { count: h.untyped }), help: t("agent.memory.qUntypedHelp") },
        { kind: "episodic", n: h.episodic, label: t("agent.memory.qEpisodic", { count: h.episodic }), help: t("agent.memory.qEpisodicHelp") },
        { kind: "duplicate", n: h.duplicate, label: t("agent.memory.qDuplicate", { count: h.duplicate }), help: t("agent.memory.qDuplicateHelp") },
        { kind: "stub", n: h.stubs, label: t("agent.memory.qStubs", { count: h.stubs }), help: t("agent.memory.qStubsHelp") },
      ] as QualityItem[]).filter((q) => q.n > 0)
    : [];

  return (
    <div>
      <p className="muted text-[12.5px] mb-3">{t("agent.memory.hint")}</p>
      <div className="flex items-center gap-2 mb-3">
        <div className="seg" role="tablist">
          <button className={view === "pages" ? "active" : ""} onClick={() => setView("pages")}>
            {t("agent.memory.pages")}
            {list.length > 0 && ` (${list.length})`}
          </button>
          <button className={view === "graph" ? "active" : ""} onClick={() => setView("graph")}>
            {t("agent.memory.graph")}
          </button>
          <button className={view === "log" ? "active" : ""} onClick={() => setView("log")}>
            {t("agent.memory.log")}
          </button>
        </div>
        <span className="flex-1" />
        {canManage && (
          <button className="btn sm" disabled={consolidate.isPending || list.length < 2} onClick={() => consolidate.mutate()}>
            {consolidate.isPending ? t("agent.memory.consolidating") : t("agent.memory.consolidate")}
          </button>
        )}
      </div>

      {/* Qualitätsbefunde: Zahlen, die zugleich Filter sind. */}
      {h && list.length > 0 && (
        <div className="wiki-quality mb-3">
          <span className="muted text-[11px] uppercase tracking-wide">{t("agent.memory.quality")}</span>
          {quality.length === 0 ? (
            <span className="chip ok">{t("agent.memory.qClean")}</span>
          ) : (
            quality.map((q) => (
              <button
                key={q.kind}
                type="button"
                title={q.help}
                className={`chip q${filter === q.kind ? " on" : ""}`}
                onClick={() => {
                  setFilter(filter === q.kind ? null : q.kind);
                  setView("pages");
                }}
              >
                {q.label}
              </button>
            ))
          )}
          {filter && (
            <button type="button" className="btn sm" onClick={() => setFilter(null)}>
              {t("agent.memory.filterClear")}
            </button>
          )}
        </div>
      )}
      {note && <p className="muted text-xs mb-2">{note}</p>}

      {view === "log" ? (
        <>
          {logs.length === 0 && <p className="muted">{t("agent.memory.logEmpty")}</p>}
          {logDays.map((day) => (
            <div key={day.key} className="wiki-log-day">
              <div className="wiki-log-day-h">{day.label}</div>
              <div className="card" style={{ padding: "2px 14px" }}>
                {day.rows.map((l) => {
                  // Seiten beim Namen nennen, wo es sie noch gibt — der rohe Slug
                  // ist bis zu 64 Zeichen lang und sagt weniger als der Titel.
                  const page = l.page_slug ? bySlug.get(l.page_slug) : undefined;
                  const name = page?.title || page?.slug || l.page_slug || "";
                  const extra = logDetail(l.summary, name);
                  // Gibt es die Seite nicht mehr, ist ihr Slug kryptisch und bis
                  // 64 Zeichen lang; dann trägt der Satz aus dem Protokoll mehr.
                  const primary = page ? name : extra || name;
                  const detail = page ? extra : "";
                  return (
                    <div key={l.id} className="wiki-log-row">
                      <span className="at">
                        {new Date(l.created_at).toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" })}
                      </span>
                      <span className={`op op-${l.op}`}>
                        <WikiOpIcon op={l.op} />
                        {opLabel(l.op)}
                      </span>
                      <span className="cell">
                        {page ? (
                          <button type="button" className="wikilink" onClick={() => openPage(page.slug)} title={page.slug}>
                            {name}
                          </button>
                        ) : (
                          <span className="wikilink missing" title={l.page_slug}>
                            {primary}
                          </span>
                        )}
                        {detail && (
                          <span className="detail" title={l.summary}>
                            {" · "}
                            {detail}
                          </span>
                        )}
                      </span>
                    </div>
                  );
                })}
              </div>
            </div>
          ))}
        </>
      ) : view === "graph" ? (
        <div className="card" style={{ padding: 0, overflow: "hidden" }}>
          <WikiGraph pages={visible} current={selected ?? undefined} onOpen={openPage} height={520} />
          <div className="wiki-legend">
            <span className="key">
              <span className="dot hub" /> {t("agent.memory.graphHub")}
            </span>
            <span className="key">
              <span className="dot" /> {t("agent.memory.graphLinked")}
            </span>
            <span className="key">
              <span className="dot orph" /> {t("agent.memory.graphOrphanKey")}
            </span>
            <span className="flex-1" />
            <span className="muted">{t("agent.memory.graphHint")}</span>
          </div>
        </div>
      ) : (
        // ── Arbeitsfläche: Baum | Seite | Kontext ──────────────────────────────
        <div className="wiki-panes">
          <div className="card wiki-pane" style={{ padding: "10px 12px" }}>
            <div className="wiki-search mb-2">
              <input type="search" placeholder={t("agent.memory.searchPlaceholder")} value={query} onChange={(e) => setQuery(e.target.value)} />
            </div>
            <div className="wiki-tree">
              {searching ? (
                (search.data ?? []).length === 0 && !search.isFetching ? (
                  <p className="muted text-[12.5px]">{t("agent.memory.searchEmpty")}</p>
                ) : (
                  (search.data ?? []).map((m) => (
                    <div key={m.id} className={`wiki-node${m.slug === selected ? " sel" : ""}`}>
                      <span className="tw">
                        <WikiTypeIcon type={m.type} size={13} />
                      </span>
                      <button type="button" className="lbl" onClick={() => setSelected(m.slug)}>
                        {m.title || m.slug}
                      </button>
                      {typeof m.score === "number" && <span className="cnt">{Math.round(m.score * 100)}%</span>}
                    </div>
                  ))
                )
              ) : list.length === 0 ? (
                <p className="muted text-[12.5px]">{t("agent.memory.nothingLearned")}</p>
              ) : (
                tree
              )}
            </div>
          </div>

          <div className="card wiki-pane" style={{ padding: 0 }}>
            {!current ? (
              <p className="muted text-[12.5px]" style={{ padding: "14px 16px" }}>
                {t("agent.memory.preview")}
              </p>
            ) : editId === current.id ? (
              <div style={{ padding: "12px 14px" }}>
                <input className="mb-2" value={editTitle} onChange={(e) => setEditTitle(e.target.value)} placeholder={t("agent.memory.titlePlaceholder")} />
                <textarea rows={10} value={editText} onChange={(e) => setEditText(e.target.value)} />
                <div className="flex items-center gap-2 mt-2">
                  <button className="btn primary sm" disabled={!editText.trim() || save.isPending} onClick={() => save.mutate()}>
                    {t("agent.memory.save")}
                  </button>
                  <button className="btn sm" onClick={() => setEditId(null)}>
                    {t("agent.memory.cancel")}
                  </button>
                  {save.isError && <span className="danger-text text-xs">{(save.error as Error).message}</span>}
                </div>
              </div>
            ) : (
              <div style={{ padding: "14px 18px 18px" }}>
                <div className="flex items-start justify-between gap-3 mb-1">
                  <span className="flex items-center gap-2 min-w-0 flex-wrap">
                    <span className="font-medium text-[16px]">{current.title || current.slug}</span>
                    <span className={`chip${current.type ? " is-fixed" : " q"}`} style={{ fontSize: "10px" }}>
                      <WikiTypeIcon type={current.type} size={12} />
                      {current.type ? typeLabel(current.type) : t("agent.memory.noType")}
                    </span>
                    {current.source && (
                      <span className="chip is-fixed" style={{ fontSize: "10px" }}>
                        {current.source === "manual" ? t("agent.memory.sourceManual") : t("agent.memory.sourceAgent")}
                      </span>
                    )}
                  </span>
                  <span className="muted text-[11px] shrink-0 flex items-center gap-2">
                    {new Date(current.updated_at || current.created_at).toLocaleDateString(locale)}
                    {canManage && (
                      <>
                        <button
                          className="btn sm"
                          onClick={() => {
                            setEditId(current.id);
                            setEditTitle(current.title ?? "");
                            setEditText(current.content);
                          }}
                        >
                          {t("agent.memory.change")}
                        </button>
                        <button
                          className="btn sm danger"
                          disabled={remove.isPending}
                          onClick={() => {
                            remove.mutate(current.id);
                            setSelected(null);
                          }}
                        >
                          {t("agent.memory.forget")}
                        </button>
                      </>
                    )}
                  </span>
                </div>
                <div className="slug mb-3">{current.slug}</div>
                <WikiBody text={current.content} has={has} onNav={openPage} />
              </div>
            )}
          </div>

          <div className="card wiki-pane" style={{ padding: "10px 12px" }}>
            {!current ? (
              canManage && (
                <>
                  <div className="muted text-[11px] mb-1.5">{t("agent.memory.remember")}</div>
                  <input className="mb-2" placeholder={t("agent.memory.titlePlaceholder")} value={draftTitle} onChange={(e) => setDraftTitle(e.target.value)} />
                  <select className="mb-2" value={draftType} onChange={(e) => setDraftType(e.target.value)}>
                    <option value="">{t("agent.memory.noType")}</option>
                    {WIKI_TYPES.filter((x) => x !== "").map((ty) => (
                      <option key={ty} value={ty}>
                        {typeLabel(ty)}
                      </option>
                    ))}
                  </select>
                  <textarea rows={4} placeholder={t("agent.memory.addPlaceholder")} value={draft} onChange={(e) => setDraft(e.target.value)} />
                  <button className="btn primary sm mt-2" disabled={!draft.trim() || add.isPending} onClick={() => add.mutate()}>
                    {t("agent.memory.remember")}
                  </button>
                  {add.isError && <span className="danger-text text-xs">{(add.error as Error).message}</span>}
                </>
              )
            ) : (
              <>
                <div className="muted text-[11px] mb-1.5">
                  {t("agent.memory.backlinks")} ({backlinks.length})
                </div>
                {backlinks.length === 0 ? (
                  <p className="muted text-[12.5px] mb-3">{t("agent.memory.noBacklinks")}</p>
                ) : (
                  <div className="mb-3">
                    {backlinks.map((b) => {
                      const ctx = linkContext(b.content, current.slug);
                      return (
                        <div key={b.id} className="wiki-ctx">
                          <button type="button" className="wikilink text-[12.5px]" onClick={() => openPage(b.slug)}>
                            {b.title || b.slug}
                          </button>
                          {ctx && <p className="wiki-quote">„{ctx}“</p>}
                        </div>
                      );
                    })}
                  </div>
                )}

                <div className="muted text-[11px] mb-1.5">
                  {t("agent.memory.outgoing")} ({(current.links ?? []).length})
                </div>
                <div className="flex flex-wrap gap-x-3 gap-y-1 mb-3">
                  {(current.links ?? []).length === 0 && <span className="muted text-[12.5px]">—</span>}
                  {(current.links ?? []).map((l) =>
                    has(l) ? (
                      <button key={l} type="button" className="wikilink text-[12.5px]" onClick={() => openPage(l)}>
                        {bySlug.get(l)?.title || l}
                      </button>
                    ) : (
                      <span key={l} className="wikilink missing text-[12.5px]" title={t("agent.memory.missing")}>
                        {l}
                      </span>
                    ),
                  )}
                </div>

                <div className="muted text-[11px] mb-1">{t("agent.memory.localGraph")}</div>
                {localPages.length < 2 ? (
                  <p className="muted text-[12.5px]">{t("agent.memory.graphOrphan")}</p>
                ) : (
                  <WikiGraph pages={localPages} current={current.slug} onOpen={openPage} height={140} labels={false} />
                )}
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function AgentEgress({ agentId, canEdit }: { agentId: string; canEdit: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();

  const status = useQuery({ queryKey: ["egress", "status"], queryFn: () => api<EgressStatus>("/egress") });
  const templates = useQuery({ queryKey: ["egress", "templates"], queryFn: () => api<EgressTemplate[]>("/egress/templates") });
  const cfg = useQuery({ queryKey: ["egress", "agent", agentId], queryFn: () => api<AgentEgressCfg>(`/agents/${agentId}/egress`) });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["egress", "agent", agentId] });
    qc.invalidateQueries({ queryKey: ["config", agentId] });
  };

  const toggleTpl = useMutation({
    mutationFn: ({ tid, on }: { tid: string; on: boolean }) =>
      on ? put(`/agents/${agentId}/egress/templates/${tid}`, {}) : del(`/agents/${agentId}/egress/templates/${tid}`),
    onSuccess: invalidate,
  });
  const delHost = useMutation({ mutationFn: (id: string) => del(`/agents/${agentId}/egress/hosts/${id}`), onSuccess: invalidate });

  const assigned = new Set(cfg.data?.template_ids ?? []);
  const effective = effectiveEgress(status.data, templates.data ?? [], cfg.data);

  return (
    <div style={{ maxWidth: 780 }}>
      <p className="muted text-xs mb-4">
        {t("agent.egress.desc")}
      </p>

      <div className="card mb-5" style={{ padding: "13px 15px" }}>
        <p className="text-xs font-medium mb-2">{t("agent.egress.effectiveAllowlist")}</p>
        <div className="flex flex-wrap gap-1">
          {[...effective.entries()].map(([pattern, source]) => (
            <span key={pattern} className={`chip${source === "ENV" ? " is-fixed" : ""}`}>
              {pattern}
              <span className="src">{source}</span>
            </span>
          ))}
          {effective.size === 0 && <span className="muted text-xs">{t("agent.egress.allBlocked")}</span>}
        </div>
      </div>

      <p className="text-xs font-medium mb-2">{t("agent.egress.templates")}</p>
      <div className="grid gap-2 mb-5" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(240px, 1fr))" }}>
        {(templates.data ?? []).map((tpl) => (
          <label
            key={tpl.id}
            className="card flex items-start gap-2"
            style={{ padding: "10px 12px", margin: 0, cursor: canEdit ? "pointer" : "default", opacity: assigned.has(tpl.id) ? 1 : 0.75 }}
          >
            <input
              type="checkbox"
              style={{ width: "auto", marginTop: 2 }}
              checked={assigned.has(tpl.id)}
              disabled={!canEdit || toggleTpl.isPending}
              onChange={(e) => toggleTpl.mutate({ tid: tpl.id, on: e.target.checked })}
            />
            <span style={{ minWidth: 0 }}>
              <Link
                to={`/egress/templates/${tpl.id}`}
                className="block text-sm font-medium"
                style={{ color: "var(--text-primary)", textDecoration: "none" }}
                title="Template-Detailseite öffnen"
                onClick={(e) => e.stopPropagation()}
              >
                {tpl.name}
              </Link>
              <span className="block mono text-[11px] muted" style={{ overflowWrap: "anywhere" }}>
                {tpl.hosts.length === 0 ? t("agent.egress.none") : tpl.hosts.map((h) => h.pattern).join(", ")}
              </span>
            </span>
          </label>
        ))}
        {(templates.data ?? []).length === 0 && (
          <span className="muted text-xs">
            {t("agent.egress.noTemplates")}
          </span>
        )}
      </div>

      <p className="text-xs font-medium mb-2">{t("agent.egress.ownHosts")}</p>
      <div className="flex flex-wrap gap-1 mb-2">
        <HostChips
          hosts={cfg.data?.hosts ?? []}
          canEdit={canEdit}
          onDelete={(id) => delHost.mutate(id)}
          emptyText={t("agent.egress.none")}
        />
      </div>
      {canEdit && (
        <div className="mb-5" style={{ maxWidth: 560 }}>
          <AddHostForm onAdd={(pattern, note) => post(`/agents/${agentId}/egress/hosts`, { pattern, note }).then(invalidate)} />
        </div>
      )}

      <p className="text-xs font-medium mt-5 mb-2">{t("agent.egress.lastDecisions")}</p>
      <EgressLogTable agentId={agentId} />
    </div>
  );
}

function AgentTools({ agentId, canEdit }: { agentId: string; canEdit: boolean }) {
  const { t } = useTranslation();
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<TargetPlugin[] | null>("/targets"),
  });
  const mcp = (targets.data ?? []).filter((tgt) => tgt.kind === "mcp");

  return (
    <div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        {t("agent.tools.desc")}
      </p>
      {mcp.length === 0 && (
        <p className="muted">
          {t("agent.tools.noMcp")}
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
  const { t } = useTranslation();
  const qc = useQueryClient();
  const tools: MCPTool[] = plugin.manifest?.tools ?? [];
  const assigned = useQuery({
    queryKey: ["agent-tools", agentId, plugin.name],
    queryFn: () => api<string[]>(`/agents/${agentId}/tools/${plugin.name}`),
  });

  const [restrict, setRestrict] = useState<boolean | null>(null);
  const [sel, setSel] = useState<Set<string>>(new Set());

  const loaded = assigned.data;
  const effectiveRestrict = restrict ?? (loaded ? loaded.length > 0 : false);
  const effectiveSel = restrict === null && loaded ? new Set(loaded) : sel;

  const save = useMutation({
    mutationFn: (toolList: string[]) => put(`/agents/${agentId}/tools/${plugin.name}`, { tools: toolList }),
    onSuccess: () => {
      setRestrict(null);
      qc.invalidateQueries({ queryKey: ["agent-tools", agentId, plugin.name] });
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
    if (r && effectiveSel.size === 0) setSel(new Set(tools.map((tl) => tl.name)));
    else setSel(new Set(effectiveSel));
  };

  const dirty = restrict !== null;

  return (
    <div className="card" style={{ padding: "14px 16px", opacity: plugin.enabled ? 1 : 0.6 }}>
      <div className="flex items-center gap-2 mb-2">
        <TargetIcon name={plugin.name} kind={plugin.kind} category={plugin.category} size={16} />
        <span className="font-medium">{plugin.label || plugin.name}</span>
        <span className="mono text-xs muted">{plugin.name}</span>
        {!plugin.enabled && <span className="text-[11px] ml-auto" style={{ color: "var(--clay)" }}>{t("agent.tools.deactivated")}</span>}
      </div>
      {tools.length === 0 ? (
        <p className="muted text-xs">{t("agent.tools.noTools")}</p>
      ) : (
        <>
          <div className="flex gap-3 text-xs mb-2">
            <label className="flex items-center gap-1">
              <input type="radio" checked={!effectiveRestrict} disabled={!canEdit} onChange={() => setMode(false)} />
              {t("agent.tools.allTools")}
            </label>
            <label className="flex items-center gap-1">
              <input type="radio" checked={effectiveRestrict} disabled={!canEdit} onChange={() => setMode(true)} />
              {t("agent.tools.selectedOnly")}
            </label>
          </div>
          <ul className="text-xs" style={{ listStyle: "none", padding: 0, margin: 0 }}>
            {tools.map((tl) => (
              <li key={tl.name} className="mb-1">
                <label className="flex items-start gap-2" style={{ opacity: effectiveRestrict ? 1 : 0.5 }}>
                  <input
                    type="checkbox"
                    style={{ marginTop: 2 }}
                    disabled={!canEdit || !effectiveRestrict}
                    checked={effectiveRestrict ? effectiveSel.has(tl.name) : true}
                    onChange={() => toggleSel(tl.name)}
                  />
                  <span>
                    <span className="mono">{tl.name}</span>
                    {tl.description && <span className="muted"> — {tl.description.split("\n")[0]}</span>}
                  </span>
                </label>
              </li>
            ))}
          </ul>
          {canEdit && (
            <div className="flex items-center gap-2 mt-3">
              <span className="text-[11px] muted">
                {effectiveRestrict
                  ? t("agent.tools.allowedOf", { sel: effectiveSel.size, total: tools.length })
                  : t("agent.tools.allAllowed")}
              </span>
              <button
                className="btn sm primary ml-auto"
                disabled={!dirty || save.isPending || (effectiveRestrict && effectiveSel.size === 0)}
                onClick={() => save.mutate(effectiveRestrict ? [...effectiveSel] : [])}
              >
                {t("agent.tools.save")}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

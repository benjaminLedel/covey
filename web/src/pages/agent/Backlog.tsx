import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import i18n from "../../i18n";
import {
  api,
  post,
  patch,
  del,
  type Stage,
  type Task,
  type TaskNote,
} from "../../api";

export function Backlog({
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

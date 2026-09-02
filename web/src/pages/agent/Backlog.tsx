import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import i18n from "../../i18n";
import {
  api,
  post,
  patch,
  del,
  buildInfo,
  type AgentPhase,
  type Stage,
  type Task,
  type TaskNote,
} from "../../api";
import { fmtUSD } from "../../format";
import { upstreamIssueURL } from "../../upstream";
import { PhaseBadge } from "../../components/PhaseBadge";

export function Backlog({
  agentId,
  phase,
  canManage,
  onShowRecording,
}: {
  agentId: string;
  // Woran die Plattform gerade für diesen Agenten arbeitet. Es hängt am
  // Agenten, gezeigt wird es an der Aufgabe, die läuft: wer auf die Aufgabe
  // sieht, wartet auf genau diesen Vorgang.
  phase?: AgentPhase;
  canManage: boolean;
  onShowRecording: (taskId: string, title: string) => void;
}) {
  const { t } = useTranslation();
  const [showArchive, setShowArchive] = useState(false);
  const tasks = useQuery({
    queryKey: ["backlog", agentId, showArchive],
    queryFn: () => api<Task[]>(`/agents/${agentId}/backlog${showArchive ? "?archived=1" : ""}`),
  });
  const invalBacklog = useInvalidateBacklog(agentId);
  // Search (debounced, then the backend's ?q=). It replaces the board with a
  // flat list of hits: a search runs across the columns and across the archive,
  // and both of those are exactly what the board does not show.
  const [query, setQuery] = useState("");
  const [debounced, setDebounced] = useState("");
  useEffect(() => {
    const h = setTimeout(() => setDebounced(query.trim()), 250);
    return () => clearTimeout(h);
  }, [query]);
  const searching = debounced.length > 0;
  const search = useQuery({
    queryKey: ["backlog-search", agentId, debounced],
    queryFn: () => api<Task[]>(`/agents/${agentId}/backlog?q=${encodeURIComponent(debounced)}`),
    enabled: searching,
  });
  const hits = search.data ?? [];
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
      invalBacklog();
    },
  });

  const stages = useQuery({
    queryKey: ["stages", agentId],
    queryFn: () => api<Stage[]>(`/agents/${agentId}/stages`),
  });
  const move = useMutation({
    mutationFn: ({ taskId, stageId }: { taskId: string; stageId: string | null }) =>
      post(`/tasks/${taskId}/stage`, { stage_id: stageId }),
    onSuccess: invalBacklog,
  });
  const [dragTask, setDragTask] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  // Columns show the newest COLUMN_LIMIT cards; whoever wants the older ones
  // unfolds the column. Per column, because one deep column should not push
  // every other one long.
  const [unfolded, setUnfolded] = useState<Set<string>>(new Set());
  const toggleUnfold = (key: string) =>
    setUnfolded((prev) => {
      const next = new Set(prev);
      if (!next.delete(key)) next.add(key);
      return next;
    });

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
            <input
              type="search"
              className="bl-search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("agent.backlog.searchPlaceholder")}
              title={t("agent.backlog.searchHint")}
            />
            <button
              className="btn sm"
              style={{ marginLeft: "auto" }}
              disabled={searching}
              title={searching ? t("agent.backlog.searchHint") : undefined}
              onClick={() => setShowArchive((v) => !v)}
            >
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

          {!searching && (
            <p className="muted text-xs mb-3">
              {stageList.length === 0
                ? t("agent.backlog.noStages")
                : canManage
                  ? t("agent.backlog.stagesInfo")
                  : t("agent.backlog.stagesReadonly")}
            </p>
          )}

          {editing && canManage && <StageEditor agentId={agentId} stages={stageList} />}

          {searching ? (
            <SearchResults
              agentId={agentId}
              canManage={canManage}
              query={debounced}
              hits={hits}
              pending={search.isPending}
              stageName={(tk) => stageList.find((st) => st.id === tk.stage_id)?.name}
              onShowRecording={onShowRecording}
            />
          ) : columns.length === 0 ? (
            <p className="muted">{t("agent.backlog.empty")}</p>
          ) : (
            <div className="kanban" style={{ ["--kcols" as string]: columns.length }}>
              {columns.map((col) => {
                const all = (tasks.data ?? [])
                  .filter((tk) => inStage(tk, col.id))
                  .sort((a, b) => b.created_at.localeCompare(a.created_at));
                const key = col.id ?? "__none";
                const open = unfolded.has(key);
                const items = open ? all : all.slice(0, COLUMN_LIMIT);
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
                        phase={phase}
                        canManage={canManage}
                        onShowRecording={onShowRecording}
                        onDragStart={() => setDragTask(tk.id)}
                        onDragEnd={() => setDragTask(null)}
                      />
                    ))}
                    {all.length === 0 && <div className="kc-empty">—</div>}
                    {hidden > 0 && (
                      <button className="btn sm kc-more" onClick={() => toggleUnfold(key)}>
                        {t("agent.backlog.showOlder", { count: hidden })}
                      </button>
                    )}
                    {open && all.length > COLUMN_LIMIT && (
                      <button className="btn sm kc-more" onClick={() => toggleUnfold(key)}>
                        {t("agent.backlog.showFewer")}
                      </button>
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

// useInvalidateBacklog refreshes both views of the backlog: the board and the
// search. Whatever a card does — archive, discard, reschedule — has to arrive in
// the list the user is currently looking at, and during a search that is the
// list of hits.
function useInvalidateBacklog(agentId: string) {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ["backlog", agentId] });
    qc.invalidateQueries({ queryKey: ["backlog-search", agentId] });
  };
}

// SearchResults shows the hits as one flat list rather than as a board. A hit
// may come from any column and from the archive, so the column it sits in is
// written onto the card — otherwise the found task would have lost its place.
function SearchResults({
  agentId,
  canManage,
  query,
  hits,
  pending,
  stageName,
  onShowRecording,
}: {
  agentId: string;
  canManage: boolean;
  query: string;
  hits: Task[];
  pending: boolean;
  stageName: (task: Task) => string | undefined;
  onShowRecording: (taskId: string, title: string) => void;
}) {
  const { t } = useTranslation();
  if (pending) return <p className="muted">{t("agent.backlog.searching")}</p>;
  if (hits.length === 0) return <p className="muted">{t("agent.backlog.searchEmpty", { q: query })}</p>;
  return (
    <div className="bl-results">
      <div className="muted text-xs mb-2">
        {t("agent.backlog.searchResults", { count: hits.length })}
        {hits.length >= SEARCH_LIMIT ? ` · ${t("agent.backlog.searchCapped", { n: SEARCH_LIMIT })}` : ""}
      </div>
      {hits.map((tk) => (
        <TaskCard
          key={tk.id}
          task={tk}
          agentId={agentId}
          canManage={canManage}
          stageName={stageName(tk)}
          onShowRecording={onShowRecording}
        />
      ))}
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

// SEARCH_LIMIT mirrors backlog.SearchMaxResults — only so that the list can say
// when it is showing the cap rather than the whole truth.
const SEARCH_LIMIT = 50;

function TaskCard({
  task,
  agentId,
  phase,
  canManage,
  stageName,
  onShowRecording,
  onDragStart,
  onDragEnd,
}: {
  task: Task;
  agentId: string;
  phase?: AgentPhase;
  canManage: boolean;
  stageName?: string;
  onShowRecording: (taskId: string, title: string) => void;
  onDragStart?: () => void;
  onDragEnd?: () => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const invalBacklog = useInvalidateBacklog(agentId);
  const notes = useQuery({
    queryKey: ["task-notes", task.id],
    queryFn: () => api<TaskNote[]>(`/tasks/${task.id}/notes`),
    enabled: open,
  });
  // Wohin ein Plattform-Befund gemeldet wird: immer in das Projekt, aus dem
  // dieses Binary stammt (buildinfo.SourceRepo). NICHT in das Repository, das
  // die Organisation für covey Doctor eingetragen hat — das ist die Adresse,
  // an der sie ihre eigenen Vorgänge führt, und ein Fehler der Plattform
  // gehört dorthin, wo die Plattform gepflegt wird. Ein Fork trägt seine
  // eigene Adresse in der Konstante und zeigt damit auf seinen Tracker.
  const build = useQuery({
    queryKey: ["build-info"],
    queryFn: buildInfo,
    enabled: open,
    staleTime: 60 * 60 * 1000,
  });
  const repo = { system: build.data?.source_system, project: build.data?.source_project };
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
  // Draggable only where there is somewhere to drop it: the search list has no
  // columns, and a grab cursor that leads nowhere is a lie.
  const draggable = canManage && !archived && !!onDragStart;
  const subtitle =
    task.state === "blocked"
      ? task.correlation_key
        ? t("agent.backlog.waitingOn", { key: task.correlation_key })
        : t("agent.backlog.blocked")
      : relTime(task.updated_at);
  return (
    <div
      className={`kc${open ? " expanded" : ""}${draggable ? " draggable" : ""}`}
      draggable={draggable}
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onClick={() => setOpen((v) => !v)}
      style={archived ? { opacity: 0.55 } : undefined}
    >
      <div className="kc-title">
        <span className={`badge state st-${task.state} kc-state`}>{t(`status.${task.state}`, task.state)}</span>
        <span className="font-medium min-w-0 truncate">{task.title}</span>
        {archived && <span className="muted text-[11px] shrink-0">{t("agent.backlog.archived")}</span>}
        <span className="kc-prio">P{task.priority}</span>
      </div>
      {/* Läuft die Aufgabe und tut die Plattform gerade etwas dafür, steht es
          hier: „in Arbeit" allein erklärt keine Viertelstunde, in der das
          Image geholt wird. */}
      {phase && task.state === "in_progress" && (
        <div className="kc-phase">
          <PhaseBadge phase={phase} compact />
        </div>
      )}
      <div className="t">
        {subtitle} · {originLabel(task.origin)}
        {stageName && <> · {stageName}</>}
        {task.cost_usd !== undefined && (
          <>
            {" · "}
            <span title={t("agent.backlog.costHint", { n: task.cost_entries ?? 0 })}>
              {fmtUSD(task.cost_usd)}
            </span>
          </>
        )}
      </div>
      {open && (
        <div className="kc-detail fade">
          {task.body && (
            <pre className="voice whitespace-pre-wrap m-0 mb-2 secondary">{task.body}</pre>
          )}
          {task.correlation_key && (
            <p>
              <span className="muted">{t("agent.backlog.waitingOn", { key: "" }).replace(" ", "")}:</span>{" "}
              <span className="mono">{task.correlation_key}</span>
            </p>
          )}
          {task.cost_usd !== undefined && (
            <p>
              <span className="muted">{t("agent.backlog.cost")}:</span>{" "}
              <span className="mono">{fmtUSD(task.cost_usd)}</span>{" "}
              <span className="muted">{t("agent.backlog.costEntries", { n: task.cost_entries ?? 0 })}</span>
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
              {notes.data!.map((n) => {
                // Der vorbefüllte Link, nicht der Versand: der Mensch sieht das
                // Formular, liest gegen und meldet unter seinem Namen. Ohne
                // eingerichtetes Ziel steht hier nichts.
                const href = upstreamIssueURL({
                  repo,
                  title: task.title,
                  body: t("agent.backlog.reportBody", {
                    note: n.content,
                    agent: n.author,
                    task: task.title,
                    version: build.data ? `${build.data.version} (${build.data.commit})` : "?",
                  }),
                  truncationNote: "\n\n" + t("agent.backlog.reportTruncated"),
                });
                return (
                  <div key={n.id} className="text-xs mb-1" style={{ borderLeft: "2px solid var(--border)", paddingLeft: 8 }}>
                    <span className="secondary">{n.content}</span>{" "}
                    <span className="muted">· {relTime(n.created_at)}</span>
                    {href && (
                      <>
                        {" · "}
                        <a
                          href={href}
                          target="_blank"
                          rel="noopener noreferrer"
                          title={t("agent.backlog.reportUpstreamHint", {
                            repo: `${repo.system}:${repo.project}`,
                          })}
                          onClick={(e) => e.stopPropagation()}
                        >
                          {t("agent.backlog.reportUpstream")}
                        </a>
                      </>
                    )}
                  </div>
                );
              })}
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

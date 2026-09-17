import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { api, post, type Agent, type OrgChart, type Task, type TaskNote } from "../../api";
import i18n from "../../i18n";

/* The job posting: a form that ends in a task.
 *
 * Instead of four questions that assume you know the platform, a single
 * one — what should the new colleague do? — plus the two details a
 * person can answer with certainty. The answer goes as an assignment to the
 * HR department.
 *
 * Afterwards this view does not show "creating …", but the ongoing
 * interview: state, notes, and the follow-up question when the
 * posting was too thin. That is exactly why it is a task and not a synchronous
 * call — questions, answers and resuming already have a
 * home here. spec/20. */

type BriefResult = { task: Task; agent: Agent; waiting_for_hire: boolean };
type BriefStatus = { task: Task; notes: TaskNote[] | null; drafts: Agent[] };

export function Brief({ onBack, onOpen }: { onBack: () => void; onOpen: (a: Agent) => void }) {
  const { t } = useTranslation();
  const [description, setDescription] = useState("");
  const [department, setDepartment] = useState("");
  const [supervisor, setSupervisor] = useState("");
  const [started, setStarted] = useState<BriefResult | null>(null);

  const org = useQuery({ queryKey: ["org-chart"], queryFn: () => api<OrgChart>("/org/chart") });

  const send = useMutation({
    mutationFn: () =>
      post<BriefResult>(`/hiring/brief?lang=${encodeURIComponent(i18n.language)}`, {
        description,
        department,
        supervisor,
      }),
    onSuccess: setStarted,
  });

  if (started) return <BriefProgress result={started} onOpen={onOpen} />;

  return (
    <div>
      <p className="muted text-xs mb-3">{t("brief.lead")}</p>
      <form
        onSubmit={e => { e.preventDefault(); send.mutate(); }}
        style={{ display: "grid", gap: 12 }}
      >
        <div>
          <label>{t("brief.what")}</label>
          <textarea
            rows={6}
            value={description}
            autoFocus
            placeholder={t("brief.placeholder")}
            onChange={e => setDescription(e.target.value)}
          />
          <div className="muted text-xs" style={{ marginTop: 3 }}>{t("brief.whatHint")}</div>
        </div>
        <div className="flex gap-3 flex-wrap">
          <div style={{ flex: "1 1 180px" }}>
            <label>{t("dashboard.guided.departmentLabel")}</label>
            <select value={department} onChange={e => setDepartment(e.target.value)}>
              <option value="">—</option>
              {(org.data?.departments ?? []).map(d => (
                <option key={d.id} value={d.name}>{d.name}</option>
              ))}
            </select>
          </div>
          <div style={{ flex: "1 1 180px" }}>
            <label>{t("dashboard.guided.supervisorLabel")}</label>
            <select value={supervisor} onChange={e => setSupervisor(e.target.value)}>
              <option value="">—</option>
              {(org.data?.humans ?? []).map(h => (
                <option key={h.id} value={h.email}>{h.display_name}</option>
              ))}
            </select>
          </div>
        </div>
        {send.isError && (
          <div className="danger-text text-xs">
            {String((send.error as Error)?.message ?? send.error)}
            <div className="muted" style={{ marginTop: 4 }}>{t("brief.fallback")}</div>
          </div>
        )}
        <div className="flex gap-2 justify-end">
          <button type="button" className="btn" onClick={onBack}>{t("dashboard.back")}</button>
          <button className="btn primary" disabled={!description.trim() || send.isPending}>
            {send.isPending ? t("brief.sending") : t("brief.send")}
          </button>
        </div>
      </form>
    </div>
  );
}

/* The interview while it is running. Polled instead of streamed: the
   view is open as long as someone is watching, and a poll every three seconds
   is cheaper than a second streaming seam for a dialog that takes
   minutes. */
function BriefProgress({ result, onOpen }: { result: BriefResult; onOpen: (a: Agent) => void }) {
  const { t } = useTranslation();
  const status = useQuery({
    queryKey: ["brief", result.task.id],
    queryFn: () => api<BriefStatus>(`/hiring/brief/${result.task.id}`),
    refetchInterval: q => {
      // If the task waits for the hire, nothing more happens here:
      // a draft is not dispatched, and what changes it is a person at another
      // spot in the interface. Looking again every three seconds
      // would be a poll that by construction never finds anything.
      if (result.waiting_for_hire) return false;
      const s = (q.state.data as BriefStatus | undefined)?.task.state;
      return s === "done" || s === "failed" || s === "cancelled" ? false : 3000;
    },
  });

  const task = status.data?.task ?? result.task;
  const drafts = status.data?.drafts ?? [];
  const notes = status.data?.notes ?? [];

  return (
    <div>
      {result.waiting_for_hire && (
        <div className="card mb-3" style={{ borderStyle: "dashed" }}>
          <p className="text-xs">{t("brief.waitingForHire", { name: result.agent.display_name })}</p>
          <Link className="btn sm primary" style={{ marginTop: 8 }} to={`/agents/${result.agent.id}`}>
            {t("brief.openPeople")}
          </Link>
        </div>
      )}

      <div className="flex items-baseline gap-2 mb-2">
        <span className={`badge state st-${task.state}`}>{t(`status.${task.state}`, task.state)}</span>
        <span className="text-sm">{task.title}</span>
      </div>

      {task.state === "blocked" && (
        <div className="card mb-3">
          <div className="text-xs" style={{ fontWeight: 600, marginBottom: 4 }}>{t("brief.question")}</div>
          <p className="text-sm">{task.result || t("brief.questionFallback")}</p>
          <Link className="btn sm" style={{ marginTop: 8 }} to={`/agents/${result.agent.id}?tab=backlog`}>
            {t("brief.answer")}
          </Link>
        </div>
      )}

      {notes.length > 0 && (
        <ul className="brief-notes">
          {notes.map(n => (
            <li key={n.id}>
              <span className="muted text-xs">{n.author}</span> {n.content}
            </li>
          ))}
        </ul>
      )}

      {drafts.length > 0 && (
        <div style={{ marginTop: 14 }}>
          <div className="text-xs" style={{ fontWeight: 600, marginBottom: 6 }}>
            {t("brief.drafts", { count: drafts.length })}
          </div>
          <div style={{ display: "grid", gap: 8 }}>
            {drafts.map(d => (
              <button key={d.id} className="btn" style={{ justifyContent: "flex-start" }} onClick={() => onOpen(d)}>
                {d.display_name}
                <span className="muted text-xs mono" style={{ marginLeft: 8 }}>{d.slug}</span>
              </button>
            ))}
          </div>
          <p className="muted text-xs" style={{ marginTop: 8 }}>{t("brief.draftsHint")}</p>
        </div>
      )}

      {task.state === "done" && drafts.length === 0 && (
        <p className="muted text-xs">{t("brief.noDrafts")}</p>
      )}
      {task.state === "failed" && (
        <p className="danger-text text-xs">{task.error || t("brief.failed")}</p>
      )}
      {task.state !== "done" && task.state !== "failed" && drafts.length === 0 && (
        <p className="muted text-xs">{t("brief.running")}</p>
      )}
    </div>
  );
}

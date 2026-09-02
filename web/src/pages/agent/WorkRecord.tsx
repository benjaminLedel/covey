import { useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, type AgentReview, type WorkRecord as Record } from "../../api";
import { fmtUSD } from "../../format";
import { Markdown } from "../../components/Markdown";

/* Die Arbeitsakte (spec/21).

   Acht Abschnitte aus acht benannten Quellen — nichts davon ist Freitext eines
   Agenten oder eines Zielsystems, mit einer Ausnahme: den Aufgabentiteln. Die
   kommen häufig aus der Weck-Quelle und können einen Ticket-Betreff tragen.
   Ohne sie ist die Akte nicht lesbar, deshalb stehen sie hier — benannt statt
   später entdeckt.

   Der Zweck ist eine Frage: WARUM liefert dieser Kollege nicht. Drei Ursachen
   kommen in Betracht — seine Konfiguration, sein Auftrag, oder die Plattform
   unter ihm —, und die Abschnitte sind so sortiert, dass man sie in dieser
   Reihenfolge ausschließen kann. */

const PERIODS = [7, 30, 90] as const;

export function WorkRecord({ agentId }: { agentId: string }) {
  const { t } = useTranslation();
  const [days, setDays] = useState<number>(30);
  const rec = useQuery({
    queryKey: ["work-record", agentId, days],
    queryFn: () => api<Record>(`/agents/${agentId}/work-record?days=${days}`),
    retry: false,
  });

  if (rec.isError) return <p className="danger-text">{(rec.error as Error).message}</p>;
  const r = rec.data;
  if (!r) return <p className="muted">…</p>;

  const gesamt = r.throughput.by_state.reduce((n, c) => n + c.count, 0);

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-3 flex-wrap">
        <p className="muted text-xs" style={{ maxWidth: 620 }}>{t("agent.record.hint")}</p>
        <div className="flex gap-1 ml-auto">
          {PERIODS.map((d) => (
            <button
              key={d}
              className={`btn sm ${d === days ? "primary" : ""}`}
              onClick={() => setDays(d)}
            >
              {t("agent.record.days", { count: d })}
            </button>
          ))}
        </div>
      </div>

      <Reviews agentId={agentId} />

      <div className="rec-grid">
        <Section title={t("agent.record.throughput")} source="backlog_tasks">
          <CountRow label={t("agent.record.tasksTotal")} value={String(gesamt)} />
          {r.throughput.by_state.map((c) => (
            <CountRow key={c.key} label={t(`status.${c.key}`, c.key)} value={String(c.count)} indent />
          ))}
          {r.throughput.by_origin.length > 0 && (
            <div className="mt-2">
              <div className="muted text-xs mb-1">{t("agent.record.byOrigin")}</div>
              {r.throughput.by_origin.map((c) => (
                <CountRow key={c.key} label={c.key || "—"} value={String(c.count)} indent />
              ))}
            </div>
          )}
        </Section>

        <Section title={t("agent.record.aborts")} source={t("agent.record.srcLifecycle")}>
          {r.aborts.length === 0 && <p className="muted text-xs">{t("agent.record.noAborts")}</p>}
          {r.aborts.map((c) => (
            <CountRow key={c.key} label={t(`agent.record.abort.${c.key}`, c.key)} value={String(c.count)} />
          ))}
        </Section>

        <Section title={t("agent.record.cost")} source="cost_entries">
          <CountRow label={t("agent.record.total")} value={fmtUSD(r.cost.total_usd)} />
          <CountRow label={t("agent.record.perTask")} value={fmtUSD(r.cost.per_task_usd)} indent />
          <CountRow label={t("agent.record.costTasks")} value={String(r.cost.tasks)} indent />
        </Section>

        <Section title={t("agent.record.work")} source="recording_events">
          {r.work.length === 0 && <p className="muted text-xs">{t("agent.record.noWork")}</p>}
          {r.work.map((a) => (
            <div key={a.action} className="rec-row">
              <span className="mono text-xs flex-1 min-w-0 truncate">{a.action}</span>
              <span className="text-xs" style={{ color: "var(--text-success)" }}>{a.ok}</span>
              {a.failed > 0 && <span className="text-xs danger-text">{a.failed}</span>}
            </div>
          ))}
        </Section>

        <Section title={t("agent.record.indicators")} source="KPIS.md">
          {r.indicators.length === 0 && <p className="muted text-xs">{t("agent.record.noIndicators")}</p>}
          {r.indicators.map((i) => (
            <div key={i.key} className="rec-row">
              <span className="text-xs flex-1 min-w-0 truncate">{i.title || i.key}</span>
              <span className="text-xs">
                {i.count}
                {i.goal > 0 && <span className="muted"> / {i.goal}</span>}
              </span>
              {i.unit_usd != null && <span className="muted text-xs">{fmtUSD(i.unit_usd)}</span>}
            </div>
          ))}
        </Section>

        <Section title={t("agent.record.friction")} source="approvals, guardrails">
          <FrictionList label={t("agent.record.approvals")} rows={r.friction.approvals} />
          <FrictionList label={t("agent.record.denied")} rows={r.friction.denied} />
          <FrictionList label={t("agent.record.ownProposals")} rows={r.friction.proposals} />
          {r.friction.approvals.length + r.friction.denied.length + r.friction.proposals.length === 0 && (
            <p className="muted text-xs">{t("agent.record.noFriction")}</p>
          )}
        </Section>
      </div>

      {r.stuck.length > 0 && (
        <div className="card mt-3">
          <div className="text-sm mb-1" style={{ fontWeight: 600 }}>
            {t("agent.record.stuck", { count: r.stuck.length })}
          </div>
          <p className="muted text-xs mb-2" style={{ maxWidth: 620 }}>{t("agent.record.stuckHint")}</p>
          {r.stuck.map((s) => (
            <div key={s.id} className="rec-row">
              <span className="text-xs flex-1 min-w-0 truncate">{s.title}</span>
              <span className="muted mono text-xs">{s.correlation_key}</span>
              <span className="muted text-xs">{new Date(s.blocked_since).toLocaleDateString()}</span>
            </div>
          ))}
        </div>
      )}

      {r.findings.length > 0 && (
        <div className="card mt-3">
          <div className="text-sm mb-2" style={{ fontWeight: 600 }}>
            {t("agent.record.findings", { count: r.findings.length })}
          </div>
          {r.findings.map((f, i) => (
            <div key={i} className="text-xs mb-1">
              <span className="mono muted">{f.rule}</span> {f.message}
            </div>
          ))}
        </div>
      )}

      <h3 className="text-sm secondary mt-5 mb-2">{t("agent.record.tasks")}</h3>
      {r.notes?.map((n, i) => (
        <p key={i} className="muted text-xs mb-2">{n}</p>
      ))}
      <div className="card" style={{ padding: "6px 0" }}>
        {r.throughput.tasks.length === 0 && (
          <p className="muted text-xs" style={{ padding: "6px 15px" }}>{t("agent.record.noTasks")}</p>
        )}
        {r.throughput.tasks.map((task) => (
          <div key={task.id} className="rec-row" style={{ padding: "5px 15px" }}>
            <span className={`badge state st-${task.state}`}>{t(`status.${task.state}`, task.state)}</span>
            <span className="text-xs flex-1 min-w-0 truncate">{task.title}</span>
            <span className="muted text-xs mono">{task.origin.split(":")[0]}</span>
            {task.cost_usd > 0 && <span className="muted text-xs">{fmtUSD(task.cost_usd)}</span>}
            <span className="muted text-xs">{new Date(task.created_at).toLocaleDateString()}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

// Section trägt neben dem Titel die QUELLE. Das ist keine Zierde: eine Zahl,
// von der man nicht weiß, woher sie kommt, wird geglaubt oder ignoriert — und
// beides ist falsch.
function Section({ title, source, children }: { title: string; source: string; children: React.ReactNode }) {
  return (
    <div className="card">
      <div className="flex items-baseline gap-2 mb-2">
        <span className="text-sm" style={{ fontWeight: 600 }}>{title}</span>
        <span className="muted mono text-xs ml-auto">{source}</span>
      </div>
      {children}
    </div>
  );
}

function CountRow({ label, value, indent }: { label: string; value: string; indent?: boolean }) {
  return (
    <div className="rec-row" style={indent ? { paddingLeft: 10 } : undefined}>
      <span className={`text-xs flex-1 min-w-0 truncate ${indent ? "muted" : ""}`}>{label}</span>
      <span className="text-xs">{value}</span>
    </div>
  );
}

function FrictionList({ label, rows }: { label: string; rows: { key: string; count: number }[] }) {
  const { t } = useTranslation();
  if (rows.length === 0) return null;
  return (
    <div className="mb-2">
      <div className="muted text-xs mb-1">{label}</div>
      {rows.map((c) => (
        <CountRow key={c.key} label={t(`status.${c.key}`, c.key)} value={String(c.count)} indent />
      ))}
    </div>
  );
}

/* Die Beurteilungen, neueste zuerst — die Seite, die jemand öffnet, wenn er
   wissen will, was mit einem Agenten los ist. Über den Zahlen und nicht
   darunter: die Zahlen sind der Beleg, der Text ist die Aussage.

   Der beurteilte Agent sieht das hier nie. Das ist keine Einstellung, sondern
   eine Folge: sein Prompt trägt nur die aktive Config-Version, sein Gedächtnis
   ist auf ihn selbst gescopet, und es gibt keine Aktion, die Reviews liest. */
function Reviews({ agentId }: { agentId: string }) {
  const { t, i18n } = useTranslation();
  const [alle, setAlle] = useState(false);
  const q = useQuery({
    queryKey: ["agent-reviews", agentId],
    queryFn: () => api<AgentReview[] | null>(`/agents/${agentId}/reviews`),
    retry: false,
  });
  const list = q.data ?? [];
  if (list.length === 0) return null;
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  const gezeigt = alle ? list : list.slice(0, 1);

  return (
    <div className="mb-3">
      <h3 className="text-sm secondary mb-2">{t("agent.record.reviews")}</h3>
      {gezeigt.map((rev) => (
        <div key={rev.id} className="card mb-2">
          <div className="flex items-baseline gap-2 mb-2 flex-wrap">
            <strong className="text-sm">
              {new Date(rev.created_at).toLocaleDateString(locale)}
            </strong>
            <span className="muted text-xs">
              {t("agent.record.reviewPeriod", {
                from: new Date(rev.period_from).toLocaleDateString(locale),
                to: new Date(rev.period_to).toLocaleDateString(locale),
              })}
            </span>
            {rev.task_id && (
              <Link className="muted text-xs ml-auto" to={`/agents/${agentId}?tab=recording`}>
                {t("agent.record.fromTask")}
              </Link>
            )}
          </div>
          <div className="text-sm" style={{ maxWidth: 780 }}>
            <Markdown text={rev.summary} />
          </div>
        </div>
      ))}
      {list.length > 1 && (
        <button className="btn sm" onClick={() => setAlle((v) => !v)}>
          {alle ? t("agent.record.reviewsFewer") : t("agent.record.reviewsMore", { count: list.length - 1 })}
        </button>
      )}
    </div>
  );
}

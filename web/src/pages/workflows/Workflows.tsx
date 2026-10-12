/* The organisation's workflows in the preview (#560). Example data only:
   one of them opens, the others show what else a workflow could carry.

   The page reads from the top: how things stand across all workflows, what
   waits on the person looking, then the workflows themselves, filed by the
   department they serve. What the three effects mean is there for whoever
   asks, folded away. */
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import "./workflows.css";
import { BlockIcon } from "./canvas";
import { example, type Mode, type WorkflowRow } from "./model";

type ModeFilter = "all" | Mode;

export default function Workflows() {
  const { t, i18n } = useTranslation();
  const ex = useMemo(() => example(i18n.language), [i18n.language]);
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [modeFilter, setModeFilter] = useState<ModeFilter>("all");
  const [only, setOnly] = useState<string | null>(null);

  const open = (w: WorkflowRow, run?: string) => {
    if (w.id !== ex.workflows[0].id) return setOnly(w.id);
    navigate(`/workflows/${w.id}${run ? `?run=${encodeURIComponent(run)}` : ""}`);
  };

  const live = ex.workflows.filter((w) => w.mode !== "off");
  const sum = (f: (w: WorkflowRow) => number) => live.reduce((n, w) => n + f(w), 0);
  const q = query.trim().toLowerCase();
  const shown = ex.workflows
    .filter((w) => modeFilter === "all" || w.mode === modeFilter)
    .filter((w) => !q || [w.name, w.desc, w.scope, ...w.areas, ...w.chain].some((x) => x.toLowerCase().includes(q)))
    // What needs someone first; switched-off workflows last.
    .sort((a, b) => Number(a.mode === "off") - Number(b.mode === "off") || b.stuck - a.stuck || b.active - a.active);

  return (
    <div className="wf-overview">
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">{t("workflows.title")}</h1>
        <span className="wf-pill mute">{t("workflows.orgWide")}</span>
        <span className="wf-pill mute">{t("workflows.preview")}</span>
        <button className="btn primary sm ml-auto">{t("workflows.overview.new")}</button>
      </div>
      <p className="muted mb-4" style={{ maxWidth: 680 }}>
        {t("workflows.lead")}
      </p>

      <div className="wf-kpis">
        <div className="wf-kpi">
          <div className="v">{sum((w) => w.active)}</div>
          <div className="l">{t("workflows.overview.kpiRunning")}</div>
        </div>
        <div className="wf-kpi">
          <div className="v">{sum((w) => w.queued)}</div>
          <div className="l">{t("workflows.overview.kpiQueued")}</div>
        </div>
        <div className="wf-kpi">
          <div className="v danger">{sum((w) => w.stuck)}</div>
          <div className="l">{t("workflows.overview.kpiStuck")}</div>
        </div>
        <div className="wf-kpi">
          <div className="v">
            {live.length}
            <span className="of"> / {ex.workflows.length}</span>
          </div>
          <div className="l">{t("workflows.overview.kpiActive")}</div>
        </div>
      </div>

      {ex.waiting.length > 0 && (
        <section className="wf-section">
          <h2 className="wf-section-title">
            {t("workflows.overview.needsYou")} <span className="wf-count-plain">{ex.waiting.length}</span>
          </h2>
          <div className="wf-waiting">
            {ex.waiting.map((x) => {
              const w = ex.workflows.find((y) => y.id === x.workflow)!;
              return (
                <button key={x.no} className="wf-case" onClick={() => open(w, x.no)}>
                  <BlockIcon type={x.kind === "approval" ? "approval" : "agent"} small />
                  <span className="wf-case-text">
                    {x.title}
                    <small>
                      {w.name} · {x.no} · {t(`workflows.overview.kind_${x.kind}`)}
                      {only === w.id ? ` · ${t("workflows.onlyFirst")}` : ""}
                    </small>
                  </span>
                  <span className="wf-since">{x.since}</span>
                </button>
              );
            })}
          </div>
        </section>
      )}

      <div className="wf-toolbar">
        <input className="input wf-search" value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t("workflows.overview.search")} />
        <div className="wf-filters">
          {(["all", "steer", "watch", "off"] as ModeFilter[]).map((m) => (
            <button key={m} className={modeFilter === m ? "on" : ""} onClick={() => setModeFilter(m)}>
              {m === "all" ? t("workflows.runs.filter_all") : t(`workflows.mode.${m}`)}{" "}
              <span>{m === "all" ? ex.workflows.length : ex.workflows.filter((w) => w.mode === m).length}</span>
            </button>
          ))}
        </div>
      </div>

      <div className="wf-table-wrap">
        <table className="wf-list-table">
          <thead>
            <tr>
              <th>{t("workflows.col.name")}</th>
              <th>{t("workflows.col.stations")}</th>
              <th>{t("workflows.col.mode")}</th>
              <th className="num">{t("workflows.col.cases")}</th>
              <th className="num">{t("workflows.overview.colQueued")}</th>
              <th className="num">{t("workflows.col.stuck")}</th>
              <th>{t("workflows.owner")}</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((w) => (
              <tr key={w.id} tabIndex={0} className={w.mode === "off" ? "off" : ""} onClick={() => open(w)} onKeyDown={(e) => e.key === "Enter" && open(w)}>
                <td>
                  <div className="wf-name">{w.name}</div>
                  <div className="wf-desc">{only === w.id ? t("workflows.onlyFirst") : `${w.areas.join(" · ")} · ${w.scope}`}</div>
                </td>
                <td className="wf-stations">{w.chain.join(" → ")}</td>
                <td>
                  <span className={`wf-mode-dot m-${w.mode}`}>{t(`workflows.mode.${w.mode}`)}</span>
                </td>
                <td className="num">{w.mode === "off" ? "–" : w.active}</td>
                <td className="num">{w.mode === "off" || !w.queued ? "–" : w.queued}</td>
                <td className={`num${w.stuck && w.mode !== "off" ? " danger" : ""}`}>{w.mode === "off" || !w.stuck ? "–" : w.stuck}</td>
                <td>
                  {w.owner}
                  <div className="wf-desc">{w.updated}</div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {shown.length === 0 && <p className="wf-muted">{t("workflows.overview.none")}</p>}

      <details className="wf-explain">
        <summary>{t("workflows.overview.explain")}</summary>
        <div className="wf-modes">
          {(["off", "watch", "steer"] as Mode[]).map((m) => (
            <div key={m} className="wf-mode-card">
              <span className={`wf-mode-dot m-${m}`}>{t(`workflows.mode.${m}`)}</span>
              <p>{t(`workflows.modeText.${m}`)}</p>
            </div>
          ))}
        </div>
        <div className="wf-callout">
          <BlockIcon type="agent" />
          <p>
            <b>{t("workflows.without.title")}</b> {t("workflows.without.text")}
          </p>
        </div>
      </details>
    </div>
  );
}

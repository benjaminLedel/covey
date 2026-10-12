/* The organisation's workflows (#560, #565).

   The page reads from the top: how things stand across all workflows, what
   waits on the person looking, then the workflows themselves, sorted by what
   needs someone. What the three modes mean is there for whoever asks,
   folded away. */
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import "./workflows.css";
import { listWorkflows, myWorkflowApprovals, saveWorkflow, type Principal, type Workflow } from "../../api";
import { fmtDelta } from "../../format";
import { canManage } from "../agent/roles";
import { BlockIcon } from "./canvas";
import { example, toSource, type Mode } from "./model";

type ModeFilter = "all" | Mode;

const slugOf = (name: string) =>
  name
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/ß/g, "ss")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 60);

export default function Workflows({ me }: { me: Principal }) {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const manage = canManage(me.Role);
  const list = useQuery({ queryKey: ["workflows"], queryFn: listWorkflows, refetchInterval: 15_000 });
  const mine = useQuery({ queryKey: ["workflows-mine"], queryFn: myWorkflowApprovals, refetchInterval: 15_000 });
  const [query, setQuery] = useState("");
  const [modeFilter, setModeFilter] = useState<ModeFilter>("all");
  const [creating, setCreating] = useState<string | null>(null);
  const [error, setError] = useState("");

  const all = list.data ?? [];
  const live = all.filter((w) => w.mode !== "off");
  const sum = (f: (w: Workflow) => number) => live.reduce((n, w) => n + f(w), 0);
  const q = query.trim().toLowerCase();
  const shown = all
    .filter((w) => modeFilter === "all" || w.mode === modeFilter)
    .filter((w) => !q || [w.name, w.slug, w.scope, ...Object.keys(w.roles ?? {})].some((x) => x.toLowerCase().includes(q)))
    // What needs someone first; switched-off workflows last.
    .sort((a, b) => Number(a.mode === "off") - Number(b.mode === "off") || b.runs.stuck - a.runs.stuck || b.runs.open - a.runs.open);

  /* A new workflow starts as a trigger and an end, or as the example. It is
     saved at once — the editor works on stored versions. */
  const create = async (name: string, fromExample: boolean) => {
    setError("");
    const doc = example(i18n.language);
    if (!fromExample) {
      doc.meta = { name, scope: "", owner: me.Email.split("@")[0], mode: "off", roles: {} };
      doc.order = ["t", "e"];
      doc.blocks = { t: { id: "t", type: "trigger", title: t("workflows.type.trigger"), cfg: {} }, e: { id: "e", type: "end", title: t("workflows.type.end"), cfg: {} } };
      doc.links = [{ from: "t", port: "out", to: "e" }];
      doc.positions = { t: { x: 40, y: 80 }, e: { x: 330, y: 80 } };
    } else {
      doc.meta.owner = me.Email.split("@")[0];
      const who = doc.blocks.h;
      if (who) who.cfg.who = doc.meta.owner;
    }
    const slug = slugOf(fromExample ? doc.meta.name : name) || "workflow";
    const res = await saveWorkflow(slug, toSource(doc));
    if (!res.ok) {
      setError(res.problems[0]?.text ?? res.error);
      return;
    }
    await qc.invalidateQueries({ queryKey: ["workflows"] });
    navigate(`/workflows/${slug}`);
  };

  return (
    <div className="wf-overview">
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">{t("workflows.title")}</h1>
        <span className="wf-pill mute">{t("workflows.orgWide")}</span>
        {manage && (
          <button className="btn primary sm ml-auto" onClick={() => setCreating("")}>
            {t("workflows.overview.new")}
          </button>
        )}
      </div>
      <p className="muted mb-4" style={{ maxWidth: 680 }}>
        {t("workflows.lead")}
      </p>

      {creating !== null && (
        <form
          className="wf-create"
          onSubmit={(e) => {
            e.preventDefault();
            if (creating.trim()) void create(creating.trim(), false);
          }}
        >
          <input className="input" autoFocus value={creating} placeholder={t("workflows.overview.newName")} onChange={(e) => setCreating(e.target.value)} />
          <button className="btn primary sm" type="submit" disabled={!creating.trim()}>
            {t("workflows.overview.create")}
          </button>
          <button className="btn sm" type="button" onClick={() => setCreating(null)}>
            {t("workflows.runs.cancel")}
          </button>
          {creating.trim() && <span className="wf-hint">{t("workflows.overview.slugIs", { slug: slugOf(creating) || "workflow" })}</span>}
        </form>
      )}
      {error && <p className="wf-hint" style={{ color: "var(--text-danger)" }}>{error}</p>}

      {list.isSuccess && all.length === 0 ? (
        <div className="wf-empty">
          <BlockIcon type="condition" />
          <div>
            <b>{t("workflows.overview.emptyTitle")}</b>
            <p>{t("workflows.overview.emptyText")}</p>
            {manage && (
              <button className="btn sm" onClick={() => void create("", true)}>
                {t("workflows.overview.createExample")}
              </button>
            )}
          </div>
        </div>
      ) : (
        <>
          <div className="wf-kpis">
            <div className="wf-kpi">
              <div className="v">{sum((w) => w.runs.open)}</div>
              <div className="l">{t("workflows.overview.kpiRunning")}</div>
            </div>
            <div className="wf-kpi">
              <div className="v">{sum((w) => w.runs.queued)}</div>
              <div className="l">{t("workflows.overview.kpiQueued")}</div>
            </div>
            <div className="wf-kpi">
              <div className="v danger">{sum((w) => w.runs.stuck)}</div>
              <div className="l">{t("workflows.overview.kpiStuck")}</div>
            </div>
            <div className="wf-kpi">
              <div className="v">
                {live.length}
                <span className="of"> / {all.length}</span>
              </div>
              <div className="l">{t("workflows.overview.kpiActive")}</div>
            </div>
          </div>

          {(mine.data ?? []).length > 0 && (
            <section className="wf-section">
              <h2 className="wf-section-title">
                {t("workflows.overview.needsYou")} <span className="wf-count-plain">{mine.data!.length}</span>
              </h2>
              <div className="wf-waiting">
                {mine.data!.map((x) => (
                  <button key={x.run} className="wf-case" onClick={() => navigate(`/workflows/${x.workflow}?run=${encodeURIComponent(x.run)}`)}>
                    <BlockIcon type="approval" small />
                    <span className="wf-case-text">
                      {x.question || x.title || x.case}
                      <small>
                        {x.name} · {x.case} · {x.step}
                      </small>
                    </span>
                    <span className="wf-since">{fmtDelta(Date.now() - new Date(x.since).getTime())}</span>
                  </button>
                ))}
              </div>
            </section>
          )}

          <div className="wf-toolbar">
            <input className="input wf-search" value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t("workflows.overview.search")} />
            <div className="wf-filters">
              {(["all", "steer", "watch", "off"] as ModeFilter[]).map((m) => (
                <button key={m} className={modeFilter === m ? "on" : ""} onClick={() => setModeFilter(m)}>
                  {m === "all" ? t("workflows.runs.filter_all") : t(`workflows.mode.${m}`)} <span>{m === "all" ? all.length : all.filter((w) => w.mode === m).length}</span>
                </button>
              ))}
            </div>
          </div>

          <div className="wf-table-wrap">
            <table className="wf-list-table">
              <thead>
                <tr>
                  <th>{t("workflows.col.name")}</th>
                  <th>{t("workflows.roles")}</th>
                  <th>{t("workflows.col.mode")}</th>
                  <th className="num">{t("workflows.col.cases")}</th>
                  <th className="num">{t("workflows.overview.colQueued")}</th>
                  <th className="num">{t("workflows.col.stuck")}</th>
                  <th>{t("workflows.owner")}</th>
                </tr>
              </thead>
              <tbody>
                {shown.map((w) => (
                  <tr
                    key={w.slug}
                    tabIndex={0}
                    className={w.mode === "off" ? "off" : ""}
                    onClick={() => navigate(`/workflows/${w.slug}`)}
                    onKeyDown={(e) => e.key === "Enter" && navigate(`/workflows/${w.slug}`)}
                  >
                    <td>
                      <div className="wf-name">{w.name}</div>
                      <div className="wf-desc">
                        {w.slug} · {t("workflows.versionN", { n: w.version })}
                        {w.scope ? ` · ${w.scope}` : ""}
                      </div>
                    </td>
                    <td className="wf-stations">{Object.keys(w.roles ?? {}).join(" · ") || "–"}</td>
                    <td>
                      <span className={`wf-mode-dot m-${w.mode}`}>{t(`workflows.mode.${w.mode}`)}</span>
                    </td>
                    <td className="num">{w.mode === "off" ? "–" : w.runs.open}</td>
                    <td className="num">{w.mode === "off" || !w.runs.queued ? "–" : w.runs.queued}</td>
                    <td className={`num${w.runs.stuck && w.mode !== "off" ? " danger" : ""}`}>{w.mode === "off" || !w.runs.stuck ? "–" : w.runs.stuck}</td>
                    <td>
                      {w.owner || "–"}
                      <div className="wf-desc">{new Date(w.updated_at).toLocaleDateString(i18n.language)}</div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {shown.length === 0 && all.length > 0 && <p className="wf-muted">{t("workflows.overview.none")}</p>}
        </>
      )}

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

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import {
  api,
  ApiError,
  totalInput,
  type Agent,
  type CostBucket,
  type IndicatorReport,
  type OrgCostReport,
  type RunCost,
  type Tokens,
} from "../api";
import { exact, fmtCount, fmtUSD } from "../format";
import { PriceList } from "../components/PriceList";

// --- Formatierung -----------------------------------------------------------

// Number formatting lives in format.ts — one for the whole interface.
// This page used to have its own, and next to it stood raw
// toLocaleString numbers: "3.05 M" and "147952885" on the same screen.

// Buckets are labelled differently depending on the granularity.
function fmtPeriod(iso: string, bucket: string, locale: string): string {
  const d = new Date(iso);
  switch (bucket) {
    case "hour":
      return d.toLocaleString(locale, { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });
    case "month":
      return d.toLocaleDateString(locale, { month: "short", year: "numeric" });
    default:
      return d.toLocaleDateString(locale, { day: "2-digit", month: "2-digit" });
  }
}

// --- SVG-Zeitreihen-Diagramm ------------------------------------------------

type Metric = "cost" | "tokens";

function CostChart({
  data,
  metric,
  bucket,
  locale,
}: {
  data: CostBucket[];
  metric: Metric;
  bucket: string;
  locale: string;
}) {
  const { t } = useTranslation();
  const [hover, setHover] = useState<number | null>(null);

  const H = 280;
  const padL = 58;
  const padR = 16;
  const padT = 16;
  const padB = 46;
  const plotH = H - padT - padB;

  // Bar width scales with the count; with many buckets the chart scrolls.
  const n = data.length;
  const slot = n <= 1 ? 120 : Math.max(26, Math.min(72, Math.round(900 / n)));
  const W = padL + padR + n * slot;
  const barW = Math.min(38, slot * 0.62);

  const valueOf = (b: CostBucket) =>
    metric === "cost" ? b.total_usd : totalInput(b) + b.output_tokens;
  const max = Math.max(1e-9, ...data.map(valueOf));

  // "Nice" upper bound for the Y axis.
  const niceMax = (() => {
    const raw = max;
    const mag = Math.pow(10, Math.floor(Math.log10(raw)));
    const norm = raw / mag;
    const step = norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 5 ? 5 : 10;
    return step * mag;
  })();

  const y = (v: number) => padT + plotH - (v / niceMax) * plotH;
  const gridVals = [0, 0.25, 0.5, 0.75, 1].map((f) => f * niceMax);

  if (n === 0) {
    return <div className="muted text-sm" style={{ padding: "40px 0", textAlign: "center" }}>{t("costs.empty")}</div>;
  }

  const hb = hover != null ? data[hover] : null;
  const hx = hover != null ? padL + hover * slot + slot / 2 : 0;

  return (
    <div style={{ position: "relative", overflowX: "auto" }}>
      <svg width={W} height={H} style={{ display: "block", maxWidth: "none" }} role="img">
        {/* Y-Gitter + Labels */}
        {gridVals.map((v, i) => (
          <g key={i}>
            <line x1={padL} x2={W - padR} y1={y(v)} y2={y(v)} stroke="var(--border)" strokeWidth={1} />
            <text x={padL - 8} y={y(v) + 4} textAnchor="end" fontSize={11} fill="var(--text-muted)">
              {metric === "cost"
                ? v >= 1 ? v.toFixed(0) : v.toFixed(2)
                : fmtCount(v)}
            </text>
          </g>
        ))}
        {/* Balken */}
        {data.map((b, i) => {
          const cx = padL + i * slot + slot / 2;
          const x0 = cx - barW / 2;
          const active = hover === i;
          if (metric === "cost") {
            const v = b.total_usd;
            const h = ((v / niceMax) * plotH) || 0;
            return (
              <g key={i} onMouseEnter={() => setHover(i)} onMouseLeave={() => setHover(null)}>
                <rect x={padL + i * slot} y={padT} width={slot} height={plotH} fill="transparent" />
                <rect
                  x={x0}
                  y={y(v)}
                  width={barW}
                  height={Math.max(h, v > 0 ? 2 : 0)}
                  rx={3}
                  fill="var(--text-accent)"
                  opacity={active ? 1 : 0.85}
                />
              </g>
            );
          }
          // Tokens: stacked input (bottom) + output (top). Input here means
          // EVERYTHING the model read — the cache share is the lion's
          // share and was in no bar before.
          const inH = ((totalInput(b) / niceMax) * plotH) || 0;
          const outH = ((b.output_tokens / niceMax) * plotH) || 0;
          const inY = padT + plotH - inH;
          const outY = inY - outH;
          return (
            <g key={i} onMouseEnter={() => setHover(i)} onMouseLeave={() => setHover(null)}>
              <rect x={padL + i * slot} y={padT} width={slot} height={plotH} fill="transparent" />
              <rect x={x0} y={inY} width={barW} height={inH} rx={2} fill="var(--text-accent)" opacity={active ? 1 : 0.85} />
              <rect x={x0} y={outY} width={barW} height={outH} rx={2} fill="var(--clay)" opacity={active ? 1 : 0.85} />
            </g>
          );
        })}
        {/* X-Achse */}
        <line x1={padL} x2={W - padR} y1={padT + plotH} y2={padT + plotH} stroke="var(--border-strong)" strokeWidth={1} />
        {data.map((b, i) => {
          // Thin out the labels so they do not overlap.
          const every = Math.ceil(n / Math.max(1, Math.floor((W - padL - padR) / 70)));
          if (i % every !== 0 && i !== n - 1) return null;
          const cx = padL + i * slot + slot / 2;
          return (
            <text key={i} x={cx} y={H - padB + 18} textAnchor="middle" fontSize={11} fill="var(--text-muted)">
              {fmtPeriod(b.period, bucket, locale)}
            </text>
          );
        })}
        {/* Hover guide line */}
        {hb && <line x1={hx} x2={hx} y1={padT} y2={padT + plotH} stroke="var(--border-strong)" strokeWidth={1} strokeDasharray="3 3" />}
      </svg>

      {hb && (
        <div
          style={{
            position: "absolute",
            left: Math.min(hx + 12, W - 190),
            top: padT + 4,
            pointerEvents: "none",
            background: "var(--surface-2)",
            border: "0.5px solid var(--border-strong)",
            borderRadius: 8,
            padding: "8px 10px",
            fontSize: 12,
            boxShadow: "0 4px 16px var(--shadow-md)",
            minWidth: 150,
          }}
        >
          <div className="muted" style={{ fontSize: 11, marginBottom: 4 }}>{fmtPeriod(hb.period, bucket, locale)}</div>
          {metric === "cost" ? (
            <>
              <div style={{ fontWeight: 600 }}>{fmtUSD(hb.total_usd)}</div>
              <div className="muted" style={{ fontSize: 11 }}>{t("costs.runs")}: {hb.entries}</div>
            </>
          ) : (
            <>
              <div style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
                <span style={{ color: "var(--text-accent)" }}>■ {t("costs.input")}</span>
                <span style={{ fontWeight: 600 }} title={exact(totalInput(hb))}>{fmtCount(totalInput(hb))}</span>
              </div>
              <div className="muted" style={{ display: "flex", justifyContent: "space-between", gap: 12, fontSize: 11 }}>
                <span>{t("costs.ofWhichCached")}</span>
                <span title={exact(hb.cache_read_tokens + hb.cache_creation_tokens)}>
                  {fmtCount(hb.cache_read_tokens + hb.cache_creation_tokens)}
                </span>
              </div>
              <div style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
                <span style={{ color: "var(--clay)" }}>■ {t("costs.output")}</span>
                <span style={{ fontWeight: 600 }} title={exact(hb.output_tokens)}>{fmtCount(hb.output_tokens)}</span>
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}

// --- KPI-Kacheln ------------------------------------------------------------

function Kpi({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="card" style={{ flex: 1, minWidth: 150 }}>
      <div className="muted text-xs">{label}</div>
      <div style={{ fontSize: 24, fontWeight: 600, marginTop: 2 }}>{value}</div>
      {sub && <div className="muted text-xs" style={{ marginTop: 2 }}>{sub}</div>}
    </div>
  );
}

// --- Segmentierte Umschalter ------------------------------------------------

function Seg<T extends string | number>({
  value,
  options,
  onChange,
}: {
  value: T;
  options: { v: T; label: string }[];
  onChange: (v: T) => void;
}) {
  return (
    <div style={{ display: "inline-flex", gap: 2, background: "var(--surface-1)", border: "0.5px solid var(--border)", borderRadius: 8, padding: 2 }}>
      {options.map((o) => {
        const active = o.v === value;
        return (
          <button
            key={String(o.v)}
            onClick={() => onChange(o.v)}
            style={{
              border: "none",
              borderRadius: 6,
              padding: "5px 12px",
              fontSize: 13,
              cursor: "pointer",
              background: active ? "var(--surface-2)" : "transparent",
              color: active ? "var(--text-primary)" : "var(--text-secondary)",
              fontWeight: active ? 600 : 400,
              boxShadow: active ? "0 1px 3px var(--shadow-xs)" : "none",
            }}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

// --- Breakdown bars (per agent / per model) --------------------------------

function BreakdownBar({ label, value, max, display, to }: { label: string; value: number; max: number; display: string; to?: string }) {
  const pct = max > 0 ? (value / max) * 100 : 0;
  const name = to ? <Link to={to} className="hover:underline">{label}</Link> : label;
  return (
    <div style={{ marginBottom: 10 }}>
      <div style={{ display: "flex", justifyContent: "space-between", fontSize: 13, marginBottom: 3 }}>
        <span className="truncate" style={{ maxWidth: "70%" }}>{name}</span>
        <span style={{ fontWeight: 600 }}>{display}</span>
      </div>
      <div style={{ height: 8, background: "var(--surface-1)", borderRadius: 4, overflow: "hidden" }}>
        <div style={{ width: `${pct}%`, height: "100%", background: "var(--text-accent)", borderRadius: 4 }} />
      </div>
    </div>
  );
}

// --- Seite ------------------------------------------------------------------

const RANGES: { v: number; bucket: string }[] = [
  { v: 1, bucket: "hour" },
  { v: 7, bucket: "day" },
  { v: 30, bucket: "day" },
  { v: 90, bucket: "week" },
];

/** The cost categories side by side. They stand here because the USD number
 *  alone hides the key observation: the weight lies almost entirely on the
 *  cached input side, not on what the agents write. Measured on covey.work:
 *  112M cache read against 3.2M output in 24 hours — the prompt is read anew
 *  in every turn. Whoever misses that optimizes the output length and
 *  wonders why nothing happens. */
function TokenMix({ tokens, locale }: { tokens: Tokens; locale: string }) {
  const { t } = useTranslation();
  const parts = [
    { key: "cacheRead", value: tokens.cache_read_tokens, color: "var(--text-accent)" },
    { key: "cacheWrite", value: tokens.cache_creation_tokens, color: "var(--sage, #7a9)" },
    { key: "inputFresh", value: tokens.input_tokens, color: "var(--muted, #999)" },
    { key: "output", value: tokens.output_tokens, color: "var(--clay)" },
  ];
  const sum = parts.reduce((a, p) => a + p.value, 0);
  if (sum === 0) return <div className="muted text-sm">{t("costs.empty")}</div>;
  return (
    <div>
      <div style={{ display: "flex", height: 10, borderRadius: 5, overflow: "hidden", marginBottom: 12 }}>
        {parts.map((p) => (
          <div key={p.key} style={{ width: `${(p.value / sum) * 100}%`, background: p.color }} />
        ))}
      </div>
      {parts.map((p) => (
        <div key={p.key} className="flex items-center justify-between text-sm" style={{ padding: "3px 0" }}>
          <span className="flex items-center gap-2">
            <span style={{ color: p.color }}>■</span>
            {t(`costs.mix.${p.key}`)}
          </span>
          <span className="muted">
            {fmtCount(p.value)} · {sum > 0 ? Math.round((p.value / sum) * 100) : 0}%
          </span>
        </div>
      ))}
    </div>
  );
}

/** The most expensive runs. Answers the question the aggregates leave open:
 *  WHICH run burned the money. A run without actions is marked on purpose —
 *  it read, thought and went back to sleep. */
function ExpensiveRuns({ runs, locale }: { runs: RunCost[]; locale: string }) {
  const { t } = useTranslation();
  if (runs.length === 0) return <div className="muted text-sm">{t("costs.empty")}</div>;
  return (
    <div style={{ overflowX: "auto" }}>
      <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
        <thead>
          <tr className="muted" style={{ textAlign: "left" }}>
            <th style={{ padding: "4px 8px 8px 0" }}>{t("costs.runs.task")}</th>
            <th style={{ padding: "4px 8px 8px 0" }}>{t("costs.runs.agent")}</th>
            <th style={{ padding: "4px 8px 8px 0", textAlign: "right" }}>{t("costs.runs.cost")}</th>
            <th style={{ padding: "4px 8px 8px 0", textAlign: "right" }}>{t("costs.runs.cached")}</th>
            <th style={{ padding: "4px 0 8px 0", textAlign: "right" }}>{t("costs.runs.actions")}</th>
          </tr>
        </thead>
        <tbody>
          {runs.map((r) => (
            <tr key={r.task_id} style={{ borderTop: "1px solid var(--border, #e5e5e5)" }}>
              <td style={{ padding: "6px 8px 6px 0" }}>
                <div style={{ maxWidth: 340, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {r.title}
                </div>
                <span className="muted text-xs">
                  {r.origin} · {r.state} · {fmtPeriod(r.started_at, "hour", locale)}
                </span>
              </td>
              <td style={{ padding: "6px 8px 6px 0" }}>
                <Link to={`/agents/${r.agent_id}`}>{r.slug}</Link>
              </td>
              <td style={{ padding: "6px 8px 6px 0", textAlign: "right", fontWeight: 600 }}>{fmtUSD(r.total_usd)}</td>
              <td style={{ padding: "6px 8px 6px 0", textAlign: "right" }} className="muted">
                {fmtCount(r.cache_read_tokens + r.cache_creation_tokens)}
              </td>
              <td style={{ padding: "6px 0", textAlign: "right" }}>
                {r.actions === 0 ? (
                  <span title={t("costs.runs.idleHint")} style={{ color: "var(--clay)" }}>
                    {t("costs.runs.idle")}
                  </span>
                ) : (
                  <span className="muted">{r.actions}</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export default function Costs() {
  const { t, i18n } = useTranslation();
  const locale = i18n.language === "de" ? "de-DE" : "en-US";

  const [days, setDays] = useState(30);
  const [bucket, setBucket] = useState("day");
  const [metric, setMetric] = useState<Metric>("cost");
  const [scope, setScope] = useState<string>("org"); // "org" | agentId

  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[] | null>("/agents") });

  const org = useQuery({
    queryKey: ["cost", "org", days, bucket],
    queryFn: () => api<OrgCostReport>(`/cost/org?days=${days}&bucket=${bucket}`),
    enabled: scope === "org",
    refetchInterval: 30000,
  });

  const agentSeries = useQuery({
    queryKey: ["cost", "series", scope, days, bucket],
    queryFn: () => api<CostBucket[] | null>(`/agents/${scope}/cost/series?days=${days}&bucket=${bucket}`),
    enabled: scope !== "org",
    refetchInterval: 30000,
  });

  // The most expensive runs of the period — org-wide or for the chosen agent.
  const runs = useQuery({
    queryKey: ["cost", "runs", scope, days],
    queryFn: () =>
      api<RunCost[] | null>(
        scope === "org" ? `/cost/runs?days=${days}&limit=25` : `/agents/${scope}/cost/runs?days=${days}&limit=25`,
      ),
    refetchInterval: 30000,
  });

  // The price list hangs on the same scope and period as everything else on
  // this page — performance is no second topic next to the costs, but the
  // other half of the same one.
  const indicators = useQuery({
    queryKey: ["cost", "indicators", scope, days],
    queryFn: () =>
      api<IndicatorReport>(
        scope === "org" ? `/cost/indicators?days=${days}` : `/agents/${scope}/cost/indicators?days=${days}`,
      ),
    refetchInterval: 30000,
    // The metrics of ONE agent follow the work record (spec/21): controlling
    // gets a 403 on it. Without retry:false that would be three retries
    // every 30 seconds for an answer that does not change.
    // Performance.tsx does the same on the same route.
    retry: false,
  });
  // A role that may not see something should not get it as an empty box:
  // empty means "there are no metrics", and that is a different statement
  // than "you do not see them".
  const indicatorsForbidden = indicators.error instanceof ApiError && indicators.error.status === 403;

  const setRange = (v: number) => {
    setDays(v);
    const r = RANGES.find((x) => x.v === v);
    if (r) setBucket(r.bucket);
  };

  const isOrg = scope === "org";
  const rep = org.data;
  const series: CostBucket[] = isOrg ? rep?.series ?? [] : agentSeries.data ?? [];

  // Metrics for the current scope.
  const totals = useMemo(() => {
    const cached = (t: Tokens) => t.cache_read_tokens + t.cache_creation_tokens;
    if (isOrg && rep) {
      return { usd: rep.total_usd, input: totalInput(rep), cached: cached(rep), output: rep.output_tokens, entries: rep.entries };
    }
    return series.reduce(
      (a, b) => ({
        usd: a.usd + b.total_usd,
        input: a.input + totalInput(b),
        cached: a.cached + cached(b),
        output: a.output + b.output_tokens,
        entries: a.entries + b.entries,
      }),
      { usd: 0, input: 0, cached: 0, output: 0, entries: 0 },
    );
  }, [isOrg, rep, series]);

  // The cost categories for the current scope: org-wide straight from the
  // report, for an agent summed up from its time series.
  const mix: Tokens = useMemo(() => {
    if (isOrg && rep) return rep;
    return series.reduce<Tokens>(
      (a, b) => ({
        input_tokens: a.input_tokens + b.input_tokens,
        output_tokens: a.output_tokens + b.output_tokens,
        cache_read_tokens: a.cache_read_tokens + b.cache_read_tokens,
        cache_creation_tokens: a.cache_creation_tokens + b.cache_creation_tokens,
      }),
      { input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_creation_tokens: 0 },
    );
  }, [isOrg, rep, series]);

  const agentList = agents.data ?? [];
  const scopeName = isOrg ? t("costs.wholeOrg") : agentList.find((a) => a.id === scope)?.display_name ?? "";

  const maxAgent = Math.max(1, ...(rep?.agents ?? []).map((a) => a.total_usd));
  const maxModel = Math.max(1, ...(rep?.models ?? []).map((m) => m.total_usd));
  const maxCredential = Math.max(1, ...(rep?.credentials ?? []).map((c) => c.total_usd));

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-1">
        <h1 className="text-[22px]">{t("costs.title")}</h1>
        <span className="muted">{scopeName}</span>
      </div>
      <p className="muted text-sm mb-4" style={{ maxWidth: 620 }}>{t("costs.subtitle")}</p>

      {/* Steuerung */}
      <div className="flex flex-wrap items-center gap-3 mb-4">
        <Seg
          value={scope}
          onChange={setScope}
          options={[{ v: "org", label: t("costs.scopeOrg") }, ...agentList.map((a) => ({ v: a.id, label: a.display_name }))]}
        />
        <div style={{ flex: 1 }} />
        <Seg
          value={days}
          onChange={setRange}
          options={RANGES.map((r) => ({ v: r.v, label: r.v === 1 ? t("costs.range24h") : t("costs.rangeDays", { count: r.v }) }))}
        />
        <Seg
          value={bucket}
          onChange={setBucket}
          options={[
            { v: "hour", label: t("costs.bHour") },
            { v: "day", label: t("costs.bDay") },
            { v: "week", label: t("costs.bWeek") },
            { v: "month", label: t("costs.bMonth") },
          ]}
        />
      </div>

      {/* KPIs */}
      <div className="flex flex-wrap gap-3 mb-4">
        <Kpi label={t("costs.totalCost")} value={fmtUSD(totals.usd)} sub={t("costs.runsN", { count: totals.entries })} />
        <Kpi
          label={t("costs.inputTokens")}
          value={fmtCount(totals.input)}
          sub={t("costs.cachedShare", {
            pct: totals.input > 0 ? Math.round((totals.cached / totals.input) * 100) : 0,
          })}
        />
        <Kpi label={t("costs.outputTokens")} value={fmtCount(totals.output)} sub={exact(totals.output)} />
        <Kpi label={t("costs.totalTokens")} value={fmtCount(totals.input + totals.output)} />
      </div>

      {/* Diagramm */}
      <div className="card mb-4">
        <div className="flex items-center justify-between mb-3 flex-wrap gap-2">
          <div style={{ fontWeight: 600 }}>{metric === "cost" ? t("costs.chartCost") : t("costs.chartTokens")}</div>
          <div className="flex items-center gap-3">
            {metric === "tokens" && (
              <div className="flex items-center gap-3 text-xs muted">
                <span><span style={{ color: "var(--text-accent)" }}>■</span> {t("costs.input")}</span>
                <span><span style={{ color: "var(--clay)" }}>■</span> {t("costs.output")}</span>
              </div>
            )}
            <Seg
              value={metric}
              onChange={setMetric}
              options={[{ v: "cost", label: t("costs.mCost") }, { v: "tokens", label: t("costs.mTokens") }]}
            />
          </div>
        </div>
        <CostChart data={series} metric={metric} bucket={bucket} locale={locale} />
      </div>

      {/* Price list + cost types + priciest runs: they hold for every scope */}
      <div className="grid gap-4 mb-4" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(320px, 1fr))" }}>
        <div className="card">
          <div style={{ fontWeight: 600, marginBottom: 4 }}>{t("costs.indicators.title")}</div>
          <p className="muted text-xs mb-3">{t("costs.indicators.hint")}</p>
          {indicatorsForbidden ? (
            <p className="muted text-xs">{t("costs.indicators.restricted")}</p>
          ) : (
            <PriceList rep={indicators.data} />
          )}
        </div>
        <div className="card">
          <div style={{ fontWeight: 600, marginBottom: 4 }}>{t("costs.mix.title")}</div>
          <p className="muted text-xs mb-3">{t("costs.mix.hint")}</p>
          <TokenMix tokens={mix} locale={locale} />
        </div>
        <div className="card" style={{ gridColumn: "span 1" }}>
          <div style={{ fontWeight: 600, marginBottom: 4 }}>{t("costs.runs.title")}</div>
          <p className="muted text-xs mb-3">{t("costs.runs.hint")}</p>
          <ExpensiveRuns runs={runs.data ?? []} locale={locale} />
        </div>
      </div>

      {/* Breakdown only org-wide */}
      {isOrg && (
        <div className="grid gap-4" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(320px, 1fr))" }}>
          <div className="card">
            <div style={{ fontWeight: 600, marginBottom: 12 }}>{t("costs.byAgent")}</div>
            {(rep?.agents ?? []).length === 0 && <div className="muted text-sm">{t("costs.empty")}</div>}
            {(rep?.agents ?? []).map((a) => (
              <BreakdownBar
                key={a.agent_id}
                label={a.display_name}
                value={a.total_usd}
                max={maxAgent}
                display={fmtUSD(a.total_usd)}
                to={`/agents/${a.agent_id}`}
              />
            ))}
          </div>
          <div className="card">
            <div style={{ fontWeight: 600, marginBottom: 12 }}>{t("costs.byModel")}</div>
            {(rep?.models ?? []).length === 0 && <div className="muted text-sm">{t("costs.empty")}</div>}
            {(rep?.models ?? []).map((m) => (
              <BreakdownBar
                key={m.model}
                label={m.model}
                value={m.total_usd}
                max={maxModel}
                display={fmtUSD(m.total_usd)}
              />
            ))}
          </div>
          {/* Per credential — only when something is assigned at all. For a
              single key the card says nothing that the
              total does not say already. */}
          {(rep?.credentials ?? []).length > 0 && (
            <div className="card">
              <div style={{ fontWeight: 600, marginBottom: 12 }}>{t("costs.byCredential")}</div>
              {(rep?.credentials ?? []).map((c) => (
                <BreakdownBar
                  key={`${c.secret_key}#${c.slot}`}
                  label={c.label || `${c.secret_key} #${c.slot}`}
                  value={c.total_usd}
                  max={maxCredential}
                  display={fmtUSD(c.total_usd)}
                />
              ))}
              <p className="muted text-xs mt-2 mb-0">{t("costs.credentialHint")}</p>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

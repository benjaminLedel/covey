import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import {
  api,
  type Agent,
  type CostBucket,
  type OrgCostReport,
} from "../api";

// --- Formatierung -----------------------------------------------------------

function fmtUSD(v: number): string {
  if (v >= 1000) return `${v.toFixed(0)} $`;
  if (v >= 1) return `${v.toFixed(2)} $`;
  return `${v.toFixed(4)} $`;
}

function fmtTokens(v: number, locale: string): string {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(2)} M`;
  if (v >= 10_000) return `${(v / 1000).toFixed(1)} k`;
  return v.toLocaleString(locale);
}

// Buckets werden je nach Granularität unterschiedlich beschriftet.
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

  // Balkenbreite skaliert mit der Anzahl; bei vielen Buckets scrollt die Karte.
  const n = data.length;
  const slot = n <= 1 ? 120 : Math.max(26, Math.min(72, Math.round(900 / n)));
  const W = padL + padR + n * slot;
  const barW = Math.min(38, slot * 0.62);

  const valueOf = (b: CostBucket) =>
    metric === "cost" ? b.total_usd : b.input_tokens + b.output_tokens;
  const max = Math.max(1e-9, ...data.map(valueOf));

  // „schöne" Obergrenze für die Y-Achse.
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
                : fmtTokens(v, locale)}
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
          // Tokens: gestapelt input (unten) + output (oben)
          const inH = ((b.input_tokens / niceMax) * plotH) || 0;
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
          // Labels ausdünnen, damit sie nicht überlappen.
          const every = Math.ceil(n / Math.max(1, Math.floor((W - padL - padR) / 70)));
          if (i % every !== 0 && i !== n - 1) return null;
          const cx = padL + i * slot + slot / 2;
          return (
            <text key={i} x={cx} y={H - padB + 18} textAnchor="middle" fontSize={11} fill="var(--text-muted)">
              {fmtPeriod(b.period, bucket, locale)}
            </text>
          );
        })}
        {/* Hover-Führungslinie */}
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
                <span style={{ fontWeight: 600 }}>{hb.input_tokens.toLocaleString(locale)}</span>
              </div>
              <div style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
                <span style={{ color: "var(--clay)" }}>■ {t("costs.output")}</span>
                <span style={{ fontWeight: 600 }}>{hb.output_tokens.toLocaleString(locale)}</span>
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

// --- Aufschlüsselungs-Balken (pro Agent / pro Modell) -----------------------

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

  const setRange = (v: number) => {
    setDays(v);
    const r = RANGES.find((x) => x.v === v);
    if (r) setBucket(r.bucket);
  };

  const isOrg = scope === "org";
  const rep = org.data;
  const series: CostBucket[] = isOrg ? rep?.series ?? [] : agentSeries.data ?? [];

  // Kennzahlen fürs aktuelle Scope.
  const totals = useMemo(() => {
    if (isOrg && rep) {
      return { usd: rep.total_usd, input: rep.input_tokens, output: rep.output_tokens, entries: rep.entries };
    }
    return series.reduce(
      (a, b) => ({ usd: a.usd + b.total_usd, input: a.input + b.input_tokens, output: a.output + b.output_tokens, entries: a.entries + b.entries }),
      { usd: 0, input: 0, output: 0, entries: 0 },
    );
  }, [isOrg, rep, series]);

  const agentList = agents.data ?? [];
  const scopeName = isOrg ? t("costs.wholeOrg") : agentList.find((a) => a.id === scope)?.display_name ?? "";

  const maxAgent = Math.max(1, ...(rep?.agents ?? []).map((a) => a.total_usd));
  const maxModel = Math.max(1, ...(rep?.models ?? []).map((m) => m.total_usd));

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
        <Kpi label={t("costs.inputTokens")} value={fmtTokens(totals.input, locale)} sub={totals.input.toLocaleString(locale)} />
        <Kpi label={t("costs.outputTokens")} value={fmtTokens(totals.output, locale)} sub={totals.output.toLocaleString(locale)} />
        <Kpi label={t("costs.totalTokens")} value={fmtTokens(totals.input + totals.output, locale)} />
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

      {/* Aufschlüsselung nur org-weit */}
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
        </div>
      )}
    </div>
  );
}

import { useTranslation } from "react-i18next";
import { type IndicatorReport } from "../api";
import { fmtDelta, fmtUSD } from "../format";

/** Sparkline: the trend as a small area, without axes and without numbers.
 *
 *  It answers a question the total keeps silent on: whether 25 reviews grew
 *  evenly across four weeks or on a single afternoon. Deliberately without
 *  labels — whoever needs the exact trend goes to the costs page; what
 *  counts here is the shape. */
function Spark({ data, width = 54, height = 14 }: { data?: number[]; width?: number; height?: number }) {
  if (!data || data.length < 2) return null;
  const max = Math.max(...data);
  if (max <= 0) return null;
  const dx = width / (data.length - 1);
  const y = (v: number) => height - 1 - (v / max) * (height - 2);
  const punkte = data.map((v, i) => `${(i * dx).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  return (
    <svg width={width} height={height} style={{ verticalAlign: "middle", overflow: "visible" }} aria-hidden="true">
      <polyline
        points={`0,${height} ${punkte} ${width},${height}`}
        fill="var(--text-accent)"
        opacity={0.16}
        stroke="none"
      />
      <polyline points={punkte} fill="none" stroke="var(--text-accent)" strokeWidth={1.25} strokeLinejoin="round" />
    </svg>
  );
}

/** The change against the preceding period of the same length.
 *
 *  `gutWennKleiner` colours it — and only where the direction is unambiguous.
 *  For the unit price it is (cheaper is better), for the count it is not:
 *  twice as many tickets can be twice the work or twice the
 *  inbox, and that is not for the UI to decide.
 *
 *  Without a previous value (the indicator is new, or the price fell below
 *  the minimum quantity) there is no trend — and no 100 %, which would only
 *  mean that there was nothing before. */
function Delta({ now, prev, gutWennKleiner = false }: { now?: number; prev?: number; gutWennKleiner?: boolean }) {
  if (now === undefined || prev === undefined || prev <= 0) return null;
  const pct = Math.round(((now - prev) / prev) * 100);
  if (pct === 0) return null;
  const farbe = !gutWennKleiner
    ? "var(--text-muted)"
    : pct < 0
      ? "var(--text-success)"
      : "var(--text-warning)";
  return (
    <span style={{ color: farbe, fontWeight: 400, fontSize: 11, whiteSpace: "nowrap" }}>
      {pct > 0 ? "▲" : "▼"} {Math.abs(pct)} %
    </span>
  );
}

/** The price list: what the workforce delivered, and what one unit of it
 *  cost.
 *
 *  The count stands next to the price and is never replaced by it: "$3.20 per
 *  ticket" says nothing about whether the agent handled five tickets or five
 *  hundred. Below the minimum quantity the server returns no price at all —
 *  then only the raw number stands there.
 *
 *  The failed runs stand in the same block, without a price. They are not
 *  work that you buy, but without them the prices read wrong: whoever hands
 *  off every hard case has excellent unit costs across the rest.
 *
 *  compact switches to the strip display: the same numbers, but as fields
 *  side by side instead of a list of bars.
 *
 *  The reason is width. On the costs page the list stands in a narrow grid
 *  column, there the bars order the indicators by size. On the agent page the
 *  same card runs across the full window width — that makes metre-long bars
 *  which push the actual content of the page (the tabs) out of view. Same
 *  component, so that both views keep the same reading; only the layout
 *  differs. */
export function PriceList({ rep, compact = false }: { rep?: IndicatorReport; compact?: boolean }) {
  const { t } = useTranslation();
  const rows = rep?.indicators ?? [];
  if (!rep || (rows.length === 0 && rep.failed === 0)) {
    return <div className="muted text-sm">{t("costs.indicators.none")}</div>;
  }
  if (compact) return <PriceStrip rep={rep} rows={rows} />;
  const max = Math.max(1, ...rows.map((r) => r.count));
  return (
    <div>
      {rows.map((r) => (
        <div key={r.key} style={{ marginBottom: 10 }}>
          <div style={{ display: "flex", justifyContent: "space-between", fontSize: 13, marginBottom: 3, gap: 8 }}>
            <span className="truncate" style={{ maxWidth: "50%" }} title={r.action || r.key}>{r.title}</span>
            <span style={{ display: "flex", gap: 10, whiteSpace: "nowrap", alignItems: "center" }}>
              <span style={{ fontWeight: 600 }}>{r.count}</span>
              <Delta now={r.count} prev={r.prev_count} />
              {/* Three cases, and they mean different things: a price; too
                  few events for a trustworthy price; or no events at
                  all. Writing "too few" at zero would assert that there were
                  some — and hide exactly the case the lint reports on the
                  agent page. */}
              {r.unit_usd !== undefined ? (
                <>
                  <span className="muted">{t("costs.indicators.perUnit", { price: fmtUSD(r.unit_usd) })}</span>
                  <Delta now={r.unit_usd} prev={r.prev_unit_usd} gutWennKleiner />
                </>
              ) : r.count === 0 ? (
                <span style={{ color: "var(--text-warning)" }} title={t("costs.indicators.countsNothingHint")}>
                  {t("costs.indicators.countsNothing")}
                </span>
              ) : (
                <span className="muted" title={t("costs.indicators.tooFewHint")}>{t("costs.indicators.tooFew")}</span>
              )}
            </span>
          </div>
          {/* Bar and curve side by side, because they say different
              things: the bar compares the indicators with each other, the
              curve shows the trend of this one across the period. */}
          <div className="flex items-center" style={{ gap: 8 }}>
            <div style={{ flex: 1, height: 8, background: "var(--surface-1)", borderRadius: 4, overflow: "hidden" }}>
              <div style={{ width: `${(r.count / max) * 100}%`, height: "100%", background: "var(--text-accent)", borderRadius: 4 }} />
            </div>
            <Spark data={r.series} width={48} height={12} />
          </div>
          {/* The rework rate stands in the same line as its indicator:
              a price whose quality number lies elsewhere gets
              quoted on its own. */}
          {r.count > 0 && (r.returned ?? 0) > 0 && (
            <div className="muted text-xs" style={{ marginTop: 2 }} title={t("costs.indicators.returnedHint")}>
              {t("costs.indicators.returned", { pct: Math.round(((r.returned ?? 0) / r.count) * 100) })}
            </div>
          )}
        </div>
      ))}
      {rep.failed > 0 && (
        <div className="flex justify-between text-[13px]" style={{ marginTop: rows.length ? 12 : 0 }}>
          <span className="muted">{t("costs.indicators.failed")}</span>
          <span style={{ fontWeight: 600, color: "var(--text-warning)" }}>{rep.failed}</span>
        </div>
      )}
      {rep.quality.decided > 0 && (
        <div className="flex justify-between text-[13px]" style={{ marginTop: 4 }}>
          <span className="muted" title={t("costs.indicators.deniedHint")}>{t("costs.indicators.denied")}</span>
          <span style={{ fontWeight: 600 }}>
            {Math.round((rep.quality.denied / rep.quality.decided) * 100)} %
            <span className="muted"> ({rep.quality.decided})</span>
          </span>
        </div>
      )}
      {rep.quality.response_seconds !== undefined && (
        <div className="flex justify-between text-[13px]" style={{ marginTop: 4 }}>
          <span className="muted" title={t("costs.indicators.responseHint")}>{t("costs.indicators.response")}</span>
          <span style={{ fontWeight: 600 }}>{fmtDelta(rep.quality.response_seconds * 1000)}</span>
        </div>
      )}
      <p className="muted text-xs" style={{ marginTop: 10 }}>{t("costs.indicators.notSummable")}</p>
    </div>
  );
}


/** The strip display: one field per indicator, in the pattern of the cost
 *  strip above it (small label, bold value, adjunct small below). Wraps when
 *  there are many indicators, instead of running wide.
 *
 *  The counter-numbers stand at the end of the same strip and not in a box of
 *  their own: whoever reads the prices should see, without a second look, how
 *  many runs produced nothing. */
function PriceStrip({ rep, rows }: { rep: IndicatorReport; rows: NonNullable<IndicatorReport["indicators"]> }) {
  const { t } = useTranslation();
  return (
    <div className="card flex flex-wrap text-sm" style={{ gap: "10px 32px", alignItems: "flex-start" }}>
      {/* The period stands FIRST because it labels everything behind it:
          directly above lies the cost strip with the agent's TOTAL costs, and
          30-day figures without context beside it invite confusion.
          Right-aligned at the end it slipped alone into a second line when
          wrapping and made the strip a third taller. */}
      <div style={{ minWidth: 96 }}>
        <div className="muted text-xs">{t("costs.indicators.period")}</div>
        <div className="muted">{t("agent.performance.window")}</div>
      </div>
      {rows.map((r) => (
        <div key={r.key} style={{ minWidth: 96 }}>
          <div className="muted text-xs truncate" style={{ maxWidth: 190 }} title={r.action || r.key}>{r.title}</div>
          <div className="font-medium flex items-center" style={{ gap: 6 }}>
            {r.count}
            <Delta now={r.count} prev={r.prev_count} />
            <Spark data={r.series} width={42} />
            {r.unit_usd !== undefined ? (
              <>
                <span className="muted" style={{ fontWeight: 400 }}>{t("costs.indicators.perUnit", { price: fmtUSD(r.unit_usd) })}</span>
                <Delta now={r.unit_usd} prev={r.prev_unit_usd} gutWennKleiner />
              </>
            ) : r.count === 0 ? (
              <span style={{ color: "var(--text-warning)", fontWeight: 400 }} title={t("costs.indicators.countsNothingHint")}>
                {" · "}{t("costs.indicators.countsNothing")}
              </span>
            ) : null}
          </div>
          {r.count > 0 && (r.returned ?? 0) > 0 && (
            <div className="muted text-xs" title={t("costs.indicators.returnedHint")}>
              {t("costs.indicators.returned", { pct: Math.round(((r.returned ?? 0) / r.count) * 100) })}
            </div>
          )}
        </div>
      ))}
      {rep.failed > 0 && (
        <div style={{ minWidth: 96 }}>
          <div className="muted text-xs">{t("costs.indicators.failed")}</div>
          <div className="font-medium" style={{ color: "var(--text-warning)" }}>{rep.failed}</div>
        </div>
      )}
      {rep.quality.decided > 0 && (
        <div style={{ minWidth: 96 }}>
          <div className="muted text-xs" title={t("costs.indicators.deniedHint")}>{t("costs.indicators.denied")}</div>
          <div className="font-medium">
            {Math.round((rep.quality.denied / rep.quality.decided) * 100)} %
            <span className="muted" style={{ fontWeight: 400 }}> ({rep.quality.decided})</span>
          </div>
        </div>
      )}
      {rep.quality.response_seconds !== undefined && (
        <div style={{ minWidth: 96 }}>
          <div className="muted text-xs" title={t("costs.indicators.responseHint")}>{t("costs.indicators.response")}</div>
          <div className="font-medium">{fmtDelta(rep.quality.response_seconds * 1000)}</div>
        </div>
      )}
    </div>
  );
}

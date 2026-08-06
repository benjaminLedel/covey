import { useTranslation } from "react-i18next";
import { type IndicatorReport } from "../api";
import { fmtDelta, fmtUSD } from "../format";

/** Die Preisliste: was die Belegschaft geliefert hat, und was eine Einheit
 *  davon gekostet hat.
 *
 *  Die Anzahl steht neben dem Preis und wird nie von ihm ersetzt: „3,20 $ pro
 *  Ticket" sagt nichts darüber, ob der Agent fünf Tickets bearbeitet hat oder
 *  fünfhundert. Unter der Mindestmenge liefert der Server gar keinen Preis —
 *  dann steht dort nur die Rohzahl.
 *
 *  Die gescheiterten Läufe stehen im selben Block, ohne Preis. Sie sind keine
 *  Leistung, die man einkauft, aber ohne sie liest man die Preise falsch: wer
 *  jeden schweren Fall abgibt, hat auf dem Rest hervorragende Stückkosten. */
export function PriceList({ rep }: { rep?: IndicatorReport }) {
  const { t } = useTranslation();
  const rows = rep?.indicators ?? [];
  if (!rep || (rows.length === 0 && rep.failed === 0)) {
    return <div className="muted text-sm">{t("costs.indicators.none")}</div>;
  }
  const max = Math.max(1, ...rows.map((r) => r.count));
  return (
    <div>
      {rows.map((r) => (
        <div key={r.key} style={{ marginBottom: 10 }}>
          <div style={{ display: "flex", justifyContent: "space-between", fontSize: 13, marginBottom: 3, gap: 8 }}>
            <span className="truncate" style={{ maxWidth: "50%" }} title={r.action || r.key}>{r.title}</span>
            <span style={{ display: "flex", gap: 10, whiteSpace: "nowrap" }}>
              <span style={{ fontWeight: 600 }}>{r.count}</span>
              {/* Drei Fälle, und sie bedeuten Verschiedenes: ein Preis; zu
                  wenige Ereignisse für einen belastbaren Preis; oder gar keine
                  Ereignisse. „Zu wenige" bei null zu schreiben behauptete, es
                  gäbe welche — und verdeckte genau den Fall, den der Lint auf
                  der Agentenseite meldet. */}
              {r.unit_usd !== undefined ? (
                <span className="muted">{t("costs.indicators.perUnit", { price: fmtUSD(r.unit_usd) })}</span>
              ) : r.count === 0 ? (
                <span style={{ color: "var(--text-warning)" }} title={t("costs.indicators.countsNothingHint")}>
                  {t("costs.indicators.countsNothing")}
                </span>
              ) : (
                <span className="muted" title={t("costs.indicators.tooFewHint")}>{t("costs.indicators.tooFew")}</span>
              )}
            </span>
          </div>
          <div style={{ height: 8, background: "var(--surface-1)", borderRadius: 4, overflow: "hidden" }}>
            <div style={{ width: `${(r.count / max) * 100}%`, height: "100%", background: "var(--text-accent)", borderRadius: 4 }} />
          </div>
          {/* Die Nacharbeitsquote steht in derselben Zeile wie ihre Kennzahl:
              ein Preis, dessen Qualitätszahl woanders liegt, wird allein
              zitiert. */}
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


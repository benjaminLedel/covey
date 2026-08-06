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
 *  jeden schweren Fall abgibt, hat auf dem Rest hervorragende Stückkosten.
 *
 *  compact schaltet auf die Leisten-Darstellung um: dieselben Zahlen, aber als
 *  Felder nebeneinander statt als Balkenliste.
 *
 *  Der Grund ist die Breite. Auf der Kostenseite steht die Liste in einer
 *  schmalen Rasterspalte, dort ordnen die Balken die Kennzahlen der Größe nach.
 *  Auf der Agentenseite läuft dieselbe Karte über die volle Fensterbreite —
 *  daraus werden meterlange Balken, die den eigentlichen Inhalt der Seite (die
 *  Reiter) aus dem Bild schieben. Gleiche Komponente, damit die beiden Ansichten
 *  dieselbe Lesart behalten; nur das Layout unterscheidet sich. */
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


/** Die Leisten-Darstellung: ein Feld je Kennzahl, im Muster der Kostenleiste
 *  darüber (kleines Label, fetter Wert, Beiwerk klein darunter). Umbricht bei
 *  vielen Kennzahlen, statt in die Breite zu laufen.
 *
 *  Die Gegenzahlen stehen am Ende derselben Leiste und nicht in einem eigenen
 *  Kasten: wer die Preise liest, soll ohne einen zweiten Blick sehen, wie viele
 *  Läufe nichts ergaben. */
function PriceStrip({ rep, rows }: { rep: IndicatorReport; rows: NonNullable<IndicatorReport["indicators"]> }) {
  const { t } = useTranslation();
  return (
    <div className="card flex flex-wrap text-sm" style={{ gap: "10px 32px", alignItems: "flex-start" }}>
      {/* Der Zeitraum steht VORNE, weil er alles dahinter beschriftet: direkt
          darüber liegt die Kostenleiste mit den GESAMTkosten des Agenten, und
          30-Tage-Zahlen ohne Kontext daneben laden zur Verwechslung ein.
          Rechtsbündig am Ende rutschte er beim Umbruch allein in eine zweite
          Zeile und machte die Leiste um ein Drittel höher. */}
      <div style={{ minWidth: 96 }}>
        <div className="muted text-xs">{t("costs.indicators.period")}</div>
        <div className="muted">{t("agent.performance.window")}</div>
      </div>
      {rows.map((r) => (
        <div key={r.key} style={{ minWidth: 96 }}>
          <div className="muted text-xs truncate" style={{ maxWidth: 190 }} title={r.action || r.key}>{r.title}</div>
          <div className="font-medium">
            {r.count}
            {r.unit_usd !== undefined ? (
              <span className="muted" style={{ fontWeight: 400 }}> · {t("costs.indicators.perUnit", { price: fmtUSD(r.unit_usd) })}</span>
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

import { useTranslation } from "react-i18next";
import type { AgentPhase } from "../api";
import { fmtBytes, fmtCount, fmtDelta } from "../format";

// PhaseBadge: worauf ein Agent gerade wartet, in einer Zeile.
//
// Der Status sagt „triggered" — was das eine Dreiviertelstunde lang bedeutet,
// sagt erst diese Zeile: das Image wird geholt, 1,2 von 3,4 GB, seit vier
// Minuten. Ohne sie sieht ein langsamer Anschluss aus wie ein Hänger, und beide
// sehen aus wie „läuft schon".
//
// Dieselbe Anzeige neben dem Agenten und auf der Aufgabe, die gerade läuft:
// Es ist derselbe Vorgang, und wer auf die Aufgabe sieht, wartet auf ihn
// genauso.

/** anteil: Länge des Balkens, oder nichts, wenn die Phase ihr Ende nicht kennt.
 *  Ein Balken, der eine Zahl behauptet, die niemand hat, ist schlimmer als
 *  keiner. */
export function anteil(p: AgentPhase): number | undefined {
  if (p.bytes_total && p.bytes !== undefined) return Math.min(1, p.bytes / p.bytes_total);
  if (p.count_total && p.count !== undefined) return Math.min(1, p.count / p.count_total);
  return undefined;
}

/** dauer: wie lange die Phase schon läuft, in Millisekunden. */
export function dauer(p: AgentPhase, jetzt = Date.now()): number {
  const seit = Date.parse(p.since);
  return Number.isNaN(seit) ? 0 : Math.max(0, jetzt - seit);
}

export function phaseZahlen(p: AgentPhase, t: (k: string, o?: Record<string, unknown>) => string): string {
  const teile: string[] = [];
  if (p.bytes !== undefined && p.bytes_total) teile.push(`${fmtBytes(p.bytes)} / ${fmtBytes(p.bytes_total)}`);
  else if (p.bytes) teile.push(fmtBytes(p.bytes));
  if (p.count !== undefined && p.count_total)
    teile.push(t("activity.phase.filesOf", { count: fmtCount(p.count), total: fmtCount(p.count_total) }));
  else if (p.count) teile.push(t("activity.phase.files", { count: fmtCount(p.count) }));
  return teile.join(" · ");
}

export function PhaseBadge({ phase, compact }: { phase: AgentPhase; compact?: boolean }) {
  const { t } = useTranslation();
  const a = anteil(phase);
  const zahlen = phaseZahlen(phase, t);
  const label = t(`activity.phase.${phase.phase}`, phase.phase);
  return (
    <span className={`phase-badge ${compact ? "compact" : ""}`} title={phase.detail || undefined}>
      <span className="phase-label">{label}</span>
      {a !== undefined && <span className="phase-pct">{Math.round(a * 100)} %</span>}
      {!compact && zahlen && <span className="phase-figures">{zahlen}</span>}
      {!compact && <span className="phase-figures">{fmtDelta(dauer(phase))}</span>}
      <span className={`act-bar ${a === undefined ? "unknown" : ""}`}>
        <span style={a === undefined ? undefined : { width: `${Math.round(a * 100)}%` }} />
      </span>
    </span>
  );
}

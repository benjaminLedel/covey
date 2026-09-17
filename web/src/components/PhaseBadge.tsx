import { useTranslation } from "react-i18next";
import type { AgentPhase } from "../api";
import { fmtBytes, fmtCount, fmtDelta } from "../format";

// PhaseBadge: what an agent is waiting for right now, in one line.
//
// The status says `triggered` — what that means for three quarters of an hour
// is what this line says: pulling the image, 1.2 of 3.4 GB, for four minutes.
// Without it a slow connection looks like a hang, and both look like an agent
// that is up and running.
//
// The same readout next to the agent and on the task that is running: it is
// the same process, and whoever looks at the task is waiting for it in exactly
// the same way.

/** anteil: length of the bar, or nothing when the phase does not know its end.
 *  A bar that asserts a number nobody has is worse than
 *  no bar. */
export function anteil(p: AgentPhase): number | undefined {
  if (p.bytes_total && p.bytes !== undefined) return Math.min(1, p.bytes / p.bytes_total);
  if (p.count_total && p.count !== undefined) return Math.min(1, p.count / p.count_total);
  return undefined;
}

/** dauer: how long the phase has been running, in milliseconds. */
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

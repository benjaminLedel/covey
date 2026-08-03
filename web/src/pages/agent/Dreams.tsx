import { useState, useEffect, useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import i18n from "../../i18n";
import {
  api,
  post,
  type Dream,
} from "../../api";

const LOG_PREFIXES = ["neue Seite: ", "ergänzt: ", "gelöscht: ", "bearbeitet: "];

// logDetail entscheidet, ob die Zusammenfassung neben der Seite noch etwas
// beiträgt. Meist lautet sie "<Vorgang>: <Seitentitel>" — dann steht dasselbe
// zweimal in einer Zeile, und genau das machte das Protokoll unlesbar.
export function logDetail(summary: string, pageName: string): string {
  let s = (summary ?? "").trim();
  for (const p of LOG_PREFIXES) {
    if (s.startsWith(p)) {
      s = s.slice(p.length).trim();
      break;
    }
  }
  const n = (pageName ?? "").trim();
  if (!s) return "";
  if (!n) return s;
  const a = s.toLowerCase();
  const b = n.toLowerCase();
  if (a === b || a.endsWith(b) || a.startsWith(b) || b.startsWith(a)) return "";
  return s;
}

// ── Träume ────────────────────────────────────────────────────────────────
// Der Agent räumt sein Gedächtnis nachts auf: verschmelzen, umbenennen. Dieser
// Reiter zeigt, was dabei herauskam — nicht "Wartung gelaufen", sondern welche
// Seite, von welchem Titel auf welchen, und warum. Jede Umbenennung lässt sich
// einzeln zurücknehmen; der Traum schreibt schließlich, während niemand zusieht.
export function Dreams({ agentId, canManage }: { agentId: string; canManage: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  const [note, setNote] = useState("");
  const [open, setOpen] = useState<Set<string>>(new Set());

  const [polling, setPolling] = useState(false);
  const dreams = useQuery({
    queryKey: ["dreams", agentId],
    queryFn: () => api<Dream[] | null>(`/agents/${agentId}/dreams`),
    refetchInterval: polling ? 2000 : false,
  });
  const list = useMemo(() => dreams.data ?? [], [dreams.data]);
  const current = list.find((d) => d.status === "running");
  useEffect(() => setPolling(!!current), [current]);

  // Sekundenzeiger: ein Traum ohne sichtbar laufende Uhr sieht nach einer
  // halben Minute aus wie ein hängender Knopf.
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (!current) return;
    const h = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(h);
  }, [current]);
  const elapsed = useMemo(() => {
    void tick;
    if (!current) return 0;
    return Math.max(0, Math.round((Date.now() - new Date(current.started_at).getTime()) / 1000));
  }, [current, tick]);

  const start = useMutation({
    mutationFn: () => post<Dream>(`/agents/${agentId}/dreams`),
    onSuccess: (d) => {
      setPolling(d.status === "running");
      qc.invalidateQueries({ queryKey: ["dreams", agentId] });
    },
    onError: (e: Error) => setNote(e.message),
  });
  const undo = useMutation({
    mutationFn: (actionID: string) => post<{ ok: boolean }>(`/dream-actions/${actionID}/undo`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["dreams", agentId] });
      qc.invalidateQueries({ queryKey: ["memories", agentId] });
      qc.invalidateQueries({ queryKey: ["wiki-health", agentId] });
    },
    onError: (e: Error) => setNote(e.message),
  });

  const dayLabel = (iso: string) => {
    const d = new Date(iso);
    const today = new Date().toDateString();
    const yest = new Date(Date.now() - 86400000).toDateString();
    const time = d.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" });
    if (d.toDateString() === today) return `${t("agent.dreams.today")}, ${time}`;
    if (d.toDateString() === yest) return `${t("agent.dreams.yesterday")}, ${time}`;
    return `${d.toLocaleDateString(locale, { day: "2-digit", month: "long" })}, ${time}`;
  };
  const duration = (d: Dream) => {
    if (!d.finished_at) return "";
    const s = Math.max(0, Math.round((new Date(d.finished_at).getTime() - new Date(d.started_at).getTime()) / 1000));
    return t("agent.dreams.took", { s });
  };

  return (
    <div>
      <p className="muted text-[12.5px] mb-3">{t("agent.dreams.hint")}</p>
      <div className="flex items-center gap-2 mb-3">
        {current ? (
          <span className="wiki-progress">
            <span className="spin" aria-hidden="true" />
            <span>{current.phase === "titles" ? t("agent.dreams.phaseTitles", { count: current.looked_at }) : t("agent.dreams.phaseMerge")}</span>
            <span className="muted">{t("agent.dreams.elapsed", { s: elapsed })}</span>
          </span>
        ) : (
          canManage && (
            <button className="btn sm" disabled={start.isPending} onClick={() => start.mutate()}>
              {t("agent.dreams.start")}
            </button>
          )
        )}
      </div>
      {note && <p className="danger-text text-xs mb-2">{note}</p>}

      {list.length === 0 && !dreams.isLoading && <p className="muted text-[12.5px]">{t("agent.dreams.empty")}</p>}

      {list.map((d) => {
        const renames = d.actions.filter((a) => a.kind === "retitle");
        const merges = d.actions.filter((a) => a.kind === "merge").length;
        const isOpen = open.has(d.id);
        return (
          <div key={d.id} className="card wiki-titles mb-3">
            <div className="wiki-titles-h">
              <span className="font-medium">{dayLabel(d.started_at)}</span>
              <span className="chip is-fixed" style={{ fontSize: "10px" }}>
                {d.trigger === "nightly" ? t("agent.dreams.nightly") : t("agent.dreams.manual")}
              </span>
              <span className="muted">
                {[
                  renames.length > 0 ? t("agent.dreams.renamed", { count: renames.length }) : null,
                  merges > 0 ? t("agent.dreams.merged", { count: merges }) : null,
                  d.skipped > 0 ? t("agent.dreams.skipped", { count: d.skipped }) : null,
                  d.status === "done" && renames.length === 0 && merges === 0 ? t("agent.dreams.quiet") : null,
                ]
                  .filter(Boolean)
                  .join(" · ")}
              </span>
              <span className="flex-1" />
              <span className="muted text-[11px]">{duration(d)}</span>
              {renames.length > 0 && (
                <button
                  className="btn sm"
                  onClick={() =>
                    setOpen((prev) => {
                      const n = new Set(prev);
                      if (n.has(d.id)) n.delete(d.id);
                      else n.add(d.id);
                      return n;
                    })
                  }
                >
                  {isOpen ? t("agent.dreams.collapse") : t("agent.dreams.expand")}
                </button>
              )}
            </div>
            {d.status === "error" && d.error && <p className="danger-text text-xs">{d.error}</p>}
            {/* Die Erzählung ist erfunden — das Protokoll darunter ist es nicht.
                Deshalb sichtbar abgesetzt und als Traum ausgewiesen. */}
            {d.story && (
              <p className="dream-story">
                <span className="mark" aria-hidden="true">
                  ☾
                </span>
                {d.story}
              </p>
            )}
            {isOpen &&
              renames.map((a) => (
                <div key={a.id} className="wiki-title-row">
                  <span className="min-w-0">
                    <span className="old" title={a.before}>
                      {a.before}
                    </span>
                    <span className="new">{a.after}</span>
                    {a.reason && <span className="why">{a.reason}</span>}
                  </span>
                  <span className="flex items-center gap-2 shrink-0">
                    {a.undone_at ? (
                      <span className="muted text-[11px]">{t("agent.dreams.undone")}</span>
                    ) : (
                      <button className="btn sm" disabled={undo.isPending} onClick={() => undo.mutate(a.id)}>
                        {t("agent.dreams.undo")}
                      </button>
                    )}
                  </span>
                </div>
              ))}
          </div>
        );
      })}
    </div>
  );
}

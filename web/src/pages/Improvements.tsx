import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { api, decideImprovement, type ImprovementItem, type Principal } from "../api";
import { Markdown } from "../components/Markdown";
import { collapse, diffLines } from "../diff";
import { canManage } from "./agent/roles";

/* Die offenen Punkte aus dem Betrieb (spec/21).
   Diese Liste IST der Kanal. Ein Review endet in einer von drei Diagnosen —
   die Config ist falsch (Vorschlag mit Diff), der Auftrag ist falsch (Befund,
   den nur ein Mensch umsetzen kann) oder die Plattform ist falsch (Issue, das
   schon im Tracker liegt) —, und alle drei brauchen dieselbe Person. Sie
   nebenher zu verschicken hiesse, einen zweiten Posteingang neben den zu
   stellen, den es zum Annehmen ohnehin geben muss; ein Befund, den nur ein
   Mensch bearbeiten kann, ist hier deshalb keine Nachricht, die untergehen
   kann, sondern ein Punkt, der offen bleibt.

   Angenommen wird nichts von selbst: der Vorschlag wird erst durch das
   Klicken eines Menschen zu einer laufenden Config. */

export default function Improvements({ me }: { me: Principal }) {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const [showDecided, setShowDecided] = useState(false);
  const [mine, setMine] = useState(false);

  const items = useQuery({
    queryKey: ["improvements", mine ? "mine" : "all"],
    queryFn: () => api<ImprovementItem[] | null>(`/improvements${mine ? "?mine=1" : ""}`),
    refetchInterval: 30000,
  });

  const list = items.data ?? [];
  const open = list.filter((i) => i.status === "pending");
  const decided = list.filter((i) => i.status !== "pending");

  // Gruppiert nach dem Kollegen, um den es geht — das ist die Einheit, in der
  // ein Mensch liest ("was liegt bei meinem QA-Agenten an").
  const groups = new Map<string, ImprovementItem[]>();
  for (const item of open) {
    const key = item.agent_id;
    groups.set(key, [...(groups.get(key) ?? []), item]);
  }

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-1">
        <h1 className="text-[22px]">{t("improvements.title")}</h1>
        <span className="muted">{t("improvements.open", { count: open.length })}</span>
        <label className="muted text-xs flex items-center gap-1 ml-auto">
          <input type="checkbox" checked={mine} onChange={(e) => setMine(e.target.checked)} />
          {t("improvements.onlyMine")}
        </label>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 720 }}>
        {t("improvements.hint")}
      </p>

      {items.isError && <p className="danger-text">{(items.error as Error).message}</p>}
      {!items.isLoading && open.length === 0 && <p className="muted">{t("improvements.noOpen")}</p>}

      {[...groups.entries()].map(([agentID, group]) => (
        <section key={agentID} className="mb-5">
          <h2 className="text-base mb-2">
            <Link to={`/agents/${agentID}`}>{group[0].agent_name}</Link>{" "}
            <span className="muted mono text-xs">{group[0].agent_slug}</span>
          </h2>
          {group.map((item) => (
            <ItemCard key={item.id} item={item} me={me} onDecided={() => qc.invalidateQueries({ queryKey: ["improvements"] })} />
          ))}
        </section>
      ))}

      {decided.length > 0 && (
        <>
          <button className="btn sm mt-4" onClick={() => setShowDecided((v) => !v)}>
            {t("improvements.decided", { count: decided.length })}
          </button>
          {showDecided &&
            decided.map((item) => (
              <div key={item.id} className="card mb-2 mt-2 flex items-center gap-3" style={{ padding: "10px 15px" }}>
                <span className={`badge st-${item.status === "accepted" ? "done" : "denied"}`}>
                  {t(`improvements.status.${item.status}`)}
                </span>
                <span className="flex-1 min-w-0 truncate text-sm">{item.title}</span>
                <span className="muted text-xs mono">{item.agent_slug}</span>
                <span className="muted text-xs">
                  {item.decided_at && new Date(item.decided_at).toLocaleDateString(i18n.language === "de" ? "de-DE" : "en-US")}
                </span>
              </div>
            ))}
        </>
      )}
    </div>
  );
}

function ItemCard({ item, me, onDecided }: { item: ImprovementItem; me: Principal; onDecided: () => void }) {
  const { t, i18n } = useTranslation();
  const [note, setNote] = useState("");
  const [openDiff, setOpenDiff] = useState(false);
  const decide = useMutation({
    mutationFn: (accept: boolean) => decideImprovement(item.id, accept, note),
    onSuccess: onDecided,
  });

  const konflikt = (item.conflicts?.length ?? 0) > 0;
  // Wer entscheiden darf: die Tiefe des Vorschlags bestimmt es, nicht der
  // Klick. Fasst er ACCESS.md oder EGRESS.md an, entscheidet Security.
  const darfEntscheiden = canManage(me.Role) || me.Role === "security";
  const darfAnnehmen =
    darfEntscheiden &&
    !konflikt &&
    (!item.needs_security || me.Role === "platform_admin" || me.Role === "security");

  return (
    <div className="card mb-2">
      <div className="flex items-baseline gap-2 mb-1 flex-wrap">
        <span className={`badge kind-${item.kind}`}>{t(`improvements.kind.${item.kind}`)}</span>
        <strong className="text-sm">{item.title}</strong>
        <span className="muted text-xs ml-auto">
          {item.author_name && t("improvements.by", { name: item.author_name })}{" "}
          {new Date(item.created_at).toLocaleDateString(i18n.language === "de" ? "de-DE" : "en-US")}
        </span>
      </div>

      {item.rationale && (
        <div className="text-sm mb-2" style={{ maxWidth: 780 }}>
          <Markdown text={item.rationale} />
        </div>
      )}

      <div className="flex items-center gap-2 flex-wrap mb-2">
        {item.needs_security && <span className="badge st-pending">{t("improvements.needsSecurity")}</span>}
        {konflikt && (
          <span className="badge st-failed">
            {t("improvements.conflict", { files: item.conflicts!.join(", ") })}
          </span>
        )}
        {!konflikt && item.stale && (
          <span className="muted text-xs">
            {t("improvements.stale", { base: item.base_version, current: item.current_version })}
          </span>
        )}
      </div>

      {item.diff && item.diff.length > 0 && (
        <>
          <button className="assist-toggle mb-1" aria-expanded={openDiff} onClick={() => setOpenDiff((v) => !v)}>
            <span className="caret">▶</span>
            {t("improvements.showDiff", { files: item.diff.map((d) => d.file).join(", ") })}
          </button>
          {openDiff && item.diff.map((d) => <FileDiff key={d.file} file={d.file} before={d.before} after={d.after} />)}
        </>
      )}

      {darfEntscheiden && (
        <div className="flex items-center gap-2 mt-3 flex-wrap">
          <input
            placeholder={t("improvements.notePlaceholder")}
            value={note}
            onChange={(e) => setNote(e.target.value)}
            style={{ width: 260 }}
          />
          <button
            className="btn sm primary"
            disabled={!darfAnnehmen || decide.isPending}
            title={darfAnnehmen ? undefined : t(konflikt ? "improvements.blockedConflict" : "improvements.blockedSecurity")}
            onClick={() => decide.mutate(true)}
          >
            {item.kind === "proposal" ? t("improvements.accept") : t("improvements.done")}
          </button>
          <button className="btn sm danger" disabled={decide.isPending} onClick={() => decide.mutate(false)}>
            {t("improvements.reject")}
          </button>
          {decide.isError && <span className="danger-text text-xs">{(decide.error as Error).message}</span>}
        </div>
      )}
    </div>
  );
}

// FileDiff zeigt die geänderte Datei zeilenweise gegen den LAUFENDEN Stand —
// beurteilt wird die Änderung, die durch das Annehmen entsteht.
function FileDiff({ file, before, after }: { file: string; before: string; after: string }) {
  const { t } = useTranslation();
  const chunks = collapse(diffLines(before, after));
  return (
    <div className="diff mb-2">
      <div className="diff-head mono">
        {file}
        {before === "" && <span className="muted"> · {t("improvements.newFile")}</span>}
      </div>
      <pre className="diff-body">
        {chunks.map((c, i) =>
          c.kind === "skip" ? (
            <span key={i} className="diff-skip">
              {t("improvements.skipped", { count: c.skipped })}
            </span>
          ) : (
            <span key={i} className={`diff-line ${c.kind}`}>
              {c.kind === "add" ? "+" : c.kind === "del" ? "−" : " "} {c.text}
            </span>
          ),
        )}
      </pre>
    </div>
  );
}

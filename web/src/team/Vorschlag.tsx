import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { decideImprovement, type ProposalCard } from "../api";
import { FileDiff } from "../components/FileDiff";
import { Markdown } from "../components/Markdown";

/* A configuration change drafted from the chat (#491), as a card in the
 * conversation: what changes, file by file, and why; a line when it widens
 * access; Accept and Decline for whoever may decide, "waits for …" for
 * everybody else; and once decided, who and when.
 *
 * The decision goes through the agent page's path (/improvements/{id}/
 * decide), so that accepting here is accepting there. Whether the reader
 * may decide the server says (can_decide, can_accept) — the card keeps no
 * table of roles of its own. */
export default function Vorschlag({ card }: { card: ProposalCard }) {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const [offen, setOffen] = useState(false);
  const entscheiden = useMutation({
    mutationFn: (accept: boolean) => decideImprovement(card.id, accept, ""),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["thread"] });
      qc.invalidateQueries({ queryKey: ["conversation"] });
      qc.invalidateQueries({ queryKey: ["inbox"] });
      qc.invalidateQueries({ queryKey: ["agent"] });
    },
  });
  const wann = card.decided_at
    ? new Date(card.decided_at).toLocaleString(i18n.language, { dateStyle: "medium", timeStyle: "short" })
    : "";
  const wer = card.approvers.join(", ");
  const widens = card.widens ?? [];
  const konflikt = (card.conflicts ?? []).length > 0;

  return (
    <section className={`tm-vorschlag st-${card.status}`} aria-label={t("chatProposal.label")}>
      <div className="tm-vorschlag-kopf">
        <span className="tm-vorschlag-art">{t("chatProposal.label")}</span>
        <strong className="tm-vorschlag-titel">{card.title}</strong>
      </div>
      {card.rationale && (
        <div className="tm-vorschlag-text">
          <Markdown text={card.rationale} />
        </div>
      )}
      {widens.length > 0 && (
        <p className="tm-vorschlag-warnung" role="note">
          {t("chatProposal.widens", { files: widens.join(", ") })}
        </p>
      )}
      {konflikt && card.status === "pending" && (
        <p className="tm-vorschlag-warnung">{t("improvements.conflict", { files: card.conflicts!.join(", ") })}</p>
      )}
      {card.diff.length > 0 && (
        <>
          <button className="assist-toggle" aria-expanded={offen} onClick={() => setOffen((v) => !v)}>
            <span className="caret">▶</span>
            {t("improvements.showDiff", { files: card.diff.map((d) => d.file).join(", ") })}
          </button>
          {offen && card.diff.map((d) => <FileDiff key={d.file} file={d.file} before={d.before} after={d.after} />)}
        </>
      )}

      {card.status === "pending" ? (
        card.can_decide ? (
          <div className="tm-vorschlag-knoepfe">
            <button
              className="btn primary sm"
              disabled={!card.can_accept || entscheiden.isPending}
              title={card.can_accept ? undefined : t(konflikt ? "improvements.blockedConflict" : "improvements.blockedSecurity")}
              onClick={() => entscheiden.mutate(true)}
            >
              {t("chatProposal.accept")}
            </button>
            <button className="btn sm" disabled={entscheiden.isPending} onClick={() => entscheiden.mutate(false)}>
              {t("chatProposal.decline")}
            </button>
            {entscheiden.isError && <span className="danger-text text-xs">{(entscheiden.error as Error).message}</span>}
          </div>
        ) : (
          <p className="tm-vorschlag-stand">{wer ? t("chatProposal.waitingFor", { names: wer }) : t("chatProposal.waiting")}</p>
        )
      ) : (
        <p className="tm-vorschlag-stand">
          {t(card.status === "accepted" ? "chatProposal.acceptedBy" : "chatProposal.declinedBy", {
            name: card.decided_by || "…",
            when: wann,
          })}
        </p>
      )}
    </section>
  );
}

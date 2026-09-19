import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { inbox, type InboxEntry, type Principal } from "../api";

/* Was wartet — die Startseite des Workspace.
 *
 * Sie steht hier und nicht als Reiter neben den Verläufen, weil sie der Grund
 * ist, warum es den Workspace gibt: Ein Agent, der stehenbleibt und fragt,
 * kostet nichts zu halten und alles, zu lange zu halten. Wer sich anmeldet,
 * soll zuerst sehen, ob jemand auf ihn wartet.
 *
 * Entschieden wird nicht hier. Eine Freigabe braucht, was der Posteingang der
 * Konsole zeigt — die Aktion im Wortlaut, die Regel, die sie hält —, und eine
 * Frage wird dort beantwortet, wo sie steht: im Verlauf. Diese Seite führt
 * hin, sie ersetzt nichts.
 */

export default function Wartet({ me }: { me: Principal }) {
  const { t } = useTranslation();

  const offen = useQuery({
    queryKey: ["inbox", "workspace"],
    queryFn: () => inbox({ status: "open", sort: "urgent", limit: 25 }),
    refetchInterval: 30_000,
  });

  const items = offen.data?.items ?? [];

  return (
    <div className="tm-wartet-seite">
      <header className="tm-wartet-kopf">
        <h1>{t("team.wartetTitel", { name: me.DisplayName || me.Email })}</h1>
        <p className="tm-leise">
          {items.length === 0 ? t("team.wartetNichts") : t("team.wartetLead", { count: items.length })}
        </p>
      </header>

      {offen.isLoading && <p className="tm-leise">{t("common.loading")}</p>}

      <ul className="tm-wartet-liste">
        {items.map((e: InboxEntry) => (
          <li key={`${e.type}-${e.id}`}>
            <Link to={e.type === "approval" ? "/inbox" : `/team/${e.agent_id}`} className="tm-wartet-zeile">
              <span className={`tm-art a-${e.type}`}>{t(`team.art.${e.type}`)}</span>
              <span className="tm-wartet-titel">{e.title}</span>
              <span className="tm-wartet-wer">{e.agent_name}</span>
              <time className="tm-leise" dateTime={e.created_at}>
                {new Date(e.created_at).toLocaleString()}
              </time>
            </Link>
          </li>
        ))}
      </ul>

      {items.length > 0 && (
        <Link to="/inbox" className="tm-wartet-alle">
          {t("team.alleOffenen")}
        </Link>
      )}
    </div>
  );
}

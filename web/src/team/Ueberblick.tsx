import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { api, inbox, type Agent, type InboxEntry, type Principal } from "../api";
import Gesicht from "../components/Gesicht";

/* Der Überblick — die Seite, auf der jeder landet.
 *
 * Sie hieß zuerst „Wartet auf Sie" und war leer, sobald nichts wartete, und
 * damit war sie an den meisten Tagen eine leere Seite mit einem Satz darauf.
 * Das ist der falsche erste Eindruck für die Oberfläche, die man morgens als
 * erstes öffnet: Sie soll sagen, wie die Belegschaft dasteht, nicht nur, ob
 * sie etwas von einem will.
 *
 * Drei Blöcke, und jeder darf fehlen:
 *
 *   Wartet auf Sie   — die offenen Punkte. Nur, wenn es welche gibt.
 *   Arbeitet gerade  — wer läuft, mit Gesicht. Die Zeile, die zeigt, dass
 *                      hier etwas passiert, auch wenn niemand etwas will.
 *   Ihre Kollegen    — wenn beides leer ist: kein „nichts", sondern der
 *                      Anfang eines Gesprächs.
 *
 * Alles daran kommt aus zwei Abfragen, die die Schale ohnehin macht. Eine
 * Startseite, die eine eigene Abfrage braucht, wäre eine Startseite, die den
 * Server bei jedem Anmelden zusätzlich belastet.
 */

export default function Ueberblick({ me }: { me: Principal }) {
  const { t } = useTranslation();

  const offen = useQuery({
    queryKey: ["inbox", "workspace-liste"],
    queryFn: () => inbox({ status: "open", sort: "urgent", limit: 100 }),
    refetchInterval: 30_000,
  });
  const agents = useQuery({
    queryKey: ["agents"],
    queryFn: () => api<Agent[] | null>("/agents"),
    staleTime: 60_000,
  });

  const items = (offen.data?.items ?? []).slice(0, 12);
  const alle = (agents.data ?? []).filter((a) => a.status !== "applicant");
  const arbeiten = alle.filter((a) => !a.killed && a.status !== "sleeping");
  /* Wenn nichts wartet und niemand arbeitet, sind die Kollegen selbst der
     Inhalt — die ersten acht, damit die Seite nicht zur zweiten Liste wird. */
  const vorschlag = alle.slice(0, 8);

  const name = (me.DisplayName || me.Email).split(/\s+/)[0];

  return (
    <div className="tm-ueberblick">
      <header className="tm-ueberblick-kopf">
        <h1>{t("team.wartetTitel", { name })}</h1>
        <p className="tm-leise">
          {items.length > 0
            ? t("team.wartetLead", { count: offen.data?.pending ?? items.length })
            : arbeiten.length > 0
              ? t("team.ueberblickArbeiten", { count: arbeiten.length })
              : t("team.wartetNichts")}
        </p>
      </header>

      {items.length > 0 && (
        <section className="tm-block">
          <h2>{t("team.wartet")}</h2>
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
          {(offen.data?.pending ?? 0) > items.length && (
            <Link to="/inbox" className="tm-wartet-alle">
              {t("team.alleOffenen")}
            </Link>
          )}
        </section>
      )}

      {arbeiten.length > 0 && (
        <section className="tm-block">
          <h2>{t("team.arbeitenJetzt")}</h2>
          <div className="tm-riege">
            {arbeiten.map((a) => (
              <Link key={a.id} to={`/team/${a.id}`} className="tm-riege-wer">
                <Gesicht schluessel={a.slug} zustand="working" groesse={34} />
                <span className="tm-riege-name">{a.display_name}</span>
                <span className="tm-riege-rolle">{a.job_title || a.slug}</span>
              </Link>
            ))}
          </div>
        </section>
      )}

      {items.length === 0 && arbeiten.length === 0 && vorschlag.length > 0 && (
        <section className="tm-block">
          <h2>{t("team.kollegen")}</h2>
          <p className="tm-leise tm-block-lead">{t("team.ueberblickStill")}</p>
          <div className="tm-riege">
            {vorschlag.map((a) => (
              <Link key={a.id} to={`/team/${a.id}`} className="tm-riege-wer">
                <Gesicht
                  schluessel={a.slug}
                  zustand={a.killed ? "killed" : a.status === "sleeping" ? "sleeping" : "working"}
                  groesse={34}
                />
                <span className="tm-riege-name">{a.display_name}</span>
                <span className="tm-riege-rolle">{a.job_title || a.slug}</span>
              </Link>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

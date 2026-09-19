import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { useEffect, useState } from "react";
import { api, inbox, type Agent, type Department, type InboxEntry, type Laufend, type Principal } from "../api";
import Gesicht from "../components/Gesicht";
import Buero from "./Buero";

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
  /* Was gerade läuft. Zehn Sekunden, wie der Verlauf: Diese Zeile ist der
     Grund, warum die Seite nicht nur beim Öffnen etwas sagt. */
  const abteilungen = useQuery({
    queryKey: ["departments"],
    queryFn: () => api<Department[] | null>("/departments"),
    staleTime: 300_000,
  });
  const laufend = useQuery({
    queryKey: ["org-running"],
    queryFn: () => api<Laufend[] | null>("/org/running"),
    refetchInterval: 10_000,
  });

  const items = (offen.data?.items ?? []).slice(0, 12);
  const alle = (agents.data ?? []).filter((a) => a.status !== "applicant");
  const laeuft = laufend.data ?? [];
  /* „Arbeitet" heißt: hat einen laufenden Vorgang. Der Status allein sagte
     nur, dass der Agent wach ist — und ein wacher Agent ohne Aufgabe ist
     keine Auskunft, sondern ein Zustand. */
  const arbeiten = alle.filter((a) => !a.killed && a.status !== "sleeping" && !laeuft.some((l) => l.agent_id === a.id));

  const name = (me.DisplayName || me.Email).split(/\s+/)[0];

  return (
    <div className="tm-ueberblick">
      <header className="tm-ueberblick-kopf">
        <h1>{t("team.wartetTitel", { name })}</h1>
        <p className="tm-leise">
          {items.length > 0
            ? t("team.wartetLead", { count: offen.data?.pending ?? items.length })
            : laeuft.length > 0
              ? t("team.ueberblickLaeuft", { count: laeuft.length })
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

      {/* Woran gerade gearbeitet wird — mit Vorgang, Dauer und dem zuletzt
          aufgezeichneten Schritt. Die einzige Zeile dieser Seite, die sich
          ändert, während man sie ansieht. */}
      {laeuft.length > 0 && (
        <section className="tm-block">
          <h2>{t("team.laeuftGerade")}</h2>
          <ul className="tm-laeuft">
            {laeuft.map((l) => (
              <li key={l.task_id}>
                <Link to={`/team/${l.agent_id}`} className="tm-laeuft-zeile">
                  <Gesicht schluessel={l.agent_slug} zustand="working" groesse={30} />
                  <span className="tm-laeuft-wer">
                    <span className="tm-laeuft-titel">{l.title}</span>
                    <span className="tm-laeuft-unten">
                      {l.agent_name}
                      {l.step && <> · {t(`team.schritt.${l.step}`, l.step)}</>}
                    </span>
                  </span>
                  <Dauer seit={l.since} />
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}

      {/* Der Grundriss. Er zeigt dieselbe Auskunft wie die Listen darüber,
          nur als Raum — und er ist der Teil dieser Seite, den man ansieht,
          weil sich etwas bewegt, und nicht, weil man etwas sucht.

          Ohne Überschrift: Die Seite heißt „Büro", und ein Grundriss mit
          „Das Büro" darüber sagte dasselbe ein zweites Mal. */}
      <section className="tm-block tm-block-weit">
        <Buero
          agents={alle}
          departments={abteilungen.data ?? []}
          laufend={laeuft}
          wartetBei={new Set((offen.data?.items ?? []).map((e) => e.agent_id))}
        />
      </section>

    </div>
  );
}

/* Die Dauer zählt im Browser weiter.
 *
 * Vom Server käme sie als Zahl, die in dem Moment falsch ist, in dem sie
 * ankommt — und eine Dauer, die erst beim nächsten Abruf springt, sagt das
 * Gegenteil dessen, was sie zeigen soll: dass hier gerade etwas passiert. */
function Dauer({ seit }: { seit: string }) {
  const [jetzt, setJetzt] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setJetzt(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  const s = Math.max(0, Math.floor((jetzt - new Date(seit).getTime()) / 1000));
  const text =
    s < 60 ? `${s}s` : s < 3600 ? `${Math.floor(s / 60)}m ${s % 60}s` : `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
  return (
    <time className="tm-laeuft-dauer" dateTime={seit}>
      {text}
    </time>
  );
}

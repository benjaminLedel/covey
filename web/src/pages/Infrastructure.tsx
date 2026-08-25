import { useTranslation } from "react-i18next";
import { NavLink, Route, Routes } from "react-router";
import type { Principal } from "../api";
import Runners from "./Runners";
import RunnerDetail from "./RunnerDetail";
import Runtimes from "./Runtimes";
import Workplaces from "./Workplaces";

/* Womit, worin, wo.
 *
 * Drei Seiten standen nebeneinander in der Navigation und beantworteten
 * dieselbe Frage in drei Teilen: Die Runtime ist, womit ein Agent denkt; der
 * Arbeitsplatz das Image, worin er arbeitet; der Runner die Maschine, auf der
 * das startet. Wer eines davon einrichtet, hat fast immer eine Frage an die
 * anderen beiden — „läuft mein dev-Image auf dem Runner, der die GPU hat" ist
 * eine Frage über alle drei, und sie war über drei Menüpunkte verteilt.
 *
 * Die Reihenfolge der Reiter ist die Erzählung: erst der Kopf, dann der
 * Schreibtisch, dann das Gebäude.
 *
 * Die alten Adressen bleiben gültig — verlinkt und gebookmarkt wurde alles
 * drei (siehe App.tsx). */
export default function Infrastructure({ me }: { me: Principal }) {
  return (
    <Routes>
      <Route index element={<Tab me={me} which="runtimes" />} />
      <Route path="workplaces" element={<Tab me={me} which="workplaces" />} />
      <Route path="runners" element={<Tab me={me} which="runners" />} />
      {/* Der Host selbst hat eine eigene Seite: was er kann, ist eine
          Entscheidung mit Begründung, und die passt nicht in eine Spalte. */}
      <Route path="runners/:id" element={<RunnerDetail me={me} />} />
    </Routes>
  );
}

function Tab({ me, which }: { me: Principal; which: "runtimes" | "workplaces" | "runners" }) {
  const { t } = useTranslation();
  return (
    <>
      <div className="flex items-baseline gap-3 mb-1">
        <h1 className="text-[22px]">{t("infra.title")}</h1>
        <span className="muted">{t("infra.subtitle")}</span>
      </div>
      <nav className="subnav">
        <NavLink to="/infrastructure" end className={({ isActive }) => (isActive ? "active" : "")}>
          {t("infra.tabRuntimes")}
        </NavLink>
        <NavLink to="/infrastructure/workplaces" className={({ isActive }) => (isActive ? "active" : "")}>
          {t("infra.tabWorkplaces")}
        </NavLink>
        <NavLink to="/infrastructure/runners" className={({ isActive }) => (isActive ? "active" : "")}>
          {t("infra.tabRunners")}
        </NavLink>
      </nav>
      {/* Die drei Seiten bleiben, was sie waren — sie bringen ihre eigene
          Überschrift mit, die hier die Zeile unter dem Reiter füllt. */}
      {which === "runtimes" && <Runtimes me={me} embedded />}
      {which === "workplaces" && <Workplaces me={me} embedded />}
      {which === "runners" && <Runners me={me} embedded />}
    </>
  );
}

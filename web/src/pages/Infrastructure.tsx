import { useTranslation } from "react-i18next";
import { NavLink, Route, Routes } from "react-router";
import type { Principal } from "../api";
import Runners from "./Runners";
import RunnerDetail from "./RunnerDetail";
import Runtimes from "./Runtimes";
import Workplaces from "./Workplaces";

/* With what, in what, where.
 *
 * Three pages stood next to each other in the navigation and answered the
 * same question in three parts: the runtime is what an agent thinks with; the
 * workplace the image it works in; the runner the machine where that starts.
 * Whoever sets up one of them almost always has a question for the other
 * two — "does my dev-image run on the runner that has the GPU" is a question
 * over all three, and it was spread over three menu entries.
 *
 * The order of the tabs is the story: first the head, then the desk, then the
 * building.
 *
 * The old addresses stay valid — all three were linked and bookmarked
 * (see App.tsx). */
export default function Infrastructure({ me }: { me: Principal }) {
  return (
    <Routes>
      <Route index element={<Tab me={me} which="runtimes" />} />
      <Route path="workplaces" element={<Tab me={me} which="workplaces" />} />
      <Route path="runners" element={<Tab me={me} which="runners" />} />
      {/* The host itself has its own page: what it can do is a decision with
          a reason, and that does not fit into a column. */}
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
      {/* The three pages stay what they were — they bring their own heading,
          which here fills the line under the tab. */}
      {which === "runtimes" && <Runtimes me={me} embedded />}
      {which === "workplaces" && <Workplaces me={me} embedded />}
      {which === "runners" && <Runners me={me} embedded />}
    </>
  );
}

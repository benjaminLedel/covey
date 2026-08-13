import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { NavLink, Route, Routes } from "react-router";
import { api, type Agent, type Human, type OrgCostReport, type Organization, type Principal } from "../api";
import Audit from "./Audit";
import { CompanyDescription, PlatformRepo } from "./Org";
import { ProfileFieldsSettings } from "./Organizations";
import Users from "./Users";

// Das Administrations-Panel: DIESE Organisation, nicht die Installation.
//
// Die Trennlinie zum Plattform-Panel ist eine Frage, keine Rollenliste: gilt
// das nur für den Mandanten, in dem ich gerade arbeite? Dann steht es hier —
// und die Rolle, die es öffnet, vergibt die Organisation selbst (org_admin).
//
// Bewusst schlank. Secrets, Zielsysteme, Skills, Vorlagen, Runtimes,
// Guard-Rails und Egress bleiben in der Hauptnavigation: das ist tägliche
// Arbeit an der Belegschaft und keine Verwaltung der Organisation.
export default function Administration({ me }: { me: Principal }) {
  return (
    <Routes>
      <Route index element={<Profile />} />
      <Route path="members" element={<Members me={me} />} />
      <Route path="usage" element={<Usage />} />
      <Route path="audit" element={<AuditTab />} />
    </Routes>
  );
}

function Header() {
  const { t } = useTranslation();
  const own = useQuery({ queryKey: ["own-org"], queryFn: () => api<Organization>("/org") });
  return (
    <>
      <div className="flex items-baseline gap-3 mb-1">
        <h1 className="text-[22px]">{t("administration.title")}</h1>
        <span className="muted">{own.data?.name ?? t("administration.subtitle")}</span>
      </div>
      <nav className="subnav">
        <NavLink to="/administration" end className={({ isActive }) => (isActive ? "active" : "")}>
          {t("administration.tabProfile")}
        </NavLink>
        <NavLink to="/administration/members" className={({ isActive }) => (isActive ? "active" : "")}>
          {t("administration.tabMembers")}
        </NavLink>
        <NavLink to="/administration/usage" className={({ isActive }) => (isActive ? "active" : "")}>
          {t("administration.tabUsage")}
        </NavLink>
        <NavLink to="/administration/audit" className={({ isActive }) => (isActive ? "active" : "")}>
          {t("administration.tabAudit")}
        </NavLink>
      </nav>
    </>
  );
}

/* Die Stammdaten der Organisation.
 *
 * Dieselben Karten stehen auch am Org-Chart, und das ist Absicht: der
 * Beschreibungstext gehört dorthin, wo man ihn beim Lesen des Organigramms vor
 * Augen hat. Hier stehen sie, weil jemand, der die Organisation verwaltet, sie
 * dort sucht. Ein Speicher, zwei Wege — dieselbe Komponente, kein zweiter
 * Editor (dasselbe Muster wie ACCESS.md als Textansicht auf den UI-Store). */
function Profile() {
  const { t } = useTranslation();
  return (
    <div>
      <Header />
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        {t("administration.profileDesc")}
      </p>
      <CompanyDescription />
      <PlatformRepo />
      <ProfileFieldsSettings />
    </div>
  );
}

function Members({ me }: { me: Principal }) {
  return (
    <div>
      <Header />
      <Users me={me} embedded />
    </div>
  );
}

function AuditTab() {
  return (
    <div>
      <Header />
      <Audit embedded />
    </div>
  );
}

/* Was diese Organisation verbraucht.
 *
 * Die Kontingente aus FR-002 P6 gibt es noch nicht — was es gibt, sind die
 * Zahlen, gegen die sie einmal geprüft werden. Sie hier zu zeigen ist kein
 * Platzhalter: "wie viele Agenten laufen, was haben sie gekostet" ist die
 * Frage, die ein Org-Admin am Monatsende hat, und sie stand bisher über drei
 * Seiten verteilt. */
function Usage() {
  const { t } = useTranslation();
  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[]>("/agents") });
  const users = useQuery({ queryKey: ["users"], queryFn: () => api<Human[]>("/users"), retry: false });
  const cost = useQuery({
    queryKey: ["cost", "org", "30d"],
    queryFn: () => api<OrgCostReport>("/cost/org?days=30"),
  });

  // "Nicht schlafend" ist die ehrlichste Definition von beschäftigt, die die
  // Statusspalte hergibt: triggered, triage, working. killed zählt nicht mit —
  // ein gestoppter Agent verbraucht nichts.
  const beschaeftigt = (agents.data ?? []).filter(
    (a) => a.status !== "sleeping" && a.status !== "killed",
  ).length;

  return (
    <div>
      <Header />
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        {t("administration.usageDesc")}
      </p>
      <div className="stat-grid mb-6">
        <div className="card stat">
          <div className="v">{users.data?.length ?? "–"}</div>
          <div className="l">{t("administration.members")}</div>
        </div>
        <div className="card stat">
          <div className="v">{agents.data?.length ?? "–"}</div>
          <div className="l">{t("administration.agents")}</div>
        </div>
        <div className="card stat">
          <div className="v">{agents.data ? beschaeftigt : "–"}</div>
          <div className="l">{t("administration.agentsActive")}</div>
        </div>
        <div className="card stat">
          <div className="v">
            {cost.data ? `$${cost.data.total_usd.toFixed(2)}` : "–"}
          </div>
          <div className="l">{t("administration.cost30d")}</div>
        </div>
      </div>
    </div>
  );
}

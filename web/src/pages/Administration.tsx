import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { NavLink, Route, Routes } from "react-router";
import { api, type Agent, type Human, type OrgCostReport, type Organization, type Principal } from "../api";
import Audit from "./Audit";
import Diagnostics from "./Diagnostics";
import { CompanyDescription, OfficeFurnishing, PlatformRepo, RecordingSettings, TeamSurfaceSettings, TriageSettings } from "./Org";
import { ProfileFieldsSettings } from "./Organizations";
import Users from "./Users";
import { fmtUSD } from "../format";

// The administration panel: THIS organisation, not the installation.
//
// The dividing line to the platform panel is a question, not a role list: does
// this apply only to the tenant I am working in right now? Then it stands here —
// and the role that opens it is granted by the organisation itself (org_admin).
//
// Deliberately lean. Secrets, target systems, skills, templates, runtimes,
// guard rails and egress stay in the main navigation: that is daily work on
// the workforce, not administration of the organisation.
export default function Administration({ me }: { me: Principal }) {
  return (
    <Routes>
      <Route index element={<Profile />} />
      <Route path="members" element={<Members me={me} />} />
      <Route path="usage" element={<Usage />} />
      <Route path="audit" element={<AuditTab />} />
      <Route path="diagnostics" element={<DiagnosticsTab me={me} />} />
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
        <NavLink to="/administration/diagnostics" className={({ isActive }) => (isActive ? "active" : "")}>
          {t("administration.tabDiagnostics")}
        </NavLink>
      </nav>
    </>
  );
}

/* The organisation's master data.
 *
 * The same cards also stand on the org chart, and that is deliberate: the
 * description text belongs where you have it in sight while reading the org
 * chart. They stand here because someone who administers the organisation
 * looks for them there. One store, two ways in — the same component, no second
 * editor (same pattern as ACCESS.md as a text view onto the UI store). */
function Profile() {
  const { t } = useTranslation();
  return (
    <div>
      <Header />
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        {t("administration.profileDesc")}
      </p>
      <CompanyDescription />
      <TeamSurfaceSettings />
      <OfficeFurnishing />
      <TriageSettings />
      <TriageSettings />
      <RecordingSettings />
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

/* The diagnostics ask what a restart would change here and which agent configs
   have to catch up after an upgrade. org_admin answers both — the same role
   that opens this panel, which is why it stands here too. It additionally
   stays in the main navigation: whoever checks after an upgrade looks for
   it where they walk day to day (same pattern as with the audit). */
function DiagnosticsTab({ me }: { me: Principal }) {
  return (
    <div>
      <Header />
      <Diagnostics me={me} embedded />
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

/* What this organisation consumes.
 *
 * The quotas from FR-002 P6 do not exist yet — what does exist are the
 * numbers they will one day be checked against. Showing them here is no
 * placeholder: "how many agents are running, what did they cost" is the
 * question an org admin has at month's end, and it stood spread over three
 * pages so far. */
function Usage() {
  const { t } = useTranslation();
  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[]>("/agents") });
  const users = useQuery({ queryKey: ["users"], queryFn: () => api<Human[]>("/users"), retry: false });
  const cost = useQuery({
    queryKey: ["cost", "org", "30d"],
    queryFn: () => api<OrgCostReport>("/cost/org?days=30"),
  });

  // "Not sleeping" is the most honest definition of busy the status column
  // yields: triggered, triage, working. killed does not count — a stopped
  // agent consumes nothing.
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
            {cost.data ? fmtUSD(cost.data.total_usd) : "–"}
          </div>
          <div className="l">{t("administration.cost30d")}</div>
        </div>
      </div>
    </div>
  );
}

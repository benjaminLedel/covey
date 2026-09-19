/* The signed-in interface: the side menu, the frame and the pages
   behind it.

   It stands in its own file because it is meant to be its own bundle.
   Before, everything hung on App.tsx, and App.tsx hangs on the public
   website — whoever called the start page loaded overview, backlog, guardrails,
   secrets, org chart and audit trail along with it: 1,17 MB, of which close to
   half goes unused on this page. A visitor should pay for the marketing
   page, not the application (#122).

   The pages behind the sign-in are loaded once more, each on its own.
   Whoever opens the overview does not need the platform administration — and
   whoever never opens it, never. */

import { Suspense, lazy, useEffect, useState } from "react";
import { BirdMark } from "./components/BirdMark";
import { useQuery } from "@tanstack/react-query";
import { Link, NavLink, Navigate, Route, Routes, useLocation } from "react-router";
import { useTranslation } from "react-i18next";
import {
  api,
  buildInfo,
  inbox,
  istSystemAdmin,
  type Agent,
  type Department,
  type Principal,
  type SetupState,
} from "./api";
import i18n, { initialLang, ladeSprache } from "./i18n";
import HelpDrawer from "./components/HelpDrawer";
import { NavIcon } from "./components/navicons";
import ShellFoot from "./components/ShellFoot";
import Suche, { useSucheKuerzel } from "./components/Suche";

/* The look of the interface comes with it, not before it — see app.css. */
import "./app.css";

const Dashboard = lazy(() => import("./pages/Dashboard"));
const AgentPage = lazy(() => import("./pages/Agent"));
const Inbox = lazy(() => import("./pages/Inbox"));
const Guardrails = lazy(() => import("./pages/Guardrails"));
const Secrets = lazy(() => import("./pages/Secrets"));
const Skills = lazy(() => import("./pages/Skills"));
const Voices = lazy(() => import("./pages/Voices"));
const Administration = lazy(() => import("./pages/Administration"));
const Platform = lazy(() => import("./pages/Platform"));
const Org = lazy(() => import("./pages/Org"));
const PersonPage = lazy(() => import("./pages/Person"));
const Targets = lazy(() => import("./pages/Targets"));
const Egress = lazy(() => import("./pages/Egress"));
const Requests = lazy(() => import("./pages/Requests"));
const Infrastructure = lazy(() => import("./pages/Infrastructure"));
const Audit = lazy(() => import("./pages/Audit"));
const Templates = lazy(() => import("./pages/Templates"));
const Setup = lazy(() => import("./pages/Setup"));
const Costs = lazy(() => import("./pages/Costs"));


function NavItem({ to, icon, label, end, count }: { to: string; icon: string; label: string; end?: boolean; count?: number }) {
  return (
    <NavLink to={to} end={end} className={({ isActive }) => `nav-item ${isActive ? "active" : ""}`}>
      <NavIcon name={icon} />
      {label}
      {count != null && count > 0 && <span className="count">{count}</span>}
    </NavLink>
  );
}


// Foot of the main column: which build runs here. After a deploy the first
// question — version, commit and build time come from the binary itself
// (internal/buildinfo). Deliberately not in the sidebar: that stays
// reserved for the navigation. The full details stand in the tooltip.
function BuildLine() {
  const { t, i18n } = useTranslation();
  const q = useQuery({
    queryKey: ["version"],
    queryFn: buildInfo,
    staleTime: Infinity, // changes only with a restart of the server
    retry: false,
  });
  const b = q.data;
  if (!b) return null;

  const built = b.built_at ? new Date(b.built_at) : null;
  const valid = built && !isNaN(built.getTime());
  const fmt: Intl.DateTimeFormatOptions = { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit" };
  const version = b.version + (b.dirty ? "-dirty" : "");
  const short = [version, b.commit.slice(0, 7)].filter(Boolean).join(" · ");
  const title = [
    short,
    valid ? t("version.builtAt", { when: built.toLocaleString(i18n.language, fmt) }) : null,
    b.go,
  ]
    .filter(Boolean)
    .join("\n");

  return (
    <div className="main-build" title={title}>
      {short}
      {valid && <span className="bt"> · {built.toLocaleString(i18n.language, fmt)}</span>}
      {/* covey runs as a network service under AGPL-3.0. The source link
          stands here because that is exactly the duty an operator otherwise
          overlooks — and because it should be reachable from every page. */}
      {b.source && (
        <div className="bt">
          <a className="build-src" href={b.source} target="_blank" rel="noopener noreferrer">
            {t("version.source")}
          </a>
        </div>
      )}
    </div>
  );
}

export default function AppShell({ me, onLogout }: { me: Principal; onLogout: () => void }) {
  const { t } = useTranslation();
  const location = useLocation();
  const [helpOpen, setHelpOpen] = useState(false);
  /* Sitz, Organisationswechsel, Erscheinungsbild, Sprache und Abmelden liegen
     im Fuß (components/ShellFoot.tsx) — er ist in beiden Schalen derselbe. */

  /* The language choice of the interface is a personal setting and stands
     in localStorage. The sign-in area follows the address, the
     signed-in interface follows the choice — here it is caught up as soon as
     the interface takes over.

     It is the same decision as at startup (initialLang in i18n.ts), and
     that is why it stands in one place: saved choice, otherwise the language
     of the browser, otherwise the base language. If a fixed "en" stood here, the
     interface would tip anyone who comes in without a saved choice, right after
     the sign-in, from the language of their browser to English — the
     sign-in page had already greeted them in their own.

     The path is "/" here and not the real one: an app address carries no
     language, and that of the sign-in area is over with the sign-in. */
  useEffect(() => {
    const gewaehlt = initialLang("/");
    if (i18n.language !== gewaehlt) void ladeSprache(gewaehlt);
  }, []);

  // Administration is needed rarely — collapsed by default, the state is
  // remembered; on an administration page the group is always open.
  const inPlatform = ["/administration", "/platform"].some((p) => location.pathname.startsWith(p));
  const [platformOpen, setPlatformOpen] = useState(() => localStorage.getItem("covey.nav.platform") === "1");
  const togglePlatform = () => {
    const next = !platformOpen;
    setPlatformOpen(next);
    localStorage.setItem("covey.nav.platform", next ? "1" : "0");
  };
  const showPlatform = platformOpen || inPlatform;

  // "?" opens the help from anywhere — except while typing in form fields.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable) return;
      if (e.key === "?") {
        e.preventDefault();
        setHelpOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  /* One counter for both. Approvals and open points are different
     things (there an agent waits, here nobody does) — but they need
     the same person, and two numbers side by side are one more question that
     someone would have to answer while walking past. The separation stands on the
     page, where it belongs. */
  /* Einhundert statt einer: Die Zahl an der Navigation kommt aus `pending`
     und hinge auch an einer einzigen Zeile, aber die Suche will wissen, WER
     wartet — und dafür braucht sie die Liste. Eine Abfrage für beides. */
  const inboxCount = useQuery({
    queryKey: ["inbox", "count"],
    queryFn: () => inbox({ status: "open", limit: 100 }),
    refetchInterval: 15000,
  });
  const pending = inboxCount.data?.pending ?? 0;

  /* Dieselbe Suche wie im Team, nur führt sie hier auf die Agentenseite.
     Die beiden Abfragen teilen sich den Cache mit den Seiten, die sie
     ohnehin brauchen — die Schale fragt den Server deshalb nicht öfter. */
  const [sucheOffen, setSucheOffen] = useState(false);
  useSucheKuerzel(() => setSucheOffen(true));
  const agenten = useQuery({
    queryKey: ["agents"],
    queryFn: () => api<Agent[] | null>("/agents"),
    staleTime: 60_000,
  });
  const abteilungen = useQuery({
    queryKey: ["departments"],
    queryFn: () => api<Department[] | null>("/departments"),
    staleTime: 300_000,
  });
  const wartetBei = new Set((inboxCount.data?.items ?? []).map((e) => e.agent_id));

  /* Setup stands in the menu only as long as it has something to do.
     A point that stays permanently and is permanently done turns into
     furniture — the same reasoning as with the checklist on the
     agent overview, which disappears on its own. Nothing is lost:
     the organisation description then lies in the org chart, the access under
     Secrets and Runtimes, and /setup stays reachable for whoever types it.
     Whoever may not set up gets a 403 from the endpoint — and so
     does not see the point in the first place. */
  const setup = useQuery({
    queryKey: ["setup"],
    queryFn: () => api<SetupState>("/setup/state"),
    retry: false,
    staleTime: 60_000,
  });
  const setupOpen = !!setup.data && !(setup.data.engine_done && setup.data.org_done && setup.data.people_done);

  /* Setup runs without the shell: no side menu, no help shelf,
     nothing that calls on the side. That is no cosmetics — the three cards are
     the only thing someone should do the first time, and a navigation that
     already offers thirteen other places invites exactly to
     click them away. The way out therefore stands visibly top right: the
     setup can be skipped, but it should not happen in passing. */
  if (location.pathname === "/setup") {
    return (
      <Suspense fallback={null}>
        <Setup />
      </Suspense>
    );
  }

  return (
    <div className="flex min-h-screen">
      <aside className="sidebar">
        <div className="brand">
          <BirdMark size={26} />
          covey
          <button
            className="brand-suche"
            onClick={() => setSucheOffen(true)}
            title={`${t("team.suche")} (⌘K)`}
            aria-label={t("team.suche")}
          >
            <NavIcon name="search" />
          </button>
        </div>
        {/* Der Weg zurück in den Workspace. Er steht oben und nicht in einem
            Menü, weil er das Gegenstück zum Schalter dort ist: zwei Schalen,
            eine Bewegung zwischen ihnen. */}
        <nav className="shell-schalter" aria-label={t("team.schalterAria")}>
          <Link to="/" className="shell-schalter-aus">
            {t("team.workspace")}
          </Link>
          <span className="shell-schalter-an" aria-current="page">
            {t("team.verwaltung")}
          </span>
        </nav>
        {/* The navigation grew — the order showed when something was added,
            not when it is needed. Now sorted by the everyday: on top what
            opens daily; below what is set up once; then the
            oversight. */}
        <div className="nav-group">
          <NavItem to="/agents" end icon="robot" label={t("nav.agents")} />
          <NavItem to="/inbox" icon="bell" label={t("nav.inbox")} count={pending} />
          <NavItem to="/costs" icon="chart" label={t("nav.costs")} />
          <NavItem to="/org" icon="sitemap" label={t("nav.org")} />
        </div>
        <div className="nav-sec">{t("nav.setup")}</div>
        <div className="nav-group">
          {setupOpen && <NavItem to="/setup" icon="checklist" label={t("nav.setupPage")} />}
          <NavItem to="/secrets" icon="key" label={t("nav.secrets")} />
          <NavItem to="/targets" icon="plug" label={t("nav.targets")} />
          <NavItem to="/skills" icon="book" label={t("nav.skills")} />
          <NavItem to="/voices" icon="book" label={t("nav.voices")} />
          <NavItem to="/templates" icon="copy" label={t("nav.templates")} />
          <NavItem to="/infrastructure" icon="server" label={t("nav.infrastructure")} />
        </div>
        <div className="nav-sec">{t("nav.control")}</div>
        <div className="nav-group">
          <NavItem to="/guardrails" icon="shield" label={t("nav.guardrails")} />
          <NavItem to="/egress" icon="globe" label={t("nav.egress")} />
          {/* Only those who may fetch the request log should read it (the API
              lets `org_admin` and `security` through) — otherwise the menu
              showed a path that ends in a 403. It used to sit in the
              admin block, which hid it from security. */}
          {/* The audit trail is for those who review it: org admin,
              security, auditor. Agent owners and controlling stand
              in it themselves. */}
          {["org_admin", "security", "auditor"].includes(me.Role) && (
            <NavItem to="/audit" icon="clipboard" label={t("nav.audit")} />
          )}
          {(me.Role === "org_admin" || me.Role === "security") && (
            <NavItem to="/requests" icon="exchange" label={t("nav.requests")} />
          )}

        </div>
        <div className="mt-auto">
          {/* Two areas, two scopes — and two different levels that open
              them. Administration manages THIS organisation, so it belongs to
              the role the organisation grants itself. The platform
              manages the installation and hangs on the account, where no
              organisation can hand it out
              (FR-003, finding F). */}
          {(me.Role === "org_admin" || istSystemAdmin(me)) && (
            <>
              <button className={`nav-sec toggle ${showPlatform ? "open" : ""}`} onClick={togglePlatform} disabled={inPlatform}>
                {t("nav.administrationSection")}
                <NavIcon name="chevron" />
              </button>
              {showPlatform && (
                <div className="nav-group">
                  {me.Role === "org_admin" && (
                    <NavItem to="/administration" icon="user" label={t("nav.administration")} />
                  )}
                  {istSystemAdmin(me) && (
                    <NavItem to="/platform" icon="box" label={t("nav.platform")} />
                  )}
                </div>
              )}
            </>
          )}
          {/* Der Fuß ist ein eigenes Bauteil: Beide Schalen tragen ihn, und
              er muss in beiden derselbe sein. */}
          <ShellFoot me={me} onLogout={onLogout} onHelp={() => setHelpOpen(true)} />
        </div>
      </aside>
      <main className="flex-1 min-w-0 flex flex-col">
        {/* Full width. A hard cap of 1080px came from the time when this was
            mostly forms; by now it is boards, tables and the three-column wiki
            area that lack the room — on a wide screen a third stayed empty on
            the right. Reading width is therefore the business of the content
            that needs it (see `.measure` in styles.css), not of the
            frame. */}
        <div key={location.pathname} className="fade flex-1" style={{ padding: "22px 26px 60px" }}>
          {/* Every page is its own bundle and arrives only when it is
              called for. No placeholder: the frame already stands, and a
              spinner for two hundred milliseconds is a flicker, not a
              signal. */}
          <Suspense fallback={null}>
          <Routes>
            <Route path="/agents" element={<Dashboard me={me} />} />
            <Route path="/agents/:id" element={<AgentPage me={me} />} />
            <Route path="/templates" element={<Templates me={me} />} />
            <Route path="/skills" element={<Skills me={me} />} />
            <Route path="/voices" element={<Voices me={me} />} />
            <Route path="/org" element={<Org />} />
            <Route path="/costs" element={<Costs />} />
            <Route path="/people/:id" element={<PersonPage me={me} />} />
            <Route path="/profile" element={<Navigate to={`/people/${me.ID}`} replace />} />
            <Route path="/inbox" element={<Inbox me={me} />} />
            {/* The old addresses stay valid: both were linked to. */}
            <Route path="/approvals" element={<Navigate to="/inbox" replace />} />
            <Route path="/improvements" element={<Navigate to="/inbox" replace />} />
            <Route path="/guardrails" element={<Guardrails me={me} />} />
            <Route path="/secrets" element={<Secrets me={me} />} />
            {/* The two administration areas. They differ not in the duties
                but in the reach: `/administration` manages the organisation
                this session is currently working in,
                `/platform` the installation with all its tenants. */}
            <Route
              path="/administration/*"
              element={me.Role === "org_admin" ? <Administration me={me} /> : <Navigate to="/agents" replace />}
            />
            <Route
              path="/platform/*"
              element={istSystemAdmin(me) ? <Platform me={me} /> : <Navigate to="/agents" replace />}
            />
            {/* The old addresses stay valid — both were linked to and
                bookmarked. */}
            <Route path="/users" element={<Navigate to="/administration/members" replace />} />
            <Route path="/orgs" element={<Navigate to="/platform" replace />} />
            <Route path="/infrastructure/*" element={<Infrastructure me={me} />} />
            {/* The old addresses stay valid: all three were linked to and
                bookmarked, and a bookmark that leads nowhere is the worst way
                to notice a restructure. */}
            <Route path="/runtimes" element={<Navigate to="/infrastructure" replace />} />
            <Route path="/workplaces" element={<Navigate to="/infrastructure/workplaces" replace />} />
            <Route path="/requests" element={<Requests me={me} />} />
            <Route path="/runners" element={<Navigate to="/infrastructure/runners" replace />} />
            {/* Diagnostics sits in administration (the "Diagnostics" tab). The
                old address stays valid: it is linked in runbooks, and
                a dead bookmark is the worst way to notice a
                restructure. */}
            <Route path="/diagnostics" element={<Navigate to="/administration/diagnostics" replace />} />
            <Route path="/audit" element={<Audit />} />
            <Route path="/targets" element={<Targets me={me} />} />
            <Route path="/egress/*" element={<Egress me={me} />} />
            <Route path="*" element={<Navigate to="/agents" />} />
          </Routes>
          </Suspense>
        </div>
        <BuildLine />
      </main>
      <Suche
        offen={sucheOffen}
        onClose={() => setSucheOffen(false)}
        agents={agenten.data ?? []}
        departments={abteilungen.data ?? []}
        wartetBei={wartetBei}
        pfad={(a) => `/agents/${a.id}`}
      />
      <HelpDrawer open={helpOpen} onClose={() => setHelpOpen(false)} />
    </div>
  );
}

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

import { Suspense, lazy, useEffect, useState, type JSX } from "react";
import { BirdMark } from "./components/BirdMark";
import { useQuery } from "@tanstack/react-query";
import { NavLink, Navigate, Route, Routes, useLocation } from "react-router";
import { useTranslation } from "react-i18next";
import {
  api,
  buildInfo,
  inbox,
  istSystemAdmin,
  post,
  type Principal,
  type SetupState,
} from "./api";
import i18n, { initialLang, ladeSprache } from "./i18n";
import HelpDrawer from "./components/HelpDrawer";
import GitHubLink from "./components/GitHubLink";
import LangPicker from "./components/LangPicker";
import { useMemberships, useSwitchOrg } from "./components/OrgSwitcher";
import ThemeSwitch from "./components/ThemeSwitch";

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

// Icon paths from mockup/covey-ui-mockup.html — the nav takes over the design language of the mockup.
const icons: Record<string, JSX.Element> = {
  checklist: (
    <>
      <rect x="4" y="4" width="16" height="16" rx="2" />
      <path d="M8 9.5l1.6 1.6L12.5 8" />
      <path d="M8 15.5h8" />
    </>
  ),
  robot: (
    <>
      <rect x="5" y="8" width="14" height="11" rx="2" />
      <path d="M12 4v4" />
      <circle cx="12" cy="3.5" r="1" />
      <circle cx="9.5" cy="13" r="1" />
      <circle cx="14.5" cy="13" r="1" />
    </>
  ),
  sitemap: (
    <>
      <rect x="9" y="3" width="6" height="5" rx="1" />
      <rect x="3" y="16" width="6" height="5" rx="1" />
      <rect x="15" y="16" width="6" height="5" rx="1" />
      <path d="M12 8v4M6 16v-2h12v2M12 12v2" />
    </>
  ),
  bell: (
    <>
      <path d="M6 9a6 6 0 0 1 12 0c0 5 2 6 2 6H4s2-1 2-6" />
      <path d="M10 20a2 2 0 0 0 4 0" />
    </>
  ),
  shield: <path d="M12 3l7 3v5c0 5-3 8-7 10c-4-2-7-5-7-10V6z" />,
  key: (
    <>
      <circle cx="8" cy="8" r="3.5" />
      <path d="M10.5 10.5L20 20M17 17l2-2M14 14l2 2" />
    </>
  ),
  user: (
    <>
      <circle cx="12" cy="8" r="4" />
      <path d="M4 20c0-4 4-6 8-6s8 2 8 6" />
    </>
  ),
  box: (
    <>
      <path d="M12 3l8 4.5v9L12 21l-8-4.5v-9z" />
      <path d="M4 7.5l8 4.5l8-4.5M12 12v9" />
    </>
  ),
  cpu: (
    <>
      <rect x="7" y="7" width="10" height="10" rx="1.5" />
      <rect x="10" y="10" width="4" height="4" />
      <path d="M10 3v3M14 3v3M10 18v3M14 18v3M3 10h3M3 14h3M18 10h3M18 14h3" />
    </>
  ),
  stethoscope: (
    <>
      <path d="M6 3v5a5 5 0 0 0 10 0V3" />
      <path d="M4 3h3M15 3h3" />
      <path d="M11 13v3a4 4 0 0 0 8 0v-1" />
      <circle cx="19" cy="10" r="2" />
    </>
  ),
  server: (
    <>
      <rect x="3" y="4" width="18" height="7" rx="1.5" />
      <rect x="3" y="13" width="18" height="7" rx="1.5" />
      <path d="M7 7.5h.01M7 16.5h.01" />
    </>
  ),
  plug: (
    <>
      <path d="M9 3v5M15 3v5" />
      <path d="M6 8h12v3a6 6 0 0 1-6 6a6 6 0 0 1-6-6z" />
      <path d="M12 17v4" />
    </>
  ),
  globe: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18M12 3c2.5 2.5 3.8 5.7 3.8 9s-1.3 6.5-3.8 9c-2.5-2.5-3.8-5.7-3.8-9S9.5 5.5 12 3z" />
    </>
  ),
  help: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M9.6 9.4a2.5 2.5 0 1 1 3.2 2.4c-.7.3-1 .8-1 1.5v.2" />
      <path d="M12 16.4v.01" />
    </>
  ),
  copy: (
    <>
      <rect x="9" y="9" width="12" height="13" rx="2" />
      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
    </>
  ),
  chart: (
    <>
      <path d="M4 20V4M4 20h16" />
      <rect x="7" y="12" width="3" height="5" rx="0.5" />
      <rect x="12" y="8" width="3" height="9" rx="0.5" />
      <rect x="17" y="5" width="3" height="12" rx="0.5" />
    </>
  ),
  book: (
    <>
      <path d="M4 5.5A1.5 1.5 0 0 1 5.5 4H19v14H5.5A1.5 1.5 0 0 0 4 19.5z" />
      <path d="M4 19.5A1.5 1.5 0 0 1 5.5 18H19v2H5.5" />
      <path d="M8 8.5h7" />
    </>
  ),
  chevron: <path d="M9 6l6 6l-6 6" />,
  // Audit: a clipboard — the list of what people have done.
  clipboard: (
    <>
      <rect x="5" y="4" width="14" height="17" rx="2" />
      <path d="M9 4V3h6v1M8.5 10h7M8.5 14h7M8.5 18h4" />
    </>
  ),
  // Request log: two arrows, in and out.
  exchange: (
    <>
      <path d="M4 8h14l-3.5-3.5M20 16H6l3.5 3.5" />
    </>
  ),
  logout: (
    <>
      <path d="M9 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h3" />
      <path d="M16 17l5-5l-5-5M21 12H9" />
    </>
  ),
  dots: (
    <>
      <circle cx="12" cy="5.5" r="0.9" />
      <circle cx="12" cy="12" r="0.9" />
      <circle cx="12" cy="18.5" r="0.9" />
    </>
  ),
};

function NavIcon({ name }: { name: string }) {
  return (
    <svg className="ic" viewBox="0 0 24 24" aria-hidden="true">
      {icons[name]}
    </svg>
  );
}

function NavItem({ to, icon, label, end, count }: { to: string; icon: string; label: string; end?: boolean; count?: number }) {
  return (
    <NavLink to={to} end={end} className={({ isActive }) => `nav-item ${isActive ? "active" : ""}`}>
      <NavIcon name={icon} />
      {label}
      {count != null && count > 0 && <span className="count">{count}</span>}
    </NavLink>
  );
}

// Monogram from the display name: first letters of the first two words.
function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
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
  /* The same query as in the build line in the foot — TanStack serves it from
     the cache, the server is not asked twice. All this needs from it
     here is the address of the source text. */
  const build = useQuery({ queryKey: ["version"], queryFn: buildInfo, staleTime: Infinity, retry: false });
  const location = useLocation();
  const [helpOpen, setHelpOpen] = useState(false);
  const [userMenu, setUserMenu] = useState(false);

  /* With more than one seat the footer names the organisation and the menu
     offers the others (#262). With one, both would answer a question nobody
     asked. */
  const memberships = useMemberships();
  const seats = memberships.data ?? [];
  const switchOrg = useSwitchOrg(onLogout);
  const activeOrg = seats.length > 1 ? seats.find((m) => m.org_id === me.OrgID) : undefined;

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
  const inboxCount = useQuery({
    queryKey: ["inbox", "count"],
    queryFn: () => inbox({ status: "open", limit: 1 }),
    refetchInterval: 15000,
  });
  const pending = inboxCount.data?.pending ?? 0;

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

  const logout = async () => {
    await post("/auth/logout");
    onLogout();
  };

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
        </div>
        {/* The navigation grew — the order showed when something was added,
            not when it is needed. Now sorted by the everyday: on top what
            opens daily; below what is set up once; then the
            oversight. */}
        <div className="nav-group">
          <NavItem to="/" end icon="robot" label={t("nav.agents")} />
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
          {/* Footer: one row — user (link to the profile) + ⋯ menu with
              language, help and log out. */}
          <div className="side-foot">
            <div className="suser-row">
              <NavLink to="/profile" className="suser" title={t("nav.profile")}>
                <span className="avatar">{initials(me.DisplayName)}</span>
                <span className="min-w-0">
                  <span className="nm truncate block">{me.DisplayName}</span>
                  <span className="rl block truncate">
                    {t(`role.${me.Role}`, me.Role)}
                    {activeOrg && ` · ${activeOrg.org_name}`}
                  </span>
                </span>
              </NavLink>
              <button
                className={`icon-btn foot-menu-btn${userMenu ? " open" : ""}`}
                onClick={() => setUserMenu(v => !v)}
                title={t("nav.userMenu")}
                aria-label={t("nav.userMenu")}
                aria-expanded={userMenu}
              >
                <NavIcon name="dots" />
              </button>
              {userMenu && (
                <>
                  <div className="foot-menu-backdrop" onClick={() => setUserMenu(false)} />
                  <div className="foot-menu">
                    {seats.length > 1 && (
                      <>
                        <div className="foot-menu-sec">{t("nav.orgSwitch")}</div>
                        {seats.map((m) => {
                          const active = m.org_id === me.OrgID;
                          return (
                            <button
                              key={m.org_id}
                              onClick={() => { setUserMenu(false); switchOrg.mutate(m.org_id); }}
                              disabled={active || switchOrg.isPending}
                              aria-current={active ? "true" : undefined}
                              style={active ? { fontWeight: 600 } : undefined}
                            >
                              <NavIcon name="box" />
                              <span className="truncate">{m.org_name}</span>
                            </button>
                          );
                        })}
                        <div className="sep" />
                      </>
                    )}
                    <div className="foot-menu-sec">{t("theme.label")}</div>
                    <ThemeSwitch />
                    <div className="sep" />
                    <LangPicker variant="menu" />
                    <div className="sep" />
                    <GitHubLink url={build.data?.source} variant="menu" />
                    <button onClick={() => { setUserMenu(false); setHelpOpen(true); }}>
                      <NavIcon name="help" />
                      {t("nav.help")}
                    </button>
                    <div className="sep" />
                    <button className="danger" onClick={logout}>
                      <NavIcon name="logout" />
                      {t("nav.logout")}
                    </button>
                  </div>
                </>
              )}
            </div>
          </div>
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
            <Route path="/" element={<Dashboard me={me} />} />
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
              element={me.Role === "org_admin" ? <Administration me={me} /> : <Navigate to="/" replace />}
            />
            <Route
              path="/platform/*"
              element={istSystemAdmin(me) ? <Platform me={me} /> : <Navigate to="/" replace />}
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
            <Route path="*" element={<Navigate to="/" />} />
          </Routes>
          </Suspense>
        </div>
        <BuildLine />
      </main>
      <HelpDrawer open={helpOpen} onClose={() => setHelpOpen(false)} />
    </div>
  );
}

import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { NavLink, Navigate, Route, Routes, useLocation } from "react-router";
import { useTranslation } from "react-i18next";
import { api, buildInfo, post, type Approval, type ImprovementItem, type Principal, type SetupState } from "./api";
import i18n, { gespeicherteSprache, istVorgerendert, merkeSprache } from "./i18n";
import HelpDrawer from "./components/HelpDrawer";
import ThemeSwitch from "./components/ThemeSwitch";
import PublicSite from "./public/PublicSite";
import Dashboard from "./pages/Dashboard";
import AgentPage from "./pages/Agent";
import Approvals from "./pages/Approvals";
import Improvements from "./pages/Improvements";
import Guardrails from "./pages/Guardrails";
import Secrets from "./pages/Secrets";
import Skills from "./pages/Skills";
import Users from "./pages/Users";
import Organizations from "./pages/Organizations";
import Org from "./pages/Org";
import PersonPage from "./pages/Person";
import Runtimes from "./pages/Runtimes";
import Targets from "./pages/Targets";
import Egress from "./pages/Egress";
import Requests from "./pages/Requests";
import Audit from "./pages/Audit";
import Templates from "./pages/Templates";
import Setup from "./pages/Setup";
import Costs from "./pages/Costs";

// useLiveEvents hält die UI über SSE aktuell: jedes Server-Event invalidiert
// die betroffenen Queries — TanStack Query lädt gezielt nach.
function useLiveEvents(enabled: boolean) {
  const qc = useQueryClient();
  useEffect(() => {
    if (!enabled) return;
    const es = new EventSource("/api/v1/events");
    const invalidate = () => {
      qc.invalidateQueries({ queryKey: ["agents"] });
      qc.invalidateQueries({ queryKey: ["agent"] });
      qc.invalidateQueries({ queryKey: ["backlog"] });
      qc.invalidateQueries({ queryKey: ["recording"] });
      qc.invalidateQueries({ queryKey: ["approvals"] });
      qc.invalidateQueries({ queryKey: ["cost"] });
      qc.invalidateQueries({ queryKey: ["memories"] });
    };
    for (const t of ["agent_status", "task", "recording", "approval", "guardrail"]) {
      es.addEventListener(t, invalidate);
    }
    return () => es.close();
  }, [enabled, qc]);
}

/* Wurde diese Seite beim Build vorgerendert? Dann steht ihr Inhalt schon im
   HTML, und der erste Rendervorgang im Browser muss ihn treffen — sonst
   verwirft React das vorgerenderte Markup. Deshalb zeigt App auf solchen
   Seiten die öffentliche Website bereits, während /auth/me noch läuft. Auf
   allen anderen Pfaden bleibt es beim bisherigen Verhalten. */
const prerendered = istVorgerendert();

export default function App() {
  const me = useQuery({
    queryKey: ["me"],
    queryFn: () => api<Principal>("/auth/me"),
    retry: false,
  });
  useLiveEvents(me.isSuccess);

  if (me.isLoading) {
    return prerendered ? <PublicSite onLogin={() => me.refetch()} /> : null;
  }
  if (me.isError) return <PublicSite onLogin={() => me.refetch()} />;
  return <Shell me={me.data!} onLogout={() => me.refetch()} />;
}

// Icon-Pfade aus mockup/covey-ui-mockup.html — die Nav übernimmt die Design-Sprache des Mockups.
const icons: Record<string, React.JSX.Element> = {
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
  wrench: (
    <>
      <path d="M14.5 4.5a4.5 4.5 0 0 0 5.6 5.9l-8.4 8.4a2.4 2.4 0 0 1-3.4-3.4l8.4-8.4a4.5 4.5 0 0 0-2.2-2.5z" />
      <circle cx="7.5" cy="17.5" r="0.8" />
    </>
  ),
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
  // Audit: ein Klemmbrett — die Liste dessen, was Menschen getan haben.
  clipboard: (
    <>
      <rect x="5" y="4" width="14" height="17" rx="2" />
      <path d="M9 4V3h6v1M8.5 10h7M8.5 14h7M8.5 18h4" />
    </>
  ),
  // Request-Log: zwei Pfeile, rein und raus.
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

// Monogramm aus dem Anzeigenamen: erste Buchstaben der ersten zwei Wörter.
function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

// Fuß der Hauptspalte: welcher Stand läuft hier. Nach einem Deploy die erste
// Frage — Version, Commit und Bauzeit kommen aus dem Binary selbst
// (internal/buildinfo). Bewusst nicht in der Sidebar: die bleibt der
// Navigation vorbehalten. Die vollen Angaben stehen im Tooltip.
function BuildLine() {
  const { t, i18n } = useTranslation();
  const q = useQuery({
    queryKey: ["version"],
    queryFn: buildInfo,
    staleTime: Infinity, // ändert sich nur mit einem Neustart des Servers
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
      {/* Covey läuft als Netzwerkdienst unter AGPL-3.0. Der Quelltext-Link
          steht hier, weil genau das die Pflicht ist, die ein Betreiber sonst
          übersieht — und weil er von jeder Seite aus erreichbar sein soll. */}
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

function Shell({ me, onLogout }: { me: Principal; onLogout: () => void }) {
  const { t } = useTranslation();
  const location = useLocation();
  const [helpOpen, setHelpOpen] = useState(false);
  const [userMenu, setUserMenu] = useState(false);

  /* Die Sprachwahl der Oberfläche ist eine persönliche Einstellung und steht
     im localStorage. Auf einer vorgerenderten Seite startet i18n bewusst auf
     Deutsch, damit die Hydration passt (siehe i18n.ts) — hier wird die Wahl
     nachgeholt, sobald die Oberfläche übernimmt. Ohne gespeicherte Wahl gilt
     die Basissprache: Sonst bliebe die angemeldete Oberfläche für jeden, der
     über eine vorgerenderte Seite hereinkommt, dauerhaft deutsch. */
  useEffect(() => {
    const gewaehlt = gespeicherteSprache() ?? "en";
    if (i18n.language !== gewaehlt) void i18n.changeLanguage(gewaehlt);
  }, []);

  // Plattform-Administration ist selten gebraucht — standardmäßig eingeklappt,
  // Zustand wird gemerkt; auf einer Plattform-Seite ist die Gruppe immer offen.
  const inPlatform = ["/users", "/orgs"].some((p) => location.pathname.startsWith(p));
  const [platformOpen, setPlatformOpen] = useState(() => localStorage.getItem("covey.nav.platform") === "1");
  const togglePlatform = () => {
    const next = !platformOpen;
    setPlatformOpen(next);
    localStorage.setItem("covey.nav.platform", next ? "1" : "0");
  };
  const showPlatform = platformOpen || inPlatform;

  // "?" öffnet die Hilfe von überall — außer beim Tippen in Formularfeldern.
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

  const approvals = useQuery({
    queryKey: ["approvals", "pending"],
    queryFn: () => api<Approval[] | null>("/approvals?status=pending"),
    refetchInterval: 15000,
  });
  const pending = approvals.data?.length ?? 0;

  // Der Zaehler der offenen Punkte. Fehlerhaft (403 fuer Controlling) heisst
  // hier schlicht: keine Zahl.
  const improvements = useQuery({
    queryKey: ["improvements", "pending-count"],
    queryFn: () => api<ImprovementItem[] | null>("/improvements?status=pending"),
    refetchInterval: 60000,
    retry: false,
    enabled: me.Role !== "controlling",
  });
  const offenePunkte = improvements.data?.length ?? 0;

  /* Die Einrichtung steht nur im Menue, solange sie etwas zu tun hat.
     Ein Punkt, der dauerhaft bleibt und dauerhaft erledigt ist, wird zu
     Moebel — dieselbe Ueberlegung wie bei der Checkliste auf der
     Agenten-Uebersicht, die von selbst verschwindet. Verloren geht nichts:
     die Unternehmensbeschreibung liegt danach im Org-Chart, der Zugang unter
     Secrets und Runtimes, und /setup bleibt erreichbar, wer es tippt.
     Wer nicht einrichten darf, bekommt vom Endpunkt eine 403 — und damit
     den Punkt gar nicht erst zu sehen. */
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

  /* Die Einrichtung laeuft ohne die Huelle: kein Seitenmenue, kein Hilfe-Regal,
     nichts, was nebenher ruft. Das ist keine Kosmetik — die drei Karten sind
     das Einzige, was jemand beim ersten Mal tun soll, und eine Navigation, die
     schon dreizehn andere Orte anbietet, laedt genau dazu ein, sie
     wegzuklicken. Der Weg heraus steht deshalb sichtbar oben rechts: die
     Einrichtung ist ueberspringbar, aber sie soll nicht nebenbei passieren. */
  if (location.pathname === "/setup") {
    return <Setup />;
  }

  const toggleLang = () => {
    const next = i18n.language === "de" ? "en" : "de";
    i18n.changeLanguage(next);
    merkeSprache(next);
  };

  return (
    <div className="flex min-h-screen">
      <aside className="sidebar">
        <div className="brand">
          <span className="mark" aria-hidden="true">
            {/* Covey = ein Schwarm — drei Vögel in Flugformation als Wortmarke. */}
            <svg viewBox="0 0 24 24" width="18" height="18">
              <path d="M7 15 Q9.75 11.8 12.5 15 Q15.25 11.8 18 15" />
              <path d="M3.5 10 Q5.5 7.7 7.5 10 Q9.5 7.7 11.5 10" />
              <path d="M13 8 Q14.5 6.3 16 8 Q17.5 6.3 19 8" />
            </svg>
          </span>
          Covey
        </div>
        {/* Die Navigation ist gewachsen — die Reihenfolge bildete ab, wann
            etwas dazukam, nicht wann man es braucht. Jetzt nach dem Alltag
            sortiert: oben, was man taeglich oeffnet; darunter, was man einmal
            einrichtet; dann die Aufsicht. */}
        <div className="nav-group">
          <NavItem to="/" end icon="robot" label={t("nav.agents")} />
          <NavItem to="/approvals" icon="bell" label={t("nav.approvals")} count={pending} />
          {/* Die offenen Punkte aus dem Betrieb (spec/21). Neben den Freigaben,
              weil es dieselbe Handbewegung ist: etwas liegt da und wartet auf
              die Entscheidung eines Menschen. Controlling sieht den Punkt
              nicht — der Endpunkt liesse es nicht durch, und ein Menuepunkt,
              der in einer 403 endet, ist keiner. */}
          {me.Role !== "controlling" && (
            <NavItem to="/improvements" icon="wrench" label={t("nav.improvements")} count={offenePunkte} />
          )}
          <NavItem to="/costs" icon="chart" label={t("nav.costs")} />
          <NavItem to="/org" icon="sitemap" label={t("nav.org")} />
        </div>
        <div className="nav-sec">{t("nav.setup")}</div>
        <div className="nav-group">
          {setupOpen && <NavItem to="/setup" icon="checklist" label={t("nav.setupPage")} />}
          <NavItem to="/secrets" icon="key" label={t("nav.secrets")} />
          <NavItem to="/targets" icon="plug" label={t("nav.targets")} />
          <NavItem to="/skills" icon="book" label={t("nav.skills")} />
          <NavItem to="/templates" icon="copy" label={t("nav.templates")} />
          <NavItem to="/runtimes" icon="cpu" label={t("nav.runtimes")} />
        </div>
        <div className="nav-sec">{t("nav.control")}</div>
        <div className="nav-group">
          <NavItem to="/guardrails" icon="shield" label={t("nav.guardrails")} />
          <NavItem to="/egress" icon="globe" label={t("nav.egress")} />
          {/* Das Request-Log liest nur, wer es auch abrufen darf (die API
              laesst platform_admin und security durch) — sonst zeigte das
              Menue einen Weg, der in einer 403 endet. Vorher stand es im
              Admin-Block und blieb Security damit verborgen. */}
          {/* Die Audit-Spur geht die an, die sie prüfen: Plattform-Admin,
              Security, Auditor. Agent-Owner und Controlling stehen selbst
              darin. */}
          {["platform_admin", "security", "auditor"].includes(me.Role) && (
            <NavItem to="/audit" icon="clipboard" label={t("nav.audit")} />
          )}
          {(me.Role === "platform_admin" || me.Role === "security") && (
            <NavItem to="/requests" icon="exchange" label={t("nav.requests")} />
          )}
        </div>
        <div className="mt-auto">
          {me.Role === "platform_admin" && (
            <>
              <button className={`nav-sec toggle ${showPlatform ? "open" : ""}`} onClick={togglePlatform} disabled={inPlatform}>
                {t("nav.platform")}
                <NavIcon name="chevron" />
              </button>
              {showPlatform && (
                <div className="nav-group">
                  <NavItem to="/users" icon="user" label={t("nav.users")} />
                  <NavItem to="/orgs" icon="box" label={t("nav.organizations")} />
                </div>
              )}
            </>
          )}
          {/* Fuß: eine Zeile — Nutzer (Link zum Profil) + ⋯-Menü mit
              Sprache, Hilfe und Abmelden. */}
          <div className="side-foot">
            <div className="suser-row">
              <NavLink to="/profile" className="suser" title={t("nav.profile")}>
                <span className="avatar">{initials(me.DisplayName)}</span>
                <span className="min-w-0">
                  <span className="nm truncate block">{me.DisplayName}</span>
                  <span className="rl block truncate">{t(`role.${me.Role}`, me.Role)}</span>
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
                    <div className="foot-menu-sec">{t("theme.label")}</div>
                    <ThemeSwitch />
                    <div className="sep" />
                    <button onClick={toggleLang}>
                      <NavIcon name="globe" />
                      {t("lang.switchTo")}
                    </button>
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
        {/* Volle Breite. Ein harter Deckel von 1080px stammte aus der Zeit, als
            hier vor allem Formulare standen; inzwischen sind es Boards, Tabellen
            und die dreispaltige Wiki-Fläche, denen der Platz fehlt — auf einem
            breiten Schirm blieb rechts ein Drittel leer. Lesebreite ist damit
            Sache der Inhalte, die sie brauchen (siehe .measure in styles.css),
            nicht des Rahmens. */}
        <div key={location.pathname} className="fade flex-1" style={{ padding: "22px 26px 60px" }}>
          <Routes>
            <Route path="/" element={<Dashboard me={me} />} />
            <Route path="/agents/:id" element={<AgentPage me={me} />} />
            <Route path="/templates" element={<Templates me={me} />} />
            <Route path="/skills" element={<Skills me={me} />} />
            <Route path="/org" element={<Org />} />
            <Route path="/costs" element={<Costs />} />
            <Route path="/people/:id" element={<PersonPage me={me} />} />
            <Route path="/profile" element={<Navigate to={`/people/${me.ID}`} replace />} />
            <Route path="/approvals" element={<Approvals />} />
            <Route path="/improvements" element={<Improvements me={me} />} />
            <Route path="/guardrails" element={<Guardrails me={me} />} />
            <Route path="/secrets" element={<Secrets me={me} />} />
            <Route path="/users" element={<Users me={me} />} />
            <Route path="/orgs" element={<Organizations me={me} />} />
            <Route path="/runtimes" element={<Runtimes me={me} />} />
            <Route path="/requests" element={<Requests me={me} />} />
            <Route path="/audit" element={<Audit />} />
            <Route path="/targets" element={<Targets me={me} />} />
            <Route path="/egress/*" element={<Egress me={me} />} />
            <Route path="*" element={<Navigate to="/" />} />
          </Routes>
        </div>
        <BuildLine />
      </main>
      <HelpDrawer open={helpOpen} onClose={() => setHelpOpen(false)} />
    </div>
  );
}

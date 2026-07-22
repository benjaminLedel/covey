import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { NavLink, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { api, post, type Approval, type Principal } from "./api";
import HelpDrawer from "./components/HelpDrawer";
import Login from "./pages/Login";
import Dashboard from "./pages/Dashboard";
import AgentPage from "./pages/Agent";
import Approvals from "./pages/Approvals";
import Guardrails from "./pages/Guardrails";
import Secrets from "./pages/Secrets";
import Users from "./pages/Users";
import Organizations from "./pages/Organizations";
import Org from "./pages/Org";
import PersonPage from "./pages/Person";
import Runtimes from "./pages/Runtimes";
import Targets from "./pages/Targets";
import Egress from "./pages/Egress";
import Templates from "./pages/Templates";

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

export default function App() {
  const me = useQuery({
    queryKey: ["me"],
    queryFn: () => api<Principal>("/auth/me"),
    retry: false,
  });
  useLiveEvents(me.isSuccess);

  if (me.isLoading) return null;
  if (me.isError) return <Login onLogin={() => me.refetch()} />;
  return <Shell me={me.data!} onLogout={() => me.refetch()} />;
}

// Icon-Pfade aus mockup/covey-ui-mockup.html — die Nav übernimmt die Design-Sprache des Mockups.
const icons: Record<string, JSX.Element> = {
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
  chevron: <path d="M9 6l6 6l-6 6" />,
  logout: (
    <>
      <path d="M9 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h3" />
      <path d="M16 17l5-5l-5-5M21 12H9" />
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

function Shell({ me, onLogout }: { me: Principal; onLogout: () => void }) {
  const location = useLocation();
  const [helpOpen, setHelpOpen] = useState(false);

  // Plattform-Administration ist selten gebraucht — standardmäßig eingeklappt,
  // Zustand wird gemerkt; auf einer Plattform-Seite ist die Gruppe immer offen.
  const inPlatform = ["/users", "/orgs", "/runtimes"].some((p) => location.pathname.startsWith(p));
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

  const logout = async () => {
    await post("/auth/logout");
    onLogout();
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
        <div className="nav-group">
          <NavItem to="/" end icon="robot" label="Agenten" />
          <NavItem to="/templates" icon="copy" label="Vorlagen" />
          <NavItem to="/org" icon="sitemap" label="Organigramm" />
        </div>
        <div className="nav-sec">Vertrauen</div>
        <div className="nav-group">
          <NavItem to="/approvals" icon="bell" label="Freigaben" count={pending} />
          <NavItem to="/guardrails" icon="shield" label="Guard-Rails" />
          <NavItem to="/secrets" icon="key" label="Secrets" />
          <NavItem to="/targets" icon="plug" label="Zielsysteme" />
          <NavItem to="/egress" icon="globe" label="Egress" />
        </div>
        <div className="mt-auto">
          {me.Role === "platform_admin" && (
            <>
              <button className={`nav-sec toggle ${showPlatform ? "open" : ""}`} onClick={togglePlatform} disabled={inPlatform}>
                Plattform
                <NavIcon name="chevron" />
              </button>
              {showPlatform && (
                <div className="nav-group">
                  <NavItem to="/users" icon="user" label="Benutzer" />
                  <NavItem to="/orgs" icon="box" label="Organisationen" />
                  <NavItem to="/runtimes" icon="cpu" label="Runtimes" />
                </div>
              )}
            </>
          )}
          <div className="side-foot">
            <NavLink to="/profile" className="suser" title="Profil & Einstellungen">
              <span className="avatar">{initials(me.DisplayName)}</span>
              <span className="min-w-0">
                <span className="nm truncate block">{me.DisplayName}</span>
                <span className="rl block">{me.Role}</span>
              </span>
            </NavLink>
            <div className="foot-actions">
              <button className="icon-btn" onClick={() => setHelpOpen(true)} title="Hilfe (Taste ?)" aria-label="Hilfe">
                <NavIcon name="help" />
              </button>
              <button className="icon-btn danger" onClick={logout} title="Abmelden" aria-label="Abmelden">
                <NavIcon name="logout" />
              </button>
            </div>
          </div>
        </div>
      </aside>
      <main className="flex-1 min-w-0">
        <div key={location.pathname} className="fade" style={{ padding: "22px 26px 60px", maxWidth: 1080 }}>
          <Routes>
            <Route path="/" element={<Dashboard me={me} />} />
            <Route path="/agents/:id" element={<AgentPage me={me} />} />
            <Route path="/templates" element={<Templates me={me} />} />
            <Route path="/org" element={<Org />} />
            <Route path="/people/:id" element={<PersonPage me={me} />} />
            <Route path="/profile" element={<Navigate to={`/people/${me.ID}`} replace />} />
            <Route path="/approvals" element={<Approvals />} />
            <Route path="/guardrails" element={<Guardrails me={me} />} />
            <Route path="/secrets" element={<Secrets me={me} />} />
            <Route path="/users" element={<Users me={me} />} />
            <Route path="/orgs" element={<Organizations me={me} />} />
            <Route path="/runtimes" element={<Runtimes />} />
            <Route path="/targets" element={<Targets me={me} />} />
            <Route path="/egress/*" element={<Egress me={me} />} />
            <Route path="*" element={<Navigate to="/" />} />
          </Routes>
        </div>
      </main>
      <HelpDrawer open={helpOpen} onClose={() => setHelpOpen(false)} />
    </div>
  );
}

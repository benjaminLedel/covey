import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { api, roleLabel, statusLabel, type Human, type OrgChart, type Principal } from "../api";
import AccountSettings from "../components/AccountSettings";
import { Avatar, PersonLink } from "../components/person";
import ProfileForm from "../components/ProfileForm";

// Die Profil-Seite eines Menschen — das Gegenstück zur Agenten-Seite und
// zugleich die eigene Profil-Seite (/profile leitet hierher). Erreichbar aus
// Organigramm und Benutzerverwaltung. Profil editierbar für Admins
// (/users/{id}) und die eigene Person (/auth/me), sonst read-only; dazu die
// Einbettung in den Org-Chart und — nur bei der eigenen Person — die
// Konto-Einstellungen (Anzeigename, Passwort, Sitzungen).
export default function PersonPage({ me }: { me: Principal }) {
  const { id } = useParams<{ id: string }>();
  const person = useQuery({ queryKey: ["human", id], queryFn: () => api<Human>(`/org/humans/${id}`) });
  const chart = useQuery({ queryKey: ["orgchart"], queryFn: () => api<OrgChart>("/org/chart") });

  if (person.isLoading) return null;
  if (person.isError || !person.data) return <p className="danger-text">Person nicht gefunden.</p>;
  const h = person.data;

  const humans = chart.data?.humans ?? [];
  const agents = chart.data?.agents ?? [];
  const manager = humans.find((m) => m.id === h.manager_id);
  const reports = humans.filter((r) => r.manager_id === h.id);
  const ownAgents = agents.filter((a) => a.supervisor_id === h.id);

  const isAdmin = me.Role === "platform_admin";
  const isSelf = h.id === me.ID;

  return (
    <div style={{ maxWidth: 720 }}>
      <div className="text-sm secondary mb-3">
        <Link to="/org" style={{ color: "inherit" }}>
          Organigramm
        </Link>{" "}
        / <b style={{ color: "var(--text-primary)", fontWeight: 500 }}>{h.display_name}</b>
      </div>

      <div className="flex items-center gap-3 mb-5 flex-wrap">
        <Avatar name={h.display_name} size={44} human />
        <div>
          <h1 className="text-[22px] m-0">
            {h.display_name}
            {isSelf && <span className="muted text-sm"> — Sie</span>}
          </h1>
          <div className="muted text-xs">
            {h.job_title || (roleLabel[h.role] ?? h.role)} · <span className="mono">{h.email}</span>
          </div>
        </div>
        <span className="ntag" style={{ marginLeft: "auto" }}>
          {roleLabel[h.role] ?? h.role}
        </span>
      </div>

      <div className="card mb-4">
        <div className="text-sm font-medium mb-2">Profil</div>
        <ProfileForm human={h} endpoint={isAdmin ? `/users/${h.id}` : "/auth/me"} readOnly={!isAdmin && !isSelf} />
        {!isAdmin && !isSelf && (
          <p className="muted text-xs mt-2 mb-0">Profile anderer Personen pflegt ein Admin unter „Benutzer".</p>
        )}
      </div>

      <div className="card">
        <div className="text-sm font-medium mb-2">Im Org-Chart</div>
        <div className="text-xs">
          <div className="flex gap-2 py-0.5">
            <span className="muted" style={{ minWidth: 110 }}>berichtet an</span>
            {manager ? <PersonLink human={manager} /> : <span className="muted">niemanden</span>}
          </div>
          <div className="flex gap-2 py-0.5">
            <span className="muted" style={{ minWidth: 110 }}>direkte Berichte</span>
            {reports.length === 0 ? (
              <span className="muted">keine</span>
            ) : (
              <span className="flex gap-2 flex-wrap">
                {reports.map((r) => (
                  <PersonLink key={r.id} human={r} />
                ))}
              </span>
            )}
          </div>
          <div className="flex gap-2 py-0.5">
            <span className="muted" style={{ minWidth: 110 }}>betreute Agenten</span>
            {ownAgents.length === 0 ? (
              <span className="muted">keine</span>
            ) : (
              <span className="flex gap-2 flex-wrap items-center">
                {ownAgents.map((a) => (
                  <Link key={a.id} to={`/agents/${a.id}`}>
                    {a.display_name}
                    <span className={`badge st-${a.killed ? "killed" : a.status}`} style={{ marginLeft: 4 }}>
                      {a.killed ? "gestoppt" : statusLabel[a.status] ?? a.status}
                    </span>
                  </Link>
                ))}
              </span>
            )}
          </div>
        </div>
      </div>

      {isSelf && <AccountSettings me={me} />}
    </div>
  );
}

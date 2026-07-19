import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, roleLabel, statusLabel, type Agent, type Human, type OrgChart } from "../api";
import { Avatar } from "../components/person";

// Organigramm (spec/02, spec/09): der unternehmensweite Org-Chart über Menschen
// und Agenten. Menschen berichten an Menschen (manager_id), Agenten an ihren
// Vorgesetzten (supervisor_id). Sichtbar für alle Rollen — jeder Knoten führt
// auf seine Detail-Seite (Mensch → Profil, Agent → Agenten-Seite).
export default function Org() {
  const chart = useQuery({
    queryKey: ["orgchart"],
    queryFn: () => api<OrgChart>("/org/chart"),
  });

  if (chart.isLoading) return null;
  if (chart.isError) return <p className="danger-text">Org-Chart konnte nicht geladen werden.</p>;
  const { humans, agents } = chart.data!;

  const humanIds = new Set(humans.map((h) => h.id));
  // Wurzeln: ohne Vorgesetzte — oder mit Verweis auf jemanden außerhalb der Liste.
  const roots = humans.filter((h) => !h.manager_id || !humanIds.has(h.manager_id));
  const orphanAgents = agents.filter((a) => !a.supervisor_id || !humanIds.has(a.supervisor_id));

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">Organigramm</h1>
        <span className="muted">Menschen &amp; Agenten</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        Der Org-Chart strukturiert Eskalation und Delegation: Agenten berichten an ihren Vorgesetzten,
        Menschen an ihre Führungskraft. Zuordnungen pflegen Sie auf der Agenten-Seite bzw. in der Benutzerverwaltung.
      </p>

      <div className="org-legend">
        <span>
          <span className="sw" style={{ background: "var(--text-secondary)" }} /> Mensch
        </span>
        <span>
          <span className="sw" style={{ background: "var(--text-accent)" }} /> Agent
        </span>
        <span className="muted">Knoten sind anklickbar — Menschen öffnen ihre Profil-Seite</span>
      </div>

      {roots.length === 0 ? (
        <p className="muted">Keine Personen in dieser Organisation.</p>
      ) : (
        <div className="tree">
          <ul>
            {roots.map((h) => (
              <TreeNode key={h.id} human={h} humans={humans} agents={agents} seen={new Set()} />
            ))}
          </ul>
        </div>
      )}

      {orphanAgents.length > 0 && (
        <>
          <h2 className="text-sm secondary mt-6 mb-2">Ohne Vorgesetzten ({orphanAgents.length})</h2>
          <p className="muted text-xs mb-3">
            Diese Agenten hängen noch nicht im Org-Chart — auf der Agenten-Seite lässt sich ein Vorgesetzter zuordnen.
          </p>
          <div className="flex gap-2 flex-wrap">
            {orphanAgents.map((a) => (
              <AgentNode key={a.id} agent={a} />
            ))}
          </div>
        </>
      )}
    </div>
  );
}

// seen schützt gegen Zyklen in Altdaten — der Server verhindert neue.
function TreeNode({
  human,
  humans,
  agents,
  seen,
}: {
  human: Human;
  humans: Human[];
  agents: Agent[];
  seen: Set<string>;
}) {
  const nextSeen = new Set(seen).add(human.id);
  const childHumans = humans.filter((h) => h.manager_id === human.id && !nextSeen.has(h.id));
  const childAgents = agents.filter((a) => a.supervisor_id === human.id);
  const hasKids = childHumans.length > 0 || childAgents.length > 0;

  return (
    <li>
      <Link to={`/people/${human.id}`} className="node human" title="Profil-Seite öffnen">
        <Avatar name={human.display_name} human />
        <div>
          <div className="nm">{human.display_name}</div>
          <div className="rl">{human.job_title || (roleLabel[human.role] ?? human.role)}</div>
        </div>
        <span className="ntag">Mensch</span>
      </Link>
      {hasKids && (
        <ul>
          {childHumans.map((h) => (
            <TreeNode key={h.id} human={h} humans={humans} agents={agents} seen={nextSeen} />
          ))}
          {childAgents.map((a) => (
            <li key={a.id}>
              <AgentNode agent={a} />
            </li>
          ))}
        </ul>
      )}
    </li>
  );
}

function AgentNode({ agent }: { agent: Agent }) {
  const status = agent.killed ? "killed" : agent.status;
  return (
    <Link to={`/agents/${agent.id}`} className="node agent">
      <Avatar name={agent.display_name} />
      <div>
        <div className="nm">{agent.display_name}</div>
        <div className="rl mono">{agent.slug}</div>
      </div>
      <span className={`badge st-${status}`}>{agent.killed ? "gestoppt" : statusLabel[agent.status] ?? agent.status}</span>
    </Link>
  );
}

import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { api, roleLabel, type Agent, type Human, type OrgChart } from "../api";
import { Avatar } from "../components/person";

export default function Org() {
  const { t } = useTranslation();
  const chart = useQuery({
    queryKey: ["orgchart"],
    queryFn: () => api<OrgChart>("/org/chart"),
  });

  if (chart.isLoading) return null;
  if (chart.isError) return <p className="danger-text">{t("org.loadError")}</p>;
  const { humans, agents } = chart.data!;

  const humanIds = new Set(humans.map((h) => h.id));
  const roots = humans.filter((h) => !h.manager_id || !humanIds.has(h.manager_id));
  const orphanAgents = agents.filter((a) => !a.supervisor_id || !humanIds.has(a.supervisor_id));

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">{t("org.title")}</h1>
        <span className="muted">{t("org.subtitle")}</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        {t("org.desc")}
      </p>

      <div className="org-legend">
        <span>
          <span className="sw" style={{ background: "var(--text-secondary)" }} /> {t("org.legendHuman")}
        </span>
        <span>
          <span className="sw" style={{ background: "var(--text-accent)" }} /> {t("org.legendAgent")}
        </span>
        <span className="muted">{t("org.legendHint")}</span>
      </div>

      {roots.length === 0 ? (
        <p className="muted">{t("org.noPersons")}</p>
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
          <h2 className="text-sm secondary mt-6 mb-2">{t("org.withoutSupervisor", { count: orphanAgents.length })}</h2>
          <p className="muted text-xs mb-3">
            {t("org.withoutSupervisorHint")}
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
  const { t } = useTranslation();
  const nextSeen = new Set(seen).add(human.id);
  const childHumans = humans.filter((h) => h.manager_id === human.id && !nextSeen.has(h.id));
  const childAgents = agents.filter((a) => a.supervisor_id === human.id);
  const hasKids = childHumans.length > 0 || childAgents.length > 0;

  return (
    <li>
      <Link to={`/people/${human.id}`} className="node human" title={t("org.openProfile")}>
        <Avatar name={human.display_name} human />
        <div>
          <div className="nm">{human.display_name}</div>
          <div className="rl">{human.job_title || (roleLabel[human.role] ?? human.role)}</div>
        </div>
        <span className="ntag">{t("org.nodeHuman")}</span>
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
  const { t } = useTranslation();
  const status = agent.killed ? "killed" : agent.status;
  return (
    <Link to={`/agents/${agent.id}`} className="node agent">
      <Avatar name={agent.display_name} />
      <div>
        <div className="nm">{agent.display_name}</div>
        <div className="rl mono">{agent.slug}</div>
      </div>
      <span className={`badge st-${status}`}>{t(`status.${status}`, status)}</span>
    </Link>
  );
}

import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { api, type Human, type OrgChart, type Principal } from "../api";
import AccountSettings from "../components/AccountSettings";
import { Avatar, PersonLink } from "../components/person";
import ProfileForm from "../components/ProfileForm";

export default function PersonPage({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const person = useQuery({ queryKey: ["human", id], queryFn: () => api<Human>(`/org/humans/${id}`) });
  const chart = useQuery({ queryKey: ["orgchart"], queryFn: () => api<OrgChart>("/org/chart") });

  if (person.isLoading) return null;
  if (person.isError || !person.data) return <p className="danger-text">{t("person.notFound")}</p>;
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
          {t("person.breadcrumb")}
        </Link>{" "}
        / <b style={{ color: "var(--text-primary)", fontWeight: 500 }}>{h.display_name}</b>
      </div>

      <div className="flex items-center gap-3 mb-5 flex-wrap">
        <Avatar name={h.display_name} size={44} human />
        <div>
          <h1 className="text-[22px] m-0">
            {h.display_name}
            {isSelf && <span className="muted text-sm"> {t("person.self")}</span>}
          </h1>
          <div className="muted text-xs">
            {h.job_title || t(`role.${h.role}`, h.role)} · <span className="mono">{h.email}</span>
          </div>
        </div>
        <span className="ntag" style={{ marginLeft: "auto" }}>
          {t(`role.${h.role}`, h.role)}
        </span>
      </div>

      <div className="card mb-4">
        <div className="text-sm font-medium mb-2">{t("person.profile")}</div>
        <ProfileForm human={h} endpoint={isAdmin ? `/users/${h.id}` : "/auth/me"} readOnly={!isAdmin && !isSelf} />
        {!isAdmin && !isSelf && (
          <p className="muted text-xs mt-2 mb-0">{t("person.readOnlyHint")}</p>
        )}
      </div>

      <div className="card">
        <div className="text-sm font-medium mb-2">{t("person.orgChart")}</div>
        <div className="text-xs">
          <div className="flex gap-2 py-0.5">
            <span className="muted" style={{ minWidth: 110 }}>{t("person.reportsTo")}</span>
            {manager ? <PersonLink human={manager} /> : <span className="muted">{t("person.nobody")}</span>}
          </div>
          <div className="flex gap-2 py-0.5">
            <span className="muted" style={{ minWidth: 110 }}>{t("person.directReports")}</span>
            {reports.length === 0 ? (
              <span className="muted">{t("person.noDirect")}</span>
            ) : (
              <span className="flex gap-2 flex-wrap">
                {reports.map((r) => (
                  <PersonLink key={r.id} human={r} />
                ))}
              </span>
            )}
          </div>
          <div className="flex gap-2 py-0.5">
            <span className="muted" style={{ minWidth: 110 }}>{t("person.supervisedAgents")}</span>
            {ownAgents.length === 0 ? (
              <span className="muted">{t("person.noAgents")}</span>
            ) : (
              <span className="flex gap-2 flex-wrap items-center">
                {ownAgents.map((a) => (
                  <Link key={a.id} to={`/agents/${a.id}`}>
                    {a.display_name}
                    <span
                      className={`badge st-${a.killed ? "killed" : a.status}`}
                      style={{ marginLeft: 4 }}
                    >
                      {t(`status.${a.killed ? "killed" : a.status}`, a.killed ? "killed" : a.status)}
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

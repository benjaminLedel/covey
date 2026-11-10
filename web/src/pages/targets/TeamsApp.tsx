import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, post, put, type Agent, type Principal } from "../../api";

// The installation's own Teams app (#710): one app for every organisation,
// so connecting Teams is a sign-in and an upload, not an Azure registration
// per agent.

type TeamsAppStatus = {
  available: boolean;
  app_id?: string;
  can_link?: boolean;
  tenants?: { tenant_id: string; linked_by: string }[];
  teams?: { tenant_id: string; team_id: string; name: string }[];
  channel_agents?: Record<string, string>;
};

type ChannelRef = { team: string; channel: string; channel_id: string; tenant_id: string };

const outcomes = ["linked", "taken", "not_admin", "error"] as const;

export function TeamsAppPanel({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["teamsapp"], queryFn: () => api<TeamsAppStatus>("/teamsapp") });
  const [showChannels, setShowChannels] = useState(false);
  const isAdmin = me.Role === "org_admin";

  const params = new URLSearchParams(window.location.search);
  const outcome = params.get("teams");
  const detail = params.get("detail");

  const link = useMutation({
    mutationFn: () => post<{ url: string }>("/teamsapp/link"),
    onSuccess: (r) => {
      window.location.href = r.url;
    },
  });
  const unlink = useMutation({
    mutationFn: (tenant: string) => del(`/teamsapp/tenants/${encodeURIComponent(tenant)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["teamsapp"] }),
  });

  const s = status.data;
  if (!s?.available) return null;
  const tenants = s.tenants ?? [];

  return (
    <section className="card mb-4" aria-labelledby="teamsapp-title" style={{ padding: 16 }}>
      <h2 id="teamsapp-title" className="text-[16px] mb-1">
        {t("teamsApp.title")}
      </h2>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        {t("teamsApp.desc")}
      </p>

      {outcome && (outcomes as readonly string[]).includes(outcome) && (
        <p className={`text-sm mb-3${outcome === "linked" ? "" : " err"}`} role="status">
          {t(`teamsApp.outcome.${outcome}`)}
          {outcome === "error" && detail ? ` ${detail}` : ""}
        </p>
      )}

      {tenants.length === 0 ? (
        <p className="text-sm mb-3">{t("teamsApp.notLinked")}</p>
      ) : (
        <ul className="text-sm mb-3">
          {tenants.map((tn) => (
            <li key={tn.tenant_id} className="flex items-center gap-2">
              <span>
                {t("teamsApp.linkedTenant", { tenant: tn.tenant_id })}
                {tn.linked_by && <span className="muted"> · {t("teamsApp.linkedBy", { who: tn.linked_by })}</span>}
              </span>
              {isAdmin && (
                <button
                  className="btn sm ghost"
                  disabled={unlink.isPending}
                  onClick={() => unlink.mutate(tn.tenant_id)}
                >
                  {t("teamsApp.unlink")}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}

      <div className="flex gap-2 flex-wrap">
        {isAdmin && (
          <button
            className="btn sm"
            disabled={link.isPending || !s.can_link}
            title={s.can_link ? undefined : t("teamsApp.noSiteURL")}
            onClick={() => link.mutate()}
          >
            {tenants.length === 0 ? t("teamsApp.connect") : t("teamsApp.connectAnother")}
          </button>
        )}
        <a className="btn sm ghost" href="/api/v1/teamsapp/package" download>
          {t("teamsApp.package")}
        </a>
        {tenants.length > 0 && (
          <button className="btn sm ghost" onClick={() => setShowChannels((v) => !v)}>
            {showChannels ? t("teamsApp.hideChannels") : t("teamsApp.showChannels")}
          </button>
        )}
      </div>
      {link.error && <p className="err text-xs mt-2">{(link.error as Error).message}</p>}
      {!isAdmin && tenants.length === 0 && <p className="muted text-xs mt-2">{t("teamsApp.adminOnly")}</p>}

      <p className="muted text-xs mt-3" style={{ maxWidth: 640 }}>
        {t("teamsApp.howTo")}
      </p>

      {showChannels && <Channels defaults={s.channel_agents ?? {}} />}
    </section>
  );
}

function Channels({ defaults }: { defaults: Record<string, string> }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const channels = useQuery({ queryKey: ["teamsapp-channels"], queryFn: () => api<ChannelRef[]>("/teamsapp/channels") });
  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[]>("/agents") });
  const set = useMutation({
    mutationFn: (v: { tenant_id: string; channel_id: string; agent_id: string | null }) =>
      put("/teamsapp/channel-agent", v),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["teamsapp"] }),
  });

  if (channels.isLoading) return <p className="muted text-xs mt-3">{t("teamsApp.loading")}</p>;
  if (channels.error) return <p className="err text-xs mt-3">{(channels.error as Error).message}</p>;
  const list = channels.data ?? [];
  if (list.length === 0) return <p className="muted text-xs mt-3">{t("teamsApp.noChannels")}</p>;

  return (
    <table className="tbl mt-3 text-sm">
      <thead>
        <tr>
          <th>{t("teamsApp.channel")}</th>
          <th>{t("teamsApp.defaultAgent")}</th>
        </tr>
      </thead>
      <tbody>
        {list.map((c) => (
          <tr key={c.channel_id}>
            <td>
              {c.team} / {c.channel}
            </td>
            <td>
              <select
                aria-label={t("teamsApp.defaultAgentFor", { channel: c.channel })}
                value={defaults[c.channel_id] ?? ""}
                disabled={set.isPending}
                onChange={(e) =>
                  set.mutate({ tenant_id: c.tenant_id, channel_id: c.channel_id, agent_id: e.target.value || null })
                }
              >
                <option value="">{t("teamsApp.nobody")}</option>
                {(agents.data ?? []).map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.display_name || a.slug}
                  </option>
                ))}
              </select>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

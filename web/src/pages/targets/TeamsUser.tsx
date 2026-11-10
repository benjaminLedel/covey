import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, post, type Agent, type Principal } from "../../api";

// Teams as a person (#712): a person lets one agent work in their own Teams.

type Status = {
  available: boolean;
  can_delegate?: boolean;
  me?: string;
  delegations?: { agent_id: string; human_id?: string; ms_user: string; since: string }[];
};

export function TeamsUserPanel({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["teamsuser"], queryFn: () => api<Status>("/teamsuser") });
  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[]>("/agents") });
  const [agentID, setAgentID] = useState("");
  const outcome = new URLSearchParams(window.location.search).get("teamsuser");
  const detail = new URLSearchParams(window.location.search).get("detail");

  const start = useMutation({
    mutationFn: (agent_id: string) => post<{ url: string }>("/teamsuser/delegate", { agent_id }),
    onSuccess: (r) => {
      window.location.href = r.url;
    },
  });
  const revoke = useMutation({
    mutationFn: (agent: string) => del(`/teamsuser/delegations/${agent}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["teamsuser"] }),
  });

  const s = status.data;
  if (!s?.available) return null;
  const names = new Map((agents.data ?? []).map((a) => [a.id, a.display_name || a.slug]));
  const canManage = me.Role === "org_admin" || me.Role === "agent_owner";

  return (
    <section className="card mb-4" aria-labelledby="teamsuser-title" style={{ padding: 16 }}>
      <h2 id="teamsuser-title" className="text-[16px] mb-1">
        {t("teamsUser.title")}
      </h2>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        {t("teamsUser.desc")}
      </p>
      {outcome === "delegated" && <p className="text-sm mb-3" role="status">{t("teamsUser.delegated")}</p>}
      {outcome === "error" && (
        <p className="err text-sm mb-3" role="status">
          {t("teamsUser.failed")} {detail}
        </p>
      )}

      {(s.delegations ?? []).length > 0 && (
        <ul className="text-sm mb-3">
          {(s.delegations ?? []).map((d) => (
            <li key={d.agent_id} className="flex items-center gap-2">
              <span>
                {t("teamsUser.actsAs", { agent: names.get(d.agent_id) ?? d.agent_id, who: d.ms_user })}
              </span>
              {(me.Role === "org_admin" || d.human_id === s.me) && (
                <button className="btn sm ghost" disabled={revoke.isPending} onClick={() => revoke.mutate(d.agent_id)}>
                  {t("teamsUser.revoke")}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}

      {canManage && (
        <div className="flex gap-2 flex-wrap items-center">
          <select
            aria-label={t("teamsUser.agent")}
            value={agentID}
            onChange={(e) => setAgentID(e.target.value)}
          >
            <option value="">{t("teamsUser.chooseAgent")}</option>
            {(agents.data ?? []).map((a) => (
              <option key={a.id} value={a.id}>
                {a.display_name || a.slug}
              </option>
            ))}
          </select>
          <button
            className="btn sm"
            disabled={!agentID || start.isPending || !s.can_delegate}
            title={s.can_delegate ? undefined : t("teamsApp.noSiteURL")}
            onClick={() => start.mutate(agentID)}
          >
            {t("teamsUser.connect")}
          </button>
        </div>
      )}
      {start.error && <p className="err text-xs mt-2">{(start.error as Error).message}</p>}
      <p className="muted text-xs mt-3" style={{ maxWidth: 640 }}>
        {t("teamsUser.note")}
      </p>
    </section>
  );
}

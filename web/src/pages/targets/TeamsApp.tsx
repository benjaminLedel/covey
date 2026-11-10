import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, put, type Agent, type Principal } from "../../api";

// An organisation's own Teams bot (#710, #715): entered here, sealed on the
// server, effective at once. One bot serves every agent of the organisation.

type Bot = {
  id: string;
  name: string;
  app_id: string;
  tenant_id: string;
  sso_connection: string;
  deleg_app_id?: string;
};

type TeamsAppStatus = {
  available: boolean;
  site_configured?: boolean;
  bot?: Bot;
  endpoint?: string;
  redirect_uris?: string[];
  sso_resource?: string;
  teams?: { team_id: string; name: string }[];
  channel_agents?: Record<string, string>;
};

type ChannelRef = { team: string; channel: string; channel_id: string };

const empty = { name: "", app_id: "", tenant_id: "", secret: "", sso_connection: "", deleg_app_id: "", deleg_secret: "" };

export function TeamsAppPanel({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["teamsapp"], queryFn: () => api<TeamsAppStatus>("/teamsapp") });
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState(empty);
  const [showChannels, setShowChannels] = useState(false);
  const isAdmin = me.Role === "org_admin";

  const save = useMutation({
    mutationFn: () => put<Bot>("/teamsapp/bot", form),
    onSuccess: () => {
      setEditing(false);
      setForm(empty);
      qc.invalidateQueries({ queryKey: ["teamsapp"] });
      qc.invalidateQueries({ queryKey: ["teamsuser"] });
    },
  });
  const remove = useMutation({
    mutationFn: () => del("/teamsapp/bot"),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["teamsapp"] }),
  });

  const s = status.data;
  if (!s?.available) return null;
  const bot = s.bot;

  const startEdit = () => {
    setForm({
      ...empty,
      name: bot?.name ?? "",
      app_id: bot?.app_id ?? "",
      tenant_id: bot?.tenant_id ?? "",
      sso_connection: bot?.sso_connection ?? "",
      deleg_app_id: bot?.deleg_app_id ?? "",
    });
    setEditing(true);
  };
  const field = (key: keyof typeof empty, label: string, opts: { secret?: boolean; hint?: string } = {}) => (
    <label className="block text-xs mb-2">
      <span className="block mb-1">{label}</span>
      <input
        className="w-full"
        type={opts.secret ? "password" : "text"}
        autoComplete="off"
        value={form[key]}
        placeholder={opts.hint}
        onChange={(e) => setForm({ ...form, [key]: e.target.value })}
      />
    </label>
  );

  return (
    <section className="card mb-4" aria-labelledby="teamsapp-title" style={{ padding: 16 }}>
      <h2 id="teamsapp-title" className="text-[16px] mb-1">
        {t("teamsApp.title")}
      </h2>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        {t("teamsApp.desc")}
      </p>

      {!editing && !bot && (
        <p className="text-sm mb-3">{isAdmin ? t("teamsApp.noBot") : t("teamsApp.noBotAsk")}</p>
      )}

      {!editing && bot && (
        <div className="text-sm mb-3">
          <p>{t("teamsApp.botLine", { name: bot.name, app: bot.app_id, tenant: bot.tenant_id })}</p>
          {bot.deleg_app_id && <p className="muted text-xs">{t("teamsApp.delegLine", { app: bot.deleg_app_id })}</p>}
          {s.endpoint && (
            <dl className="text-xs mt-2" style={{ maxWidth: 640 }}>
              <dt className="muted">{t("teamsApp.endpoint")}</dt>
              <dd>
                <code>{s.endpoint}</code>
              </dd>
              <dt className="muted mt-1">{t("teamsApp.redirects")}</dt>
              {(s.redirect_uris ?? []).map((u) => (
                <dd key={u}>
                  <code>{u}</code>
                </dd>
              ))}
              <dt className="muted mt-1">{t("teamsApp.ssoResource")}</dt>
              <dd>
                <code>{s.sso_resource}</code>
              </dd>
            </dl>
          )}
          {!s.site_configured && <p className="err text-xs mt-2">{t("teamsApp.noSiteURL")}</p>}
        </div>
      )}

      {editing && (
        <form
          className="mb-3"
          style={{ maxWidth: 480 }}
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          {field("name", t("teamsApp.fName"), { hint: "covey" })}
          {field("app_id", t("teamsApp.fAppID"))}
          {field("tenant_id", t("teamsApp.fTenant"))}
          {field("secret", t("teamsApp.fSecret"), { secret: true, hint: bot ? t("teamsApp.keepSecret") : undefined })}
          <details className="mb-2">
            <summary className="text-xs">{t("teamsApp.advanced")}</summary>
            <div className="mt-2">
              {field("sso_connection", t("teamsApp.fSSO"), { hint: "covey" })}
              <p className="muted text-xs mb-2">{t("teamsApp.delegHint")}</p>
              {field("deleg_app_id", t("teamsApp.fDelegApp"))}
              {field("deleg_secret", t("teamsApp.fDelegSecret"), {
                secret: true,
                hint: bot?.deleg_app_id ? t("teamsApp.keepSecret") : undefined,
              })}
            </div>
          </details>
          <div className="flex gap-2">
            <button className="btn sm" type="submit" disabled={save.isPending}>
              {save.isPending ? t("teamsApp.checking") : t("teamsApp.save")}
            </button>
            <button className="btn sm ghost" type="button" onClick={() => setEditing(false)}>
              {t("teamsApp.cancel")}
            </button>
          </div>
          {save.error && <p className="err text-xs mt-2">{(save.error as Error).message}</p>}
        </form>
      )}

      {!editing && (
        <div className="flex gap-2 flex-wrap">
          {isAdmin && (
            <button className="btn sm" onClick={startEdit}>
              {bot ? t("teamsApp.edit") : t("teamsApp.setUp")}
            </button>
          )}
          {bot && (
            <>
              <a className="btn sm ghost" href="/api/v1/teamsapp/package" download>
                {t("teamsApp.package")}
              </a>
              <button className="btn sm ghost" onClick={() => setShowChannels((v) => !v)}>
                {showChannels ? t("teamsApp.hideChannels") : t("teamsApp.showChannels")}
              </button>
              {isAdmin && (
                <button className="btn sm ghost" disabled={remove.isPending} onClick={() => remove.mutate()}>
                  {t("teamsApp.remove")}
                </button>
              )}
            </>
          )}
        </div>
      )}

      <p className="muted text-xs mt-3" style={{ maxWidth: 640 }}>
        {t("teamsApp.howTo")}
      </p>

      {showChannels && bot && <Channels defaults={s.channel_agents ?? {}} />}
    </section>
  );
}

function Channels({ defaults }: { defaults: Record<string, string> }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const channels = useQuery({ queryKey: ["teamsapp-channels"], queryFn: () => api<ChannelRef[]>("/teamsapp/channels") });
  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[]>("/agents") });
  const set = useMutation({
    mutationFn: (v: { channel_id: string; agent_id: string | null }) => put("/teamsapp/channel-agent", v),
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
                onChange={(e) => set.mutate({ channel_id: c.channel_id, agent_id: e.target.value || null })}
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

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, put, patch, type Agent, type Principal, type SecretCheck, type SecretPreview } from "../api";

const canEdit = (role: string) => role === "platform_admin" || role === "security";

export default function Secrets({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const keys = useQuery({
    queryKey: ["secrets"],
    queryFn: () => api<SecretPreview[]>("/secrets"),
    retry: false,
  });
  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[]>("/agents") });
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [check, setCheck] = useState<({ key: string } & SecretCheck) | null>(null);

  const save = useMutation({
    mutationFn: () =>
      put<{ ok: boolean; check: SecretCheck }>(`/secrets/${encodeURIComponent(key)}`, { value }),
    onSuccess: (res) => {
      setCheck({ key, ...res.check });
      setKey("");
      setValue("");
      qc.invalidateQueries({ queryKey: ["secrets"] });
    },
  });

  if (keys.isError) {
    return (
      <div>
        <h1 className="text-[22px] mb-3">{t("secrets.title")}</h1>
        <p className="muted">{t("secrets.noAccess", { role: me.Role })}</p>
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">{t("secrets.title")}</h1>
        <span className="muted">{t("secrets.subtitle")}</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        {t("secrets.desc")}
      </p>

      {canEdit(me.Role) && (
        <form
          className="card mb-4 flex gap-3 items-end flex-wrap"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <div className="min-w-48">
            <label>{t("secrets.key")}</label>
            <input value={key} onChange={(e) => setKey(e.target.value)} className="mono" placeholder="zammad_token" required />
          </div>
          <div className="flex-1 min-w-52">
            <label>{t("secrets.value")}</label>
            <input type="password" value={value} onChange={(e) => setValue(e.target.value)} required />
          </div>
          <button className="btn primary" disabled={save.isPending}>
            {t("secrets.save")}
          </button>
          {check && (
            <p
              className="text-xs w-full m-0"
              style={{ color: check.checked && !check.valid ? "var(--danger, #b91c1c)" : check.valid ? "var(--success, #15803d)" : "var(--text-secondary)" }}
            >
              {check.checked && check.valid && t("secrets.savedValid", { key: check.key })}
              {check.checked && !check.valid && t("secrets.savedInvalid", { key: check.key, hint: check.hint })}
              {!check.checked && t("secrets.savedOk", { key: check.key })}
            </p>
          )}
        </form>
      )}

      {(keys.data ?? []).map((s) => (
        <SecretCard key={s.key} secret={s} agents={agents.data ?? []} canEdit={canEdit(me.Role)} />
      ))}
      {keys.data?.length === 0 && <p className="muted">{t("secrets.noSecrets")}</p>}
    </div>
  );
}

function SecretCard({ secret, agents, canEdit }: { secret: SecretPreview; agents: Agent[]; canEdit: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const toggle = useMutation({
    mutationFn: (revealed: boolean) =>
      patch<{ ok: boolean }>(`/secrets/${encodeURIComponent(secret.key)}`, { revealed }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["secrets"] }),
  });
  const remove = useMutation({
    mutationFn: () => del(`/secrets/${encodeURIComponent(secret.key)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["secrets"] }),
  });

  return (
    <div className="card mb-2" style={{ padding: "11px 15px" }}>
      <div className="flex items-center gap-4">
        <span className="mono text-sm flex-1">{secret.key}</span>
        <span className="mono text-xs" style={{ color: secret.revealed ? "var(--text-primary)" : "var(--text-secondary)" }}>
          {secret.revealed && secret.value
            ? secret.value
            : <>{secret.prefix ? <span style={{ color: "var(--text-secondary)" }}>{secret.prefix}</span> : null}••••••••</>}
        </span>
        {canEdit && (
          <>
            <button
              className="btn sm"
              disabled={toggle.isPending}
              onClick={() => toggle.mutate(!secret.revealed)}
              style={{ color: secret.revealed ? "var(--primary, #2563eb)" : undefined }}
            >
              {secret.revealed ? t("secrets.hide") : t("secrets.reveal")}
            </button>
            <button className="btn sm" onClick={() => remove.mutate()}>
              {t("secrets.delete")}
            </button>
          </>
        )}
      </div>
      <Assignments secret={secret} agents={agents} />
    </div>
  );
}

function Assignments({ secret, agents }: { secret: SecretPreview; agents: Agent[] }) {
  const { t } = useTranslation();
  const assigned = secret.agent_ids ?? [];
  const name = (id: string) => agents.find((a) => a.id === id)?.display_name ?? id.slice(0, 8);

  return (
    <div className="flex items-center gap-2 flex-wrap mt-2 text-xs">
      <span className="muted">{t("secrets.assignedTo")}</span>
      {assigned.length === 0 && (
        <span style={{ color: "var(--text-warning, #b45309)" }}>
          {t("secrets.nobody")}
        </span>
      )}
      {assigned.map((id) => (
        <span key={id} className="badge st-triage">
          {name(id)}
        </span>
      ))}
    </div>
  );
}

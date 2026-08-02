import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  api,
  patch,
  del,
  put,
  type SecretCheck,
  type SecretPreview,
} from "../../api";
import { SecretValue } from "../Secrets";

export function AgentSecrets({ agentId }: { agentId: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const own = useQuery({
    queryKey: ["agent-secrets", agentId],
    queryFn: () => api<SecretPreview[]>(`/agents/${agentId}/secrets`),
    retry: false,
  });
  const org = useQuery({ queryKey: ["secrets"], queryFn: () => api<SecretPreview[]>("/secrets"), retry: false });
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [sensitive, setSensitive] = useState(false);
  const [check, setCheck] = useState<({ key: string } & SecretCheck) | null>(null);
  const inval = () => qc.invalidateQueries({ queryKey: ["agent-secrets", agentId] });

  const save = useMutation({
    mutationFn: () =>
      put<{ ok: boolean; check: SecretCheck }>(
        `/agents/${agentId}/secrets/${encodeURIComponent(key)}`,
        { value, sensitive },
      ),
    onSuccess: (res) => {
      setCheck({ key, ...res.check });
      setKey("");
      setValue("");
      setSensitive(false);
      inval();
    },
  });
  const remove = useMutation({
    mutationFn: (k: string) => del(`/agents/${agentId}/secrets/${encodeURIComponent(k)}`),
    onSuccess: inval,
  });
  const protect = useMutation({
    mutationFn: (k: string) =>
      patch(`/agents/${agentId}/secrets/${encodeURIComponent(k)}`, { sensitive: true }),
    onSuccess: inval,
  });
  const invalOrg = () => qc.invalidateQueries({ queryKey: ["secrets"] });
  const assign = useMutation({
    mutationFn: (k: string) => put(`/secrets/${encodeURIComponent(k)}/agents/${agentId}`, {}),
    onSuccess: invalOrg,
  });
  const unassign = useMutation({
    mutationFn: (k: string) => del(`/secrets/${encodeURIComponent(k)}/agents/${agentId}`),
    onSuccess: invalOrg,
  });

  const ownKeys = new Set((own.data ?? []).map((s) => s.key));
  const inherited = (org.data ?? []).filter((s) => s.agent_ids.includes(agentId));
  const assignable = (org.data ?? []).filter((s) => !s.agent_ids.includes(agentId));

  return (
    <div>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        {t("agent.secrets.desc")}
      </p>

      <div className="card mb-4 flex gap-3 items-end flex-wrap">
        <div className="min-w-64">
          <label>{t("agent.secrets.assignOrg")}</label>
          <select
            value=""
            onChange={(e) => {
              if (e.target.value) assign.mutate(e.target.value);
            }}
            disabled={assign.isPending || assignable.length === 0}
          >
            <option value="">
              {assignable.length === 0 ? t("agent.secrets.noMoreOrg") : t("agent.secrets.selectSecret")}
            </option>
            {assignable.map((s) => (
              <option key={s.key} value={s.key}>
                {s.key}
              </option>
            ))}
          </select>
        </div>
        {assign.isError && <span className="danger-text text-xs">{(assign.error as Error).message}</span>}
      </div>

      {inherited.length > 0 && (
        <>
          <label>{t("agent.secrets.assignedOrg")}</label>
          {inherited.map((s) => (
            <div key={s.key} className="card mb-2 flex items-center gap-4" style={{ padding: "11px 15px", opacity: ownKeys.has(s.key) ? 0.55 : 1 }}>
              <span className="mono text-sm flex-1">{s.key}</span>
              {ownKeys.has(s.key) && (
                <span className="muted text-xs">{t("agent.secrets.shadowed")}</span>
              )}
              <SecretValue secret={s} />
              <button className="btn sm" disabled={unassign.isPending} onClick={() => unassign.mutate(s.key)}>
                {t("agent.secrets.removeAssignment")}
              </button>
            </div>
          ))}
        </>
      )}
      {inherited.length === 0 && (
        <p className="muted mb-3" style={{ color: "var(--text-warning, #b45309)" }}>
          {t("agent.secrets.noAssigned")}
        </p>
      )}

      <label className="mt-4">{t("agent.secrets.ownSecrets")}</label>

      <form
        className="card mb-4 flex gap-3 items-end flex-wrap"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <div className="min-w-48">
          <label>{t("agent.secrets.key")}</label>
          <input value={key} onChange={(e) => setKey(e.target.value)} className="mono" placeholder="zammad_token" required />
        </div>
        <div className="flex-1 min-w-52">
          <label>{t("agent.secrets.value")}</label>
          <input type={sensitive ? "password" : "text"} value={value} onChange={(e) => setValue(e.target.value)} required />
        </div>
        <label className="flex items-center gap-2 text-xs" style={{ marginBottom: 7 }}>
          <input type="checkbox" checked={sensitive} onChange={(e) => setSensitive(e.target.checked)} />
          {t("agent.secrets.markSensitive")}
        </label>
        <button className="btn primary" disabled={save.isPending}>
          {t("agent.secrets.save")}
        </button>
        {check && (
          <p
            className="text-xs w-full m-0"
            style={{ color: check.checked && !check.valid ? "var(--danger, #b91c1c)" : check.valid ? "var(--success, #15803d)" : "var(--text-secondary)" }}
          >
            {check.checked && check.valid && t("agent.secrets.savedValid", { key: check.key })}
            {check.checked && !check.valid && t("agent.secrets.savedInvalid", { key: check.key, hint: check.hint })}
            {!check.checked && t("agent.secrets.savedOk", { key: check.key })}
          </p>
        )}
      </form>

      {(own.data ?? []).map((s) => (
        <div key={s.key} className="card mb-2 flex items-center gap-4" style={{ padding: "11px 15px" }}>
          <span className="mono text-sm flex-1">{s.key}</span>
          <span className="badge st-triage">{t("agent.secrets.agentOwn")}</span>
          {s.sensitive && (
            <span className="badge st-blocked" title={t("secrets.sensitiveHint")}>
              {t("secrets.sensitive")}
            </span>
          )}
          <SecretValue secret={s} />
          {!s.sensitive && (
            <button
              className="btn sm"
              disabled={protect.isPending}
              onClick={() => {
                if (confirm(t("secrets.protectConfirm", { key: s.key }))) protect.mutate(s.key);
              }}
            >
              {t("secrets.protect")}
            </button>
          )}
          <button className="btn sm" onClick={() => remove.mutate(s.key)}>
            {t("agent.secrets.delete")}
          </button>
        </div>
      ))}
      {own.data?.length === 0 && <p className="muted mb-3">{t("agent.secrets.noOwn")}</p>}
    </div>
  );
}

// AgentSkills zeigt, was der Agent wirklich an Fähigkeiten hat: seine eigenen
// plus die ihm aus der Bibliothek verlinkten. Genau diese Auflösung
// materialisiert der Daemon vor jedem Lauf nach <home>/.claude/skills/.
//
// Bei Namensgleichheit gewinnt der agent-eigene Skill; der verdeckte
// Bibliotheks-Eintrag taucht deshalb in der oberen Liste nicht auf, wohl aber
// unten bei den Verlinkungen — sonst wüsste niemand, warum das Verlinken nichts

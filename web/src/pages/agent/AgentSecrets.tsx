import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  api,
  patch,
  del,
  put,
  type RuntimeCredential,
  type SecretCheck,
  type SecretPreview,
} from "../../api";
import { RuntimeCredentialBadge, SecretValue } from "../Secrets";
import { isRuntimeCredential } from "../../runtimecred";

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
  const inval = () => {
    qc.invalidateQueries({ queryKey: ["agent-secrets", agentId] });
    qc.invalidateQueries({ queryKey: ["runtime-credential", agentId] });
  };

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
  const invalOrg = () => {
    qc.invalidateQueries({ queryKey: ["secrets"] });
    // Zuweisen und Entziehen verschieben, was der Agent erreicht — und damit
    // auch, was sich auflösen lässt.
    qc.invalidateQueries({ queryKey: ["runtime-credential", agentId] });
  };
  const assign = useMutation({
    mutationFn: (k: string) => put(`/secrets/${encodeURIComponent(k)}/agents/${agentId}`, {}),
    onSuccess: invalOrg,
  });
  const unassign = useMutation({
    mutationFn: (k: string) => del(`/secrets/${encodeURIComponent(k)}/agents/${agentId}`),
    onSuccess: invalOrg,
  });

  // Dieselbe Query wie in RuntimeCredentialPin — react-query fasst sie über den
  // Schlüssel zusammen, es bleibt eine Anfrage. Gebraucht wird sie hier, weil
  // das festgelegte Credential nicht entzogen werden kann: das gehört an die
  // Zeile, nicht in eine Fehlermeldung nach dem Klick.
  const pinned = useQuery({
    queryKey: ["runtime-credential", agentId],
    queryFn: () => api<RuntimeCredential>(`/agents/${agentId}/runtime-credential`),
    retry: false,
  });
  const pinnedKey = pinned.data?.pinned ?? "";

  const ownKeys = new Set((own.data ?? []).map((s) => s.key));
  const inherited = (org.data ?? []).filter((s) => s.agent_ids.includes(agentId));
  const assignable = (org.data ?? []).filter((s) => !s.agent_ids.includes(agentId));

  return (
    <div>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        {t("agent.secrets.desc")}
      </p>

      <RuntimeCredentialPin
        agentId={agentId}
        candidates={[...(own.data ?? []), ...(org.data ?? [])]
          .map((s) => s.key)
          .filter(isRuntimeCredential)}
      />

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
              <RuntimeCredentialBadge secretKey={s.key} />
              {ownKeys.has(s.key) && (
                <span className="muted text-xs">{t("agent.secrets.shadowed")}</span>
              )}
              <SecretValue secret={s} />
              {s.key === pinnedKey ? (
                <span className="muted text-xs" title={t("agent.secrets.pinnedLocksHint")}>
                  {t("agent.secrets.pinnedLocks")}
                </span>
              ) : (
                <button className="btn sm" disabled={unassign.isPending} onClick={() => unassign.mutate(s.key)}>
                  {t("agent.secrets.removeAssignment")}
                </button>
              )}
            </div>
          ))}
          {unassign.isError && (
            <p className="danger-text text-xs mt-1">{(unassign.error as Error).message}</p>
          )}
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
          <RuntimeCredentialBadge secretKey={s.key} />
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
          {s.key === pinnedKey ? (
            <span className="muted text-xs" title={t("agent.secrets.pinnedLocksHint")}>
              {t("agent.secrets.pinnedLocks")}
            </span>
          ) : (
            <button className="btn sm" onClick={() => remove.mutate(s.key)}>
              {t("agent.secrets.delete")}
            </button>
          )}
        </div>
      ))}
      {remove.isError && <p className="danger-text text-xs mt-1">{(remove.error as Error).message}</p>}
      {own.data?.length === 0 && <p className="muted mb-3">{t("agent.secrets.noOwn")}</p>}
    </div>
  );
}

// RuntimeCredentialPin legt fest, mit welchem Anthropic-Credential dieser Agent
// arbeitet — die Wahl entscheidet, welches Konto seine Läufe belastet.
//
// Die Festlegung weist das organisationsweite Secret gleich mit zu; ohne
// Zuweisung erreichte es den Agenten nicht, und die Falle schnappte erst beim
// nächsten Weckruf zu. Bleibt "Standardreihenfolge" stehen, gilt wie bisher:
// API-Key vor Abo-Token.
function RuntimeCredentialPin({ agentId, candidates }: { agentId: string; candidates: string[] }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const cur = useQuery({
    queryKey: ["runtime-credential", agentId],
    queryFn: () => api<RuntimeCredential>(`/agents/${agentId}/runtime-credential`),
    retry: false,
  });
  const [moved, setMoved] = useState("");
  const pin = useMutation({
    mutationFn: (key: string) =>
      patch<{ unassigned: string }>(`/agents/${agentId}/runtime-credential`, { key }),
    onSuccess: (res) => {
      // Das Umstellen nimmt die Zuweisung des vorigen Credentials mit. Das
      // stillschweigend zu tun wäre falsch — es ist ein Entzug.
      setMoved(res.unassigned ?? "");
      qc.invalidateQueries({ queryKey: ["runtime-credential", agentId] });
      qc.invalidateQueries({ queryKey: ["secrets"] });
      qc.invalidateQueries({ queryKey: ["agent", agentId] });
    },
  });

  // Eigene und organisationsweite Namen können sich decken — einmal reicht.
  const options = [...new Set(candidates)].sort();
  const c = cur.data;
  // Zeigt die Festlegung ins Leere, muss das hier stehen: von außen sieht ein
  // toter Pin genauso aus wie ein kaputtes Token.
  const dead = !!c?.pinned && !c.resolvable;

  return (
    <div className="card mb-4">
      <div className="flex gap-3 items-end flex-wrap">
        <div className="min-w-64">
          <label>{t("agent.secrets.runtimeCred")}</label>
          <select
            value={c?.pinned ?? ""}
            onChange={(e) => pin.mutate(e.target.value)}
            disabled={pin.isPending || cur.isLoading}
          >
            <option value="">{t("agent.secrets.runtimeCredDefault")}</option>
            {options.map((k) => (
              <option key={k} value={k}>
                {k}
              </option>
            ))}
            {/* Eine Festlegung auf ein inzwischen entzogenes Secret bliebe sonst
                unsichtbar, weil sie in keiner Liste mehr vorkommt. */}
            {c?.pinned && !options.includes(c.pinned) && (
              <option value={c.pinned}>{c.pinned}</option>
            )}
          </select>
        </div>
        {c && !dead && (
          <p className="muted text-xs m-0" style={{ marginBottom: 7 }}>
            {c.resolvable
              ? t("agent.secrets.runtimeCredEffective", { key: c.effective_key, env: c.env_var })
              : t("agent.secrets.runtimeCredNone")}
          </p>
        )}
      </div>
      <p className="muted text-xs mt-2 mb-0" style={{ maxWidth: 640 }}>
        {t("agent.secrets.runtimeCredDesc")} {t("agent.secrets.runtimeCredMoveNote")}
      </p>
      {dead && (
        <p className="text-xs mt-2 mb-0" style={{ color: "var(--danger, #b91c1c)" }}>
          {t("agent.secrets.runtimeCredDead", { key: c!.pinned })}
        </p>
      )}
      {moved && <p className="muted text-xs mt-2 mb-0">{t("agent.secrets.runtimeCredMoved", { key: moved })}</p>}
      {pin.isError && <p className="danger-text text-xs mt-2 mb-0">{(pin.error as Error).message}</p>}
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

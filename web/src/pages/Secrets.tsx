import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  api,
  del,
  patch,
  post,
  put,
  type Agent,
  type Principal,
  type SecretCheck,
  type SecretLifetime as Lifetime,
  type SecretPreview,
} from "../api";

const canEdit = (role: string) => role === "org_admin" || role === "security";

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
  const [sensitive, setSensitive] = useState(false);
  const [check, setCheck] = useState<({ key: string } & SecretCheck) | null>(null);

  const save = useMutation({
    mutationFn: () =>
      put<{ ok: boolean; check: SecretCheck }>(`/secrets/${encodeURIComponent(key)}`, { value, sensitive }),
    onSuccess: (res) => {
      setCheck({ key, ...res.check });
      setKey("");
      setValue("");
      setSensitive(false);
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
            <input type={sensitive ? "password" : "text"} value={value} onChange={(e) => setValue(e.target.value)} required />
          </div>
          <label className="flex items-center gap-2 text-xs" style={{ marginBottom: 7 }}>
            <input type="checkbox" checked={sensitive} onChange={(e) => setSensitive(e.target.checked)} />
            {t("secrets.markSensitive")}
          </label>
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
  const protect = useMutation({
    mutationFn: () =>
      patch<{ ok: boolean }>(`/secrets/${encodeURIComponent(secret.key)}`, { sensitive: true }),
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
        {secret.sensitive && (
          <span className="badge st-blocked" title={t("secrets.sensitiveHint")}>
            {t("secrets.sensitive")}
          </span>
        )}
        <SecretValue secret={secret} />
        {canEdit && (
          <>
            {!secret.sensitive && (
              <button
                className="btn sm"
                disabled={protect.isPending}
                onClick={() => {
                  if (confirm(t("secrets.protectConfirm", { key: secret.key }))) protect.mutate();
                }}
              >
                {t("secrets.protect")}
              </button>
            )}
            <button className="btn sm" onClick={() => remove.mutate()}>
              {t("secrets.delete")}
            </button>
          </>
        )}
      </div>
      <SecretLifetimeLine life={secret} path={`/secrets/${encodeURIComponent(secret.key)}`} canEdit={canEdit} />
      <Assignments secret={secret} agents={agents} />
      <Pool secret={secret} canEdit={canEdit} />
    </div>
  );
}

// Fortnight before the date the platform starts saying so — the same
// fortnight the lint and the daily check use (#176).
const WARN_AHEAD_MS = 14 * 24 * 3600 * 1000;

// Das Leben eines Werts: abgewiesen, abgelaufen, läuft bald ab, zuletzt
// geprüft — und das Datum, das ein Mensch einträgt, wo das Zielsystem es
// nicht selbst sagt (Jira Cloud). Nichts davon steht, solange nichts bekannt
// ist: ein Wert ohne Befund trägt keine Zeile.
export function SecretLifetimeLine({
  life,
  path,
  canEdit,
  queryKey = ["secrets"],
}: {
  life: Lifetime;
  path: string;
  canEdit: boolean;
  queryKey?: unknown[];
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const setExpiry = useMutation({
    mutationFn: (expires_at: string) => patch<{ ok: boolean }>(path, { expires_at }),
    onSuccess: () => {
      setEditing(false);
      qc.invalidateQueries({ queryKey });
    },
  });

  const day = (iso: string) => new Date(iso).toLocaleDateString();
  const now = Date.now();
  const expiresAt = life.expires_at ? new Date(life.expires_at).getTime() : null;
  const rejected = !!life.rejected_at;
  const expired = expiresAt !== null && expiresAt <= now;
  const expiring = expiresAt !== null && !expired && expiresAt - now < WARN_AHEAD_MS;
  const checkFailed = !!life.probe_error && !rejected;
  const nothing = !rejected && expiresAt === null && !life.probed_at && !canEdit;
  if (nothing) return null;

  return (
    <div className="flex items-center gap-2 mt-2 text-xs flex-wrap">
      {rejected && (
        <span className="badge st-failed" title={life.rejected_reason}>
          {t("secrets.life.rejected", { date: day(life.rejected_at!) })}
        </span>
      )}
      {expired && !rejected && (
        <span className="badge st-failed">{t("secrets.life.expired", { date: day(life.expires_at!) })}</span>
      )}
      {expiring && !rejected && (
        <span className="badge st-blocked">{t("secrets.life.expiring", { date: day(life.expires_at!) })}</span>
      )}
      {checkFailed && (
        <span className="badge st-blocked" title={life.probe_error}>
          {t("secrets.life.checkFailed")}
        </span>
      )}
      {expiresAt !== null && !expired && !expiring && (
        <span className="muted">{t("secrets.life.expiresOn", { date: day(life.expires_at!) })}</span>
      )}
      {life.rotatable && <span className="muted">· {t("secrets.life.renewed")}</span>}
      {life.probed_at && !life.probe_error && (
        <span className="muted">
          · {t("secrets.life.checked", { date: day(life.probed_at), identity: life.probe_identity || "—" })}
        </span>
      )}
      {canEdit && !life.rotatable && (
        <span className="ml-auto flex items-center gap-2">
          {editing ? (
            <input
              type="date"
              style={{ width: "auto", padding: "2px 6px", fontSize: 12 }}
              autoFocus
              defaultValue={life.expires_at ? life.expires_at.slice(0, 10) : ""}
              onBlur={(e) => {
                const v = e.target.value;
                if (v !== (life.expires_at ? life.expires_at.slice(0, 10) : "")) setExpiry.mutate(v);
                else setEditing(false);
              }}
              onKeyDown={(e) => {
                if (e.key === "Escape") setEditing(false);
                if (e.key === "Enter") (e.target as HTMLInputElement).blur();
              }}
            />
          ) : (
            <button className="btn sm" title={t("secrets.life.expiryHint")} onClick={() => setEditing(true)}>
              {expiresAt !== null ? t("secrets.life.changeExpiry") : t("secrets.life.setExpiry")}
            </button>
          )}
        </span>
      )}
    </div>
  );
}

// Die Werte eines Schlüssels.
//
// Eingeklappt, solange es nur einen gibt — das ist der Normalfall, und eine
// Liste mit einem Eintrag ist keine Liste. Was mit den Werten GESCHIEHT (wer
// darauf sitzt, was sie verbrauchen dürfen) steht bei den Arbeitsplätzen: hier
// ist nur der Speicher (spec/18).
function Pool({ secret, canEdit }: { secret: SecretPreview; canEdit: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const values = secret.values ?? [];
  const [open, setOpen] = useState(values.length > 1);
  const path = `/secrets/${encodeURIComponent(secret.key)}`;
  const invalidate = () => qc.invalidateQueries({ queryKey: ["secrets"] });

  const [adding, setAdding] = useState(false);
  const [value, setValue] = useState("");
  const add = useMutation({
    mutationFn: () => post<{ ok: boolean; slot: number; check: SecretCheck }>(`${path}/values`, { value }),
    onSuccess: () => {
      setValue("");
      setAdding(false);
      invalidate();
    },
  });
  const remove = useMutation({
    mutationFn: (slot: number) => del(`${path}/values/${slot}`),
    onSuccess: invalidate,
  });

  return (
    <div className="mt-2 text-xs">
      <button className="btn sm" onClick={() => setOpen(!open)}>
        {open ? "▾" : "▸"} {t("secrets.pool.values", { count: values.length })}
      </button>

      {open && (
        <div className="mt-2">
          {values.map((v) => (
            <div
              key={v.slot}
              className="flex items-center gap-3 mt-1"
              style={{ borderTop: "1px solid var(--border)", paddingTop: 6 }}
            >
              <span className="mono" style={{ color: "var(--text-secondary)", minWidth: 90 }}>
                {v.sensitive ? `${v.prefix || ""}••••••••` : v.value}
              </span>
              <span className="flex-1 muted">#{v.slot}</span>
              {canEdit && values.length > 1 && (
                <button
                  className="btn sm"
                  onClick={() => {
                    if (confirm(t("secrets.pool.deleteConfirm", { slot: v.slot }))) remove.mutate(v.slot);
                  }}
                >
                  {t("secrets.delete")}
                </button>
              )}
            </div>
          ))}
          <p className="muted mt-2 mb-0">{t("secrets.pool.capacityHint")}</p>

          {canEdit &&
            (adding ? (
              <form
                className="flex gap-2 items-end flex-wrap mt-2"
                onSubmit={(e) => {
                  e.preventDefault();
                  add.mutate();
                }}
              >
                <div className="flex-1 min-w-52">
                  <label htmlFor={`pool-value-${secret.key}`}>{t("secrets.pool.newValue")}</label>
                  <input
                    id={`pool-value-${secret.key}`}
                    type={secret.sensitive ? "password" : "text"}
                    value={value}
                    onChange={(e) => setValue(e.target.value)}
                    required
                  />
                </div>
                <button className="btn primary sm" disabled={add.isPending}>
                  {t("secrets.pool.add")}
                </button>
                <button type="button" className="btn sm" onClick={() => setAdding(false)}>
                  {t("secrets.pool.cancel")}
                </button>
              </form>
            ) : (
              <button className="btn sm mt-2" onClick={() => setAdding(true)}>
                {t("secrets.pool.addValue")}
              </button>
            ))}
        </div>
      )}
    </div>
  );
}


// SecretValue zeigt Variablen im Klartext, sensible Secrets nur als
// Präfix + Maske.
export function SecretValue({ secret }: { secret: SecretPreview }) {
  if (!secret.sensitive && secret.value !== undefined) {
    return <span className="mono text-xs">{secret.value}</span>;
  }
  return (
    <span className="mono text-xs" style={{ color: "var(--text-secondary)" }}>
      {secret.prefix || null}••••••••
    </span>
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

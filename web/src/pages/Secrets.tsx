import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  api,
  del,
  post,
  put,
  patch,
  type Agent,
  type Principal,
  type SecretCheck,
  type SecretLimit,
  type SecretPool,
  type SecretPreview,
} from "../api";

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
      <Assignments secret={secret} agents={agents} />
      <Pool secret={secret} agents={agents} canEdit={canEdit} />
    </div>
  );
}

// Pool: mehrere Werte unter einem Schlüssel.
//
// Eingeklappt, solange es nur einen Wert gibt — das ist der Normalfall, und
// eine Liste mit einem Eintrag ist keine Liste. Sobald ein zweiter dazukommt,
// steht die Auslastung im Vordergrund: sie ist die Zahl, an der man abliest, ob
// ein Sitz zu wenig oder einer zu viel ist.
function Pool({ secret, agents, canEdit }: { secret: SecretPreview; agents: Agent[]; canEdit: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const count = secret.values?.length ?? 1;
  const [open, setOpen] = useState(count > 1);
  const path = `/secrets/${encodeURIComponent(secret.key)}`;

  const pool = useQuery({
    queryKey: ["secret-pool", secret.key],
    queryFn: () => api<SecretPool>(`${path}/pool`),
    enabled: open,
  });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["secret-pool", secret.key] });
    qc.invalidateQueries({ queryKey: ["secrets"] });
  };

  const [adding, setAdding] = useState(false);
  const [value, setValue] = useState("");
  const [label, setLabel] = useState("");
  const add = useMutation({
    mutationFn: () => post<{ ok: boolean; slot: number; check: SecretCheck }>(`${path}/values`, { value, label }),
    onSuccess: () => {
      setValue("");
      setLabel("");
      setAdding(false);
      invalidate();
    },
  });

  const name = (id: string) => agents.find((a) => a.id === id)?.display_name ?? id.slice(0, 8);
  const seats = (slot: number) => (pool.data?.bindings ?? []).filter((b) => b.slot === slot);

  return (
    <div className="mt-2 text-xs">
      <button className="btn sm" onClick={() => setOpen(!open)}>
        {open ? "▾" : "▸"} {t("secrets.pool.values", { count })}
      </button>

      {open && (
        <div className="mt-2">
          {(pool.data?.values ?? []).map((v) => (
            <PoolValueRow
              key={v.slot}
              secretKey={secret.key}
              value={v}
              seats={seats(v.slot).map((b) => ({ name: name(b.agent_id), reason: b.reason }))}
              canEdit={canEdit}
              deletable={(pool.data?.values.length ?? 0) > 1}
              onChanged={invalidate}
            />
          ))}

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
                <div className="min-w-40">
                  <label htmlFor={`pool-label-${secret.key}`}>{t("secrets.pool.label")}</label>
                  <input
                    id={`pool-label-${secret.key}`}
                    value={label}
                    onChange={(e) => setLabel(e.target.value)}
                    placeholder={t("secrets.pool.labelHint")}
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

function PoolValueRow({
  secretKey,
  value: v,
  seats,
  canEdit,
  deletable,
  onChanged,
}: {
  secretKey: string;
  value: SecretPool["values"][number];
  seats: { name: string; reason: string }[];
  canEdit: boolean;
  deletable: boolean;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const path = `/secrets/${encodeURIComponent(secretKey)}/values/${v.slot}`;
  const [editing, setEditing] = useState(false);

  const setLimit = useMutation({
    mutationFn: (limit: SecretLimit) => patch<{ ok: boolean }>(path, { limit }),
    onSuccess: () => {
      setEditing(false);
      onChanged();
    },
  });
  const release = useMutation({
    mutationFn: () => patch<{ ok: boolean }>(path, { cooldown: false }),
    onSuccess: onChanged,
  });
  const remove = useMutation({ mutationFn: () => del(path), onSuccess: onChanged });

  const used = v.limit.unit === "tokens" ? v.usage.tokens : v.usage.usd;
  const share = v.limit.window_secs > 0 && v.limit.amount > 0 ? Math.min(1, used / v.limit.amount) : 0;
  const parked = !!v.cooldown_until && new Date(v.cooldown_until) > new Date();

  return (
    <div className="mt-2" style={{ borderTop: "1px solid var(--border)", paddingTop: 8 }}>
      <div className="flex items-center gap-3 flex-wrap">
        <span className="mono" style={{ color: "var(--text-secondary)", minWidth: 90 }}>
          {v.sensitive ? `${v.prefix || ""}••••••••` : v.value}
        </span>
        <span className="flex-1">{v.label || t("secrets.pool.unnamed", { slot: v.slot })}</span>
        {parked && (
          <span className="badge st-blocked" title={v.cooldown_reason}>
            {t("secrets.pool.parked", { until: new Date(v.cooldown_until!).toLocaleString() })}
          </span>
        )}
        {seats.map((s) => (
          <span key={s.name} className="badge st-triage" title={t(`secrets.pool.reason.${s.reason}`)}>
            {s.name}
          </span>
        ))}
        {canEdit && (
          <>
            {parked && (
              <button className="btn sm" onClick={() => release.mutate()}>
                {t("secrets.pool.release")}
              </button>
            )}
            <button className="btn sm" onClick={() => setEditing(!editing)}>
              {t("secrets.pool.limit")}
            </button>
            {deletable && (
              <button
                className="btn sm"
                onClick={() => {
                  if (confirm(t("secrets.pool.deleteConfirm", { slot: v.slot }))) remove.mutate();
                }}
              >
                {t("secrets.delete")}
              </button>
            )}
          </>
        )}
      </div>

      {/* Auslastung: gegen das Limit, wenn eines gesetzt ist — sonst der nackte
          Verbrauch im Anzeigefenster. Eine Prozentzahl ohne Limit wäre erfunden. */}
      <div className="flex items-center gap-2 mt-1">
        {v.limit.window_secs > 0 ? (
          <>
            <div style={{ flex: 1, height: 5, background: "var(--border)", borderRadius: 3 }}>
              <div
                style={{
                  width: `${share * 100}%`,
                  height: "100%",
                  borderRadius: 3,
                  background: share >= 1 ? "var(--danger, #b91c1c)" : share > 0.8 ? "var(--warning, #b45309)" : "var(--accent, #2563eb)",
                }}
              />
            </div>
            <span className="muted">
              {t("secrets.pool.usedOfLimit", {
                used: fmtAmount(used, v.limit.unit),
                limit: fmtAmount(v.limit.amount, v.limit.unit),
                window: fmtWindow(v.window_secs),
              })}
            </span>
          </>
        ) : (
          <span className="muted">
            {t("secrets.pool.usedNoLimit", {
              usd: v.usage.usd.toFixed(2),
              tokens: v.usage.tokens.toLocaleString(),
              window: fmtWindow(v.window_secs),
            })}
          </span>
        )}
      </div>

      {editing && (
        <LimitForm
          id={`limit-${secretKey}-${v.slot}`}
          limit={v.limit}
          pending={setLimit.isPending}
          onSave={(l) => setLimit.mutate(l)}
        />
      )}
    </div>
  );
}

function LimitForm({
  id,
  limit,
  pending,
  onSave,
}: {
  id: string;
  limit: SecretLimit;
  pending: boolean;
  onSave: (l: SecretLimit) => void;
}) {
  const { t } = useTranslation();
  const [amount, setAmount] = useState(String(limit.amount || ""));
  const [unit, setUnit] = useState<SecretLimit["unit"]>(limit.unit || "usd");
  const [hours, setHours] = useState(String(limit.window_secs ? limit.window_secs / 3600 : 5));

  return (
    <form
      className="flex gap-2 items-end flex-wrap mt-2"
      onSubmit={(e) => {
        e.preventDefault();
        onSave({ amount: Number(amount) || 0, unit, window_secs: Math.round((Number(hours) || 0) * 3600) });
      }}
    >
      <div style={{ width: 110 }}>
        <label htmlFor={`${id}-amount`}>{t("secrets.pool.amount")}</label>
        <input id={`${id}-amount`} value={amount} onChange={(e) => setAmount(e.target.value)} inputMode="decimal" />
      </div>
      <div style={{ width: 110 }}>
        <label htmlFor={`${id}-unit`}>{t("secrets.pool.unit")}</label>
        <select id={`${id}-unit`} value={unit} onChange={(e) => setUnit(e.target.value as SecretLimit["unit"])}>
          <option value="usd">{t("secrets.pool.unitUsd")}</option>
          <option value="tokens">{t("secrets.pool.unitTokens")}</option>
        </select>
      </div>
      <div style={{ width: 110 }}>
        <label htmlFor={`${id}-window`}>{t("secrets.pool.windowHours")}</label>
        <input id={`${id}-window`} value={hours} onChange={(e) => setHours(e.target.value)} inputMode="decimal" />
      </div>
      <button className="btn primary sm" disabled={pending}>
        {t("secrets.pool.saveLimit")}
      </button>
      <p className="muted w-full m-0" style={{ maxWidth: 560 }}>
        {t("secrets.pool.limitHint")}
      </p>
    </form>
  );
}

const fmtAmount = (n: number, unit: string) =>
  unit === "tokens" ? n.toLocaleString(undefined, { maximumFractionDigits: 0 }) : `$${n.toFixed(2)}`;

const fmtWindow = (secs: number) => (secs % 86400 === 0 ? `${secs / 86400} d` : `${Math.round(secs / 3600)} h`);

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

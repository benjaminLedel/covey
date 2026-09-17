import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { fmtCount, fmtUSD } from "../format";
import {
  api,
  del,
  patch,
  post,
  type Agent,
  type RuntimeCredential,
  type RuntimeInfo,
  type RuntimeInstance,
  type SecretLimit,
  type SecretPreview,
} from "../api";

// The workstations: engine plus capacity, named and assignable (spec/18).
//
// The card answers three questions in this order, because they are asked in
// that order: what does this contract work on, how full is it,
// and who sits on it.

// Amounts and counts come from format.ts — the same notation as on the cost
// page and in the backlog. This file had its own version (`$12.30` with
// the sign in front, tokens as a raw number), and two notations for the same
// kind of number are one question too many for anyone with both sides open.
const fmtAmount = (n: number, unit: string) => (unit === "tokens" ? fmtCount(n) : fmtUSD(n));
const fmtWindow = (secs: number) => (secs % 86400 === 0 ? `${secs / 86400} d` : `${Math.round(secs / 3600)} h`);

export default function RuntimeInstances({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: ["runtime-instances"],
    queryFn: () => api<RuntimeInstance[]>("/runtime-instances"),
  });
  const engines = useQuery({ queryKey: ["runtimes"], queryFn: () => api<RuntimeInfo[]>("/runtimes") });
  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[]>("/agents") });
  const secrets = useQuery({
    queryKey: ["secrets"],
    queryFn: () => api<SecretPreview[]>("/secrets"),
    retry: false,
  });

  const [adding, setAdding] = useState(false);
  const [engine, setEngine] = useState("");
  const [name, setName] = useState("");
  const create = useMutation({
    mutationFn: () => post<RuntimeInstance>("/runtime-instances", { engine, display_name: name }),
    onSuccess: () => {
      setName("");
      setAdding(false);
      qc.invalidateQueries({ queryKey: ["runtime-instances"] });
    },
  });

  const engineList = engines.data ?? [];

  return (
    <div className="mb-8">
      <div className="flex items-baseline gap-3 mb-2">
        <h2 className="text-[18px]">{t("runtimes.instances.title")}</h2>
        <span className="muted text-xs">{t("runtimes.instances.subtitle")}</span>
      </div>
      <p className="muted text-xs mb-3" style={{ maxWidth: 680 }}>
        {t("runtimes.instances.desc")}
      </p>

      {(list.data ?? []).map((rt) => (
        <RuntimeCard
          key={rt.id}
          rt={rt}
          engine={engineList.find((e) => e.name === rt.engine)}
          agents={agents.data ?? []}
          secrets={secrets.data ?? []}
          canEdit={canEdit}
        />
      ))}
      {list.data?.length === 0 && <p className="muted text-sm">{t("runtimes.instances.none")}</p>}

      {canEdit &&
        (adding ? (
          <form
            className="card flex gap-3 items-end flex-wrap mt-2"
            onSubmit={(e) => {
              e.preventDefault();
              create.mutate();
            }}
          >
            <div style={{ width: 190 }}>
              <label htmlFor="rt-engine">{t("runtimes.instances.engine")}</label>
              <select id="rt-engine" value={engine} onChange={(e) => setEngine(e.target.value)} required>
                <option value="">—</option>
                {engineList.map((e) => (
                  <option key={e.name} value={e.name}>
                    {e.label}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex-1 min-w-52">
              <label htmlFor="rt-name">{t("runtimes.instances.name")}</label>
              <input
                id="rt-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder={t("runtimes.instances.namePlaceholder")}
                required
              />
            </div>
            <button className="btn primary" disabled={create.isPending}>
              {t("runtimes.instances.create")}
            </button>
            <button type="button" className="btn" onClick={() => setAdding(false)}>
              {t("secrets.pool.cancel")}
            </button>
          </form>
        ) : (
          <button className="btn mt-2" onClick={() => setAdding(true)}>
            {t("runtimes.instances.add")}
          </button>
        ))}
    </div>
  );
}

function RuntimeCard({
  rt,
  engine,
  agents,
  secrets,
  canEdit,
}: {
  rt: RuntimeInstance;
  engine?: RuntimeInfo;
  agents: Agent[];
  secrets: SecretPreview[];
  canEdit: boolean;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const invalidate = () => qc.invalidateQueries({ queryKey: ["runtime-instances"] });
  const path = `/runtime-instances/${rt.id}`;

  const remove = useMutation({ mutationFn: () => del(path), onSuccess: invalidate });
  const moveHere = useMutation({
    mutationFn: (agentID: string) => post(`/agents/${agentID}/runtime-instance`, { runtime_id: rt.id }),
    onSuccess: () => {
      invalidate();
      qc.invalidateQueries({ queryKey: ["agents"] });
    },
  });
  const [addingCred, setAddingCred] = useState(false);

  const name = (id: string) => agents.find((a) => a.id === id)?.display_name ?? id.slice(0, 8);
  const seats = (ord: number) => rt.bindings.filter((b) => b.ord === ord);
  /* Who really sits here — from the assignment, not derived from the
     engine. The old derivation (`a.runtime === rt.engine`) showed the
     intended state instead of the stored one: an agent whose engine was
     changed while its seat stayed put showed up here as set up — and got
     the foreign engine's access at run time. */
  const assigned = agents.filter((a) => a.runtime_id === rt.id);
  /* And the reverse check that did not exist before: agents running this
     engine but sitting elsewhere. That is exactly the state the old list
     silently passed as fine. */
  const stray = agents.filter((a) => a.runtime === rt.engine && a.runtime_id !== rt.id);

  return (
    <div className="card mb-3" style={{ padding: "13px 16px" }}>
      <div className="flex items-center gap-3 flex-wrap">
        <span className="font-medium">{rt.display_name}</span>
        <span className="mono text-xs muted">{engine?.label ?? rt.engine}</span>
        {rt.model && <span className="badge st-triage">{rt.model}</span>}
        {/* An engine without resume does not carry an agent that is waiting
            for an answer — that belongs at the assignment, not a footnote. */}
        {!rt.can_carry_blocking && (
          <span className="badge st-blocked" title={t("runtimes.instances.noResumeHint")}>
            {t("runtimes.instances.noResume")}
          </span>
        )}
        <span className="flex-1" />
        {canEdit && (
          <button
            className="btn sm"
            onClick={() => {
              if (confirm(t("runtimes.instances.deleteConfirm", { name: rt.display_name }))) remove.mutate();
            }}
          >
            {t("secrets.delete")}
          </button>
        )}
      </div>

      <div className="mt-2 text-xs">
        {rt.creds.length === 0 && (
          <p className="muted m-0" style={{ color: "var(--text-warning, #b45309)" }}>
            {t("runtimes.instances.noCapacity")}
          </p>
        )}
        {rt.creds.map((c) => (
          <CredentialRow
            key={c.ord}
            runtimeID={rt.id}
            cred={c}
            seats={seats(c.ord).map((b) => ({ name: name(b.agent_id), reason: b.reason }))}
            canEdit={canEdit}
            onChanged={invalidate}
          />
        ))}

        {canEdit &&
          (addingCred ? (
            <AddCredential
              runtimeID={rt.id}
              engine={engine}
              secrets={secrets}
              onDone={() => {
                setAddingCred(false);
                invalidate();
              }}
              onCancel={() => setAddingCred(false)}
            />
          ) : (
            <button className="btn sm mt-2" onClick={() => setAddingCred(true)}>
              {t("runtimes.instances.addCredential")}
            </button>
          ))}
      </div>

      <div className="flex items-center gap-2 flex-wrap mt-2 text-xs">
        <span className="muted">{t("runtimes.instances.worksHere")}</span>
        {assigned.length === 0 && <span className="muted">{t("runtimes.instances.nobody")}</span>}
        {assigned.map((a) => (
          <span key={a.id} className="badge st-triage">
            {a.display_name}
          </span>
        ))}
      </div>

      {/* The contradiction gets a line and a button, instead of staying
          invisible: someone here runs this engine but sits on a
          foreign seat — and so gets that seat's credentials. */}
      {stray.length > 0 && (
        <div className="flex items-center gap-2 flex-wrap mt-2 text-xs">
          <span className="badge st-blocked">{t("runtimes.instances.strayLabel")}</span>
          {stray.map((a) => (
            <button
              key={a.id}
              className="btn sm"
              disabled={moveHere.isPending}
              title={t("runtimes.instances.strayHint", { name: a.display_name })}
              onClick={() => moveHere.mutate(a.id)}
            >
              {t("runtimes.instances.moveHere", { name: a.display_name })}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function CredentialRow({
  runtimeID,
  cred: c,
  seats,
  canEdit,
  onChanged,
}: {
  runtimeID: string;
  cred: RuntimeCredential;
  seats: { name: string; reason: string }[];
  canEdit: boolean;
  onChanged: () => void;
}) {
  const { t } = useTranslation();
  const path = `/runtime-instances/${runtimeID}/credentials/${c.ord}`;
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
  // Pausing is a decision, not a measurement: it holds until someone takes
  // it back — unlike the cooldown the platform sets (#260).
  const setPaused = useMutation({
    mutationFn: (paused: boolean) => patch<{ ok: boolean }>(path, { paused }),
    onSuccess: onChanged,
  });
  const remove = useMutation({ mutationFn: () => del(path), onSuccess: onChanged });

  const paused = !!c.paused_at;
  const parked = !!c.cooldown_until && new Date(c.cooldown_until) > new Date();
  const used = c.limit.unit === "tokens" ? c.usage.tokens : c.usage.usd;
  const share = c.limit.window_secs > 0 && c.limit.amount > 0 ? Math.min(1, used / c.limit.amount) : 0;
  // The reported number beats our estimate — but it is labelled differently,
  // because one is a measurement and the other an inference.
  const reported = c.reported && c.reported.window_percent >= 0 ? c.reported : undefined;

  return (
    <div className="mt-2" style={{ borderTop: "1px solid var(--border)", paddingTop: 8 }}>
      <div className="flex items-center gap-3 flex-wrap">
        <span className="badge" title={t(`runtimes.instances.kindHint.${c.kind}`)}>
          {t(`runtimes.instances.kind.${c.kind}`)}
        </span>
        <span className="mono muted">
          {c.secret_key}
          {c.secret_slot > 0 ? `#${c.secret_slot}` : ""}
        </span>
        <span className="flex-1">{c.label || t("runtimes.instances.unnamed", { ord: c.ord })}</span>
        {paused && (
          <span className="badge st-blocked" title={t("runtimes.instances.pausedHint")}>
            {t("runtimes.instances.paused", { since: new Date(c.paused_at!).toLocaleString() })}
          </span>
        )}
        {parked && (
          <span className="badge st-blocked" title={c.cooldown_reason}>
            {t("runtimes.instances.parked", { until: new Date(c.cooldown_until!).toLocaleString() })}
          </span>
        )}
        {seats.map((s) => (
          <span key={s.name} className="badge st-triage" title={t(`runtimes.instances.reason.${s.reason}`)}>
            {s.name}
          </span>
        ))}
        {canEdit && (
          <>
            {parked && (
              <button className="btn sm" onClick={() => release.mutate()}>
                {t("runtimes.instances.release")}
              </button>
            )}
            <button
              className="btn sm"
              disabled={setPaused.isPending}
              title={paused ? undefined : t("runtimes.instances.pausedHint")}
              onClick={() => setPaused.mutate(!paused)}
            >
              {t(paused ? "runtimes.instances.resume" : "runtimes.instances.pause")}
            </button>
            <button className="btn sm" onClick={() => setEditing(!editing)}>
              {t("runtimes.instances.limit")}
            </button>
            <button
              className="btn sm"
              onClick={() => {
                if (confirm(t("runtimes.instances.removeCredentialConfirm"))) remove.mutate();
              }}
            >
              {t("secrets.delete")}
            </button>
          </>
        )}
      </div>

      {/* Reported number first, when there is one: it comes from the provider. */}
      {reported && (
        <div className="flex items-center gap-2 mt-1">
          <div style={{ flex: 1, height: 5, background: "var(--border)", borderRadius: 3 }}>
            <div
              style={{
                width: `${Math.min(100, reported.window_percent)}%`,
                height: "100%",
                borderRadius: 3,
                background:
                  reported.window_percent >= 90
                    ? "var(--danger, #b91c1c)"
                    : reported.window_percent >= 70
                      ? "var(--warning, #b45309)"
                      : "var(--accent, #2563eb)",
              }}
            />
          </div>
          <span className="muted">
            {t("runtimes.instances.reported", {
              window: reported.window_percent,
              week: reported.week_percent >= 0 ? reported.week_percent : "—",
            })}
            {reported.stale && ` · ${t("runtimes.instances.stale")}`}
          </span>
        </div>
      )}

      <div className="flex items-center gap-2 mt-1">
        {c.limit.window_secs > 0 ? (
          <>
            <div style={{ flex: 1, height: 5, background: "var(--border)", borderRadius: 3 }}>
              <div
                style={{
                  width: `${share * 100}%`,
                  height: "100%",
                  borderRadius: 3,
                  background:
                    share >= 1
                      ? "var(--danger, #b91c1c)"
                      : share > 0.8
                        ? "var(--warning, #b45309)"
                        : "var(--accent, #2563eb)",
                }}
              />
            </div>
            <span className="muted">
              {t("runtimes.instances.usedOfLimit", {
                used: fmtAmount(used, c.limit.unit),
                limit: fmtAmount(c.limit.amount, c.limit.unit),
                window: fmtWindow(c.window_secs),
              })}
            </span>
          </>
        ) : (
          <span className="muted">
            {t("runtimes.instances.usedNoLimit", {
              usd: fmtUSD(c.usage.usd),
              tokens: fmtCount(c.usage.tokens),
              window: fmtWindow(c.window_secs),
            })}
          </span>
        )}
      </div>

      {/* What the money number means hangs on the capacity kind — that stands
          there, instead of leaving it to the reader (spec/17). */}
      {!c.metered && c.usage.usd > 0 && (
        <p className="muted m-0 mt-1" style={{ fontSize: 11 }}>
          {t("runtimes.instances.notionalHint")}
        </p>
      )}

      {editing && <LimitForm id={`lim-${runtimeID}-${c.ord}`} limit={c.limit} metered={c.metered} pending={setLimit.isPending} onSave={(l) => setLimit.mutate(l)} />}
    </div>
  );
}

function AddCredential({
  runtimeID,
  engine,
  secrets,
  onDone,
  onCancel,
}: {
  runtimeID: string;
  engine?: RuntimeInfo;
  secrets: SecretPreview[];
  onDone: () => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const kinds = engine?.credentials ?? [];
  const [kind, setKind] = useState<string>(kinds[0]?.kind ?? "");
  const declared = kinds.find((k) => k.kind === kind);
  const [slot, setSlot] = useState(0);
  const [label, setLabel] = useState("");

  // The key follows from the engine's declaration — it is not freely
  // choosable, else a value stands there that nothing knows how to load.
  const secret = secrets.find((s) => s.key === declared?.secret);
  const values = secret?.values ?? [];

  const add = useMutation({
    mutationFn: () =>
      post<{ ok: boolean; ord: number }>(`/runtime-instances/${runtimeID}/credentials`, {
        kind,
        secret_key: declared?.secret,
        secret_slot: slot,
        label,
      }),
    onSuccess: onDone,
  });

  return (
    <form
      className="flex gap-2 items-end flex-wrap mt-2"
      onSubmit={(e) => {
        e.preventDefault();
        add.mutate();
      }}
    >
      <div style={{ width: 150 }}>
        <label htmlFor={`k-${runtimeID}`}>{t("runtimes.instances.kindLabel")}</label>
        <select id={`k-${runtimeID}`} value={kind} onChange={(e) => setKind(e.target.value)}>
          {kinds.map((k) => (
            <option key={k.kind} value={k.kind}>
              {k.label}
            </option>
          ))}
        </select>
      </div>
      <div style={{ width: 210 }}>
        <label htmlFor={`v-${runtimeID}`}>{t("runtimes.instances.value")}</label>
        <select id={`v-${runtimeID}`} value={slot} onChange={(e) => setSlot(Number(e.target.value))}>
          {values.map((v) => (
            <option key={v.slot} value={v.slot}>
              {declared?.secret}
              {v.slot > 0 ? ` #${v.slot}` : ""} {v.prefix ? `(${v.prefix}…)` : ""}
            </option>
          ))}
        </select>
      </div>
      <div style={{ width: 150 }}>
        <label htmlFor={`l-${runtimeID}`}>{t("runtimes.instances.label")}</label>
        <input id={`l-${runtimeID}`} value={label} onChange={(e) => setLabel(e.target.value)} placeholder={t("runtimes.instances.labelHint")} />
      </div>
      <button className="btn primary sm" disabled={add.isPending || !declared || values.length === 0}>
        {t("secrets.pool.add")}
      </button>
      <button type="button" className="btn sm" onClick={onCancel}>
        {t("secrets.pool.cancel")}
      </button>
      {declared && values.length === 0 && (
        <p className="muted w-full m-0" style={{ color: "var(--text-warning, #b45309)" }}>
          {t("runtimes.instances.missingSecret", { key: declared.secret })}
        </p>
      )}
    </form>
  );
}

function LimitForm({
  id,
  limit,
  metered,
  pending,
  onSave,
}: {
  id: string;
  limit: SecretLimit;
  metered: boolean;
  pending: boolean;
  onSave: (l: SecretLimit) => void;
}) {
  const { t } = useTranslation();
  const [amount, setAmount] = useState(String(limit.amount || ""));
  // The honest unit follows from the capacity kind: money where money is
  // spent; the window quota where it is not.
  const [unit, setUnit] = useState<SecretLimit["unit"]>(limit.unit || (metered ? "usd" : "tokens"));
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
        <label htmlFor={`${id}-amount`}>{t("runtimes.instances.amount")}</label>
        <input id={`${id}-amount`} value={amount} onChange={(e) => setAmount(e.target.value)} inputMode="decimal" />
      </div>
      <div style={{ width: 110 }}>
        <label htmlFor={`${id}-unit`}>{t("runtimes.instances.unit")}</label>
        <select id={`${id}-unit`} value={unit} onChange={(e) => setUnit(e.target.value as SecretLimit["unit"])}>
          <option value="usd">{t("runtimes.instances.unitUsd")}</option>
          <option value="tokens">{t("runtimes.instances.unitTokens")}</option>
        </select>
      </div>
      <div style={{ width: 110 }}>
        <label htmlFor={`${id}-window`}>{t("runtimes.instances.windowHours")}</label>
        <input id={`${id}-window`} value={hours} onChange={(e) => setHours(e.target.value)} inputMode="decimal" />
      </div>
      <button className="btn primary sm" disabled={pending}>
        {t("runtimes.instances.saveLimit")}
      </button>
      <p className="muted w-full m-0" style={{ maxWidth: 620 }}>
        {t("runtimes.instances.limitHint")}
      </p>
    </form>
  );
}

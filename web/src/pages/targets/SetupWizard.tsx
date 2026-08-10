import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, patch, post, put, type ConfigVersion } from "../../api";
import { Modal } from "../../components/Modal";

/* Der Einrichtungs-Assistent eines Zielsystems.
 *
 * Vorher war das Aktivieren ein Schalter und der Rest ein Textblock, dessen
 * Schritte auf drei Seiten und ein fremdes System verteilt waren. Der
 * Assistent macht daraus eine Reihenfolge und erledigt darin alles, was in
 * dieser Oberfläche erledigt werden kann: Secrets hinterlegen, den Agenten
 * zuweisen, die Webhook-Adresse fertig zusammensetzen, die Verbindung prüfen.
 *
 * Was im FREMDEN System zu tun ist — einen Token erzeugen, einen Trigger
 * anlegen —, bleibt Prosa. Das kann diese Oberfläche nicht für jemanden tun,
 * und so zu tun als ob wäre die schlechtere Hilfe.
 *
 * Jeder Schritt kennt seinen eigenen Zustand aus dem Server (GET
 * /targets/{name}/setup) und nicht aus dem Klickverlauf: Wer die Hälfte schon
 * von Hand eingerichtet hat, findet sie abgehakt vor. */

type SetupCredential = { key: string; kind: string; stored: boolean; optional: boolean };
type SetupAgent = { id: string; slug: string; display_name: string; access: boolean; scopes?: string[] };
type SetupState = {
  name: string;
  label: string;
  enabled: boolean;
  credentials: SetupCredential[];
  scopes?: string[];
  webhook: { supported: boolean; url?: string; secret_env?: string; secret_set: boolean };
  probe: boolean;
  agents: SetupAgent[];
  setup_doc?: string;
};
type ProbeResult = { ok: boolean; identity?: string; error?: string };

type StepKey = "activate" | "credentials" | "access" | "webhook" | "probe";

export function TargetSetupWizard({ name, onClose }: { name: string; onClose: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [agentId, setAgentId] = useState("");
  const [scopes, setScopes] = useState<string[]>([]);
  const [step, setStep] = useState(0);

  const setup = useQuery({
    queryKey: ["target-setup", name],
    queryFn: () => api<SetupState>(`/targets/${encodeURIComponent(name)}/setup`),
  });
  const s = setup.data;

  // Welche Schritte diese Einrichtung überhaupt hat, entscheidet das Plugin.
  // Ein System ohne Webhook bekommt keinen Webhook-Schritt — eine leere Seite
  // mit "trifft nicht zu" ist ein Schritt, den man liest, ohne etwas davon zu
  // haben.
  /* Listen aus dem Server werden defensiv gelesen. Ein leeres Array kommt als
     `null` zurueck, sobald jemand serverseitig ein `omitempty` setzt oder ein
     nil-Slice durchreicht — und `null.length` beendet hier nicht den Schritt,
     sondern den ganzen Assistenten mit einem TypeError im Render. */
  const credentials = s?.credentials ?? [];

  const steps = useMemo<StepKey[]>(() => {
    if (!s) return [];
    const out: StepKey[] = [];
    if (!s.enabled) out.push("activate");
    if (credentials.length > 0) out.push("credentials");
    out.push("access");
    if (s.webhook.supported) out.push("webhook");
    if (s.probe) out.push("probe");
    return out;
  }, [s, credentials]);

  const done = (k: StepKey): boolean => {
    if (!s) return false;
    switch (k) {
      case "activate":
        return s.enabled;
      case "credentials":
        return credentials.every((c) => c.stored || c.optional);
      case "access":
        return s.agents.some((a) => a.access);
      case "webhook":
        return s.webhook.secret_set;
      case "probe":
        return probe.data?.ok === true;
    }
  };

  const probe = useMutation({
    mutationFn: () => post<ProbeResult>(`/targets/${encodeURIComponent(name)}/probe`),
  });

  const activate = useMutation({
    mutationFn: () => patch(`/targets/${encodeURIComponent(name)}`, { enabled: true }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["target-setup", name] });
      qc.invalidateQueries({ queryKey: ["targets"] });
    },
  });

  if (setup.isLoading || !s) {
    return (
      <Modal title={t("targets.wizard.title")} onClose={onClose} size="lg">
        <p className="muted text-xs">{t("common.loading", "…")}</p>
      </Modal>
    );
  }

  const current = steps[Math.min(step, steps.length - 1)];
  const agent = s.agents.find((a) => a.id === agentId);

  return (
    <Modal
      title={t("targets.wizard.titleFor", { system: s.label || s.name })}
      onClose={onClose}
      size="lg"
      footer={
        <>
          <button className="btn sm" onClick={onClose}>
            {t("targets.close")}
          </button>
          <button className="btn sm" disabled={step === 0} onClick={() => setStep((n) => n - 1)}>
            {t("dashboard.back")}
          </button>
          <button
            className="btn sm primary"
            disabled={step >= steps.length - 1}
            onClick={() => setStep((n) => n + 1)}
          >
            {t("public.docs.next")}
          </button>
        </>
      }
    >
      <ol className="tgt-steps">
        {steps.map((k, i) => (
          <li
            key={k}
            className={`${done(k) ? "done" : ""} ${i === step ? "at" : ""}`}
            onClick={() => setStep(i)}
          >
            <span className="n">{done(k) ? "✓" : i + 1}</span>
            {t(`targets.wizard.step.${k}`)}
          </li>
        ))}
      </ol>

      <div className="tgt-step-body">
        {current === "activate" && (
          <>
            <p className="text-xs secondary">{t("targets.wizard.activateHint")}</p>
            <button
              className="btn sm primary"
              disabled={activate.isPending}
              onClick={() => activate.mutate()}
            >
              {t("targets.activate")}
            </button>
          </>
        )}

        {current === "credentials" && (
          <CredentialStep
            credentials={credentials}
            onSaved={() => qc.invalidateQueries({ queryKey: ["target-setup", name] })}
          />
        )}

        {current === "access" && (
          <AccessStep
            system={s.name}
            agents={s.agents}
            available={s.scopes ?? []}
            agentId={agentId}
            scopes={scopes}
            onAgent={(id) => {
              setAgentId(id);
              const a = s.agents.find((x) => x.id === id);
              setScopes(a?.scopes?.length ? a.scopes : (s.scopes ?? []).slice(0, 2));
            }}
            onScopes={setScopes}
            onSaved={() => qc.invalidateQueries({ queryKey: ["target-setup", name] })}
          />
        )}

        {current === "webhook" && (
          <WebhookStep webhook={s.webhook} slug={agent?.slug} doc={s.setup_doc} />
        )}

        {current === "probe" && (
          <>
            <p className="text-xs secondary">{t("targets.wizard.probeHint")}</p>
            <button className="btn sm primary" disabled={probe.isPending} onClick={() => probe.mutate()}>
              {probe.isPending ? t("targets.wizard.probing") : t("targets.wizard.probeBtn")}
            </button>
            {probe.data?.ok && (
              <p className="text-xs ok-text mt-2">
                {t("targets.wizard.probeOk", { identity: probe.data.identity || "—" })}
              </p>
            )}
            {probe.data && !probe.data.ok && (
              <pre className="tgt-doc mt-2">{probe.data.error}</pre>
            )}
          </>
        )}

        {/* Der Prosa-Teil steht bei jedem Schritt außer dem Webhook-Schritt am
            Fuß — dort trägt ihn der Schritt selbst. */}
        {s.setup_doc && current !== "webhook" && (
          <details className="tgt-doc-details">
            <summary>{t("targets.wizard.docSummary")}</summary>
            <pre className="tgt-doc">{s.setup_doc}</pre>
          </details>
        )}
      </div>
    </Modal>
  );
}

function CredentialStep({
  credentials,
  onSaved,
}: {
  credentials: SetupCredential[];
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const [values, setValues] = useState<Record<string, string>>({});
  const [check, setCheck] = useState<Record<string, string>>({});

  const save = useMutation({
    mutationFn: async (key: string) =>
      put<{ ok: boolean; check?: { valid: boolean; hint?: string } }>(
        `/secrets/${encodeURIComponent(key)}`,
        { value: values[key] ?? "", sensitive: key.endsWith("_token") },
      ),
    onSuccess: (res, key) => {
      setCheck((c) => ({ ...c, [key]: res.check?.hint ?? "" }));
      setValues((v) => ({ ...v, [key]: "" }));
      onSaved();
    },
  });

  return (
    <>
      <p className="text-xs secondary">{t("targets.wizard.credHint")}</p>
      {credentials.map((c) => (
        <div key={c.key} className="tgt-cred">
          <label className="mono text-xs">
            {c.key}
            {c.optional && <span className="muted"> · {t("targets.wizard.optional")}</span>}
            {c.stored && <span className="ok-text"> · {t("targets.wizard.stored")}</span>}
          </label>
          <div className="flex gap-2">
            <input
              type={c.kind === "token" ? "password" : "text"}
              value={values[c.key] ?? ""}
              placeholder={c.kind === "url" ? "https://…" : "••••••"}
              onChange={(e) => setValues((v) => ({ ...v, [c.key]: e.target.value }))}
              style={{ flex: 1 }}
            />
            <button
              className="btn sm"
              disabled={!values[c.key] || save.isPending}
              onClick={() => save.mutate(c.key)}
            >
              {t("secrets.save", "Speichern")}
            </button>
          </div>
          {check[c.key] && <p className="text-xs danger-text">{check[c.key]}</p>}
        </div>
      ))}
    </>
  );
}

/* Der Zugriffs-Schritt schreibt die ACCESS.md des gewählten Agenten — mit
   sichtbarem Vorher/Nachher. Config-as-Code bleibt Config-as-Code: Es entsteht
   eine neue Version wie bei jeder Änderung im Editor, nur ohne den Umweg. */
function AccessStep({
  system,
  agents,
  available,
  agentId,
  scopes,
  onAgent,
  onScopes,
  onSaved,
}: {
  system: string;
  agents: SetupAgent[];
  available: string[];
  agentId: string;
  scopes: string[];
  onAgent: (id: string) => void;
  onScopes: (s: string[]) => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const line = `- system: ${system}${scopes.length ? ` scope: ${scopes.join(",")}` : ""}`;

  const cfg = useQuery({
    queryKey: ["agent-config", agentId],
    queryFn: () => api<ConfigVersion>(`/agents/${agentId}/config`),
    enabled: !!agentId,
  });

  const before = cfg.data?.files["ACCESS.md"] ?? "";
  // Eine bestehende Zeile für dasselbe System wird ersetzt, nicht verdoppelt —
  // sonst gewönne je nach Parser die erste oder die letzte, und niemand sähe
  // es der Datei an.
  const after = useMemo(() => {
    const kept = before
      .split("\n")
      .filter((l) => !new RegExp(`^\\s*-?\\s*system:\\s*${system}\\b`).test(l));
    const body = kept.join("\n").trimEnd();
    return (body ? body + "\n" : "") + line + "\n";
  }, [before, line, system]);

  const save = useMutation({
    mutationFn: () =>
      put(`/agents/${agentId}/config`, {
        files: { ...(cfg.data?.files ?? {}), "ACCESS.md": after },
      }),
    onSuccess: onSaved,
  });

  return (
    <>
      <p className="text-xs secondary">{t("targets.wizard.accessHint")}</p>
      <label className="text-xs">{t("targets.wizard.pickAgent")}</label>
      <select value={agentId} onChange={(e) => onAgent(e.target.value)}>
        <option value="">—</option>
        {agents.map((a) => (
          <option key={a.id} value={a.id}>
            {a.display_name} ({a.slug}){a.access ? " ✓" : ""}
          </option>
        ))}
      </select>

      {available.length > 0 && (
        <div className="tgt-scopes">
          {available.map((sc) => (
            <label key={sc} className="text-xs mono">
              <input
                type="checkbox"
                checked={scopes.includes(sc)}
                onChange={(e) =>
                  onScopes(e.target.checked ? [...scopes, sc] : scopes.filter((x) => x !== sc))
                }
              />
              {sc}
            </label>
          ))}
        </div>
      )}

      {agentId && (
        <>
          <div className="tgt-diff">
            <div className="text-xs muted">ACCESS.md</div>
            <pre className="tgt-doc">{after}</pre>
          </div>
          <button className="btn sm primary" disabled={save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? t("targets.wizard.saving") : t("targets.wizard.writeAccess")}
          </button>
        </>
      )}
    </>
  );
}

function WebhookStep({
  webhook,
  slug,
  doc,
}: {
  webhook: SetupState["webhook"];
  slug?: string;
  doc?: string;
}) {
  const { t } = useTranslation();
  const url = (webhook.url ?? "").replace("<agent-slug>", slug || "<agent-slug>");
  return (
    <>
      <p className="text-xs secondary">{t("targets.wizard.webhookHint")}</p>
      <div className="tgt-copy">
        <code className="mono">{url}</code>
        <button className="btn sm" onClick={() => navigator.clipboard?.writeText(url)}>
          {t("targets.wizard.copy")}
        </button>
      </div>
      {!slug && <p className="text-xs muted">{t("targets.wizard.webhookNoAgent")}</p>}
      <p className="text-xs">
        {webhook.secret_set
          ? t("targets.wizard.secretSet", { env: webhook.secret_env })
          : t("targets.wizard.secretMissing", { env: webhook.secret_env })}
      </p>
      {doc && <pre className="tgt-doc">{doc}</pre>}
    </>
  );
}

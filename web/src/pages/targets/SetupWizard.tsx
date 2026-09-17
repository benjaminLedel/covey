import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, patch, post, put, type ConfigVersion } from "../../api";
import { Modal } from "../../components/Modal";

/* The setup assistant of a target system.
 *
 *  Activating used to be a switch and the rest a block of text whose steps
 *  were spread over three pages and a foreign system. The assistant makes one
 *  order out of that and does in it everything this interface can do: store
 *  secrets, assign the agent, put the webhook address together, check the
 *  connection.
 *
 *  What is to be done in the FOREIGN system — generating a token, creating a
 *  trigger — stays prose. This interface cannot do that for someone, and
 *  pretending otherwise would be the worse kind of help.
 *
 *  Every step knows its own state from the server (GET
 *  /targets/{name}/setup) and not from the click history: whoever set up half
 *  of it by hand finds it ticked off. */

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
type ProbeResult = { ok: boolean; identity?: string; error?: string; expires_at?: string; rotatable?: boolean };

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

  // Which steps this setup has at all is decided by the plugin. A system
  // without a webhook gets no webhook step — an empty page with "not
  // applicable" is a step one reads, and reads it without getting
  // anything from it.
  /* Lists from the server are read defensively. An empty array comes back as
     `null` as soon as someone sets an `omitempty` server-side or passes a nil
     slice through — and `null.length` does not end the step here, but the
     whole assistant, with a TypeError in the render. */
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

  // The test runs as the agent the access step chose: a credential that
  // belongs to an employee the test would otherwise not see and would report
  // an error that does not exist (#189). Without a chosen agent it stays the
  // org-wide check.
  const probe = useMutation({
    mutationFn: () =>
      post<ProbeResult>(
        `/targets/${encodeURIComponent(name)}/probe`,
        agentId ? { agent_id: agentId } : undefined,
      ),
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
            {t("common.next")}
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
            {agent && (
              <p className="text-xs secondary">
                {t("targets.wizard.probeAsAgent", { agent: agent.display_name || agent.slug })}
              </p>
            )}
            <button className="btn sm primary" disabled={probe.isPending} onClick={() => probe.mutate()}>
              {probe.isPending ? t("targets.wizard.probing") : t("targets.wizard.probeBtn")}
            </button>
            {probe.data?.ok && (
              <p className="text-xs ok-text mt-2">
                {t("targets.wizard.probeOk", { identity: probe.data.identity || "—" })}
                {probe.data.expires_at && (
                  <>
                    {" · "}
                    {t(probe.data.rotatable ? "targets.wizard.probeExpiresRenewed" : "targets.wizard.probeExpires", {
                      date: new Date(probe.data.expires_at).toLocaleDateString(),
                    })}
                  </>
                )}
              </p>
            )}
            {probe.data && !probe.data.ok && (
              <pre className="tgt-doc mt-2">{probe.data.error}</pre>
            )}
          </>
        )}

        {/* The prose part stands at the foot of every step except the webhook
            one — there the step carries it itself. */}
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

/* The access step writes the ACCESS.md of the chosen agent — with a visible
   before/after. Config-as-code stays config-as-code: a new version is created
   as with any change in the editor, only without the detour. */
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
  // An existing line for the same system is replaced, not duplicated —
  // otherwise, depending on the parser, the first or the last one wins, and
  // nobody would see it in the file.
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

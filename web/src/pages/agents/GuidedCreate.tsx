import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  api,
  post,
  put,
  setAgentDepartment,
  setAgentSupervisor,
  type Agent,
  type OrgChart,
  type RuntimeInfo,
  type TargetPlugin,
} from "../../api";
import { rollAgentName, slugify } from "../../names";

/* Einen Agenten anlegen, als Onboarding gedacht.
 *
 * Das Formular davor fragte Anzeigename, Slug und Runtime — und hörte auf.
 * Was der Agent TUN können soll, kam nirgends vor: keine Rolle, kein
 * Zielsystem, kein Vorgesetzter. Danach stand man auf der Agentenseite vor
 * leeren Tabs und musste selbst herausfinden, was von den sechs
 * Konfigurationsdateien jetzt wichtig ist.
 *
 * Der Ablauf hier stellt stattdessen die vier Fragen, die ein Onboarding
 * stellt — wer ist das, was ist sein Auftrag, was darf er, wer ist für ihn
 * zuständig — und schreibt die Antworten dorthin, wo die Plattform sie liest.
 * Jede Frage sagt dazu, wo sie später auftaucht: dass der Slug in der
 * Webhook-Adresse steht, sieht man dem Feld sonst nicht an.
 *
 * Übersprungen werden darf alles außer dem Namen. Ein halb ausgefüllter Agent
 * ist besser als ein Assistent, der jemanden nicht weiterlässt. */

type Step = "who" | "job" | "may" | "org";
const STEPS: Step[] = ["who", "job", "may", "org"];

export function GuidedCreate({ onBack, onDone }: { onBack: () => void; onDone: (a: Agent) => void }) {
  const { t } = useTranslation();
  const [step, setStep] = useState<Step>("who");

  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [runtime, setRuntime] = useState("claude-code");
  const [role, setRole] = useState("");
  const [remit, setRemit] = useState("");
  const [systems, setSystems] = useState<Record<string, string[]>>({});
  const [supervisor, setSupervisor] = useState("");
  const [department, setDepartment] = useState("");

  const runtimes = useQuery({ queryKey: ["runtimes"], queryFn: () => api<RuntimeInfo[]>("/runtimes") });
  const targets = useQuery({ queryKey: ["targets"], queryFn: () => api<TargetPlugin[]>("/targets") });
  const org = useQuery({ queryKey: ["org-chart"], queryFn: () => api<OrgChart>("/org/chart") });

  const create = useMutation({
    mutationFn: async () => {
      const agent = await post<Agent>("/agents", {
        slug: slug.trim(),
        display_name: name.trim(),
        runtime,
      });
      // Die Konfiguration in einem Zug: Was der Ablauf erfragt hat, steht
      // danach in denselben Dateien, die auch der Editor zeigt — es gibt
      // keinen zweiten, verborgenen Ort für dieselbe Aussage.
      const files: Record<string, string> = {};
      if (role.trim()) {
        files["SOUL.md"] = `# ${name.trim()}\n\n## Rolle\n${role.trim()}\n`;
      }
      if (remit.trim()) {
        files["CAPABILITIES.md"] = `# Zuständigkeit\n\n${remit.trim()}\n`;
      }
      const access = Object.entries(systems)
        .filter(([, sc]) => sc.length > 0 || systems[""] !== undefined)
        .map(([sys, sc]) => `- system: ${sys}${sc.length ? ` scope: ${sc.join(",")}` : ""}`);
      if (access.length > 0) {
        files["ACCESS.md"] = `${access.join("\n")}\n`;
      }
      const boss = org.data?.humans.find((h) => h.id === supervisor);
      if (boss) {
        files["ORG.md"] = `# Organisation\n\nVorgesetzt: ${boss.display_name}. Dorthin eskalieren.\n`;
      }
      if (Object.keys(files).length > 0) {
        await put(`/agents/${agent.id}/config`, { files });
      }
      if (supervisor) await setAgentSupervisor(agent.id, supervisor);
      if (department) await setAgentDepartment(agent.id, department);
      return agent;
    },
    onSuccess: onDone,
  });

  const idx = STEPS.indexOf(step);
  const last = idx === STEPS.length - 1;

  return (
    <div>
      <ol className="tgt-steps" style={{ marginTop: 4 }}>
        {STEPS.map((k, i) => (
          <li key={k} className={`${i < idx ? "done" : ""} ${k === step ? "at" : ""}`} onClick={() => setStep(k)}>
            <span className="n">{i < idx ? "✓" : i + 1}</span>
            {t(`dashboard.guided.step.${k}`)}
          </li>
        ))}
      </ol>

      <div className="tgt-step-body">
        {step === "who" && (
          <>
            <Field label={t("dashboard.displayName")} hint={t("dashboard.guided.nameHint")}>
              <div className="flex gap-2">
                <input
                  value={name}
                  autoFocus
                  onChange={(e) => {
                    setName(e.target.value);
                    setSlug(slugify(e.target.value));
                  }}
                  style={{ flex: 1 }}
                />
                <button
                  type="button"
                  className="btn"
                  title={t("dashboard.rollDice")}
                  onClick={async () => {
                    const g = await rollAgentName();
                    setName(g.name);
                    setSlug(g.slug);
                  }}
                >
                  🎲
                </button>
              </div>
            </Field>
            <Field label={t("dashboard.slug")} hint={t("dashboard.guided.slugHint")}>
              <input value={slug} onChange={(e) => setSlug(e.target.value)} className="mono" />
            </Field>
            <Field label={t("dashboard.selectRuntime")} hint={t("dashboard.guided.runtimeHint")}>
              <select value={runtime} onChange={(e) => setRuntime(e.target.value)}>
                {(runtimes.data ?? []).length === 0 && <option value="claude-code">claude-code</option>}
                {(runtimes.data ?? []).map((rt) => (
                  <option key={rt.name} value={rt.name}>
                    {rt.name}
                  </option>
                ))}
              </select>
            </Field>
          </>
        )}

        {step === "job" && (
          <>
            <Field label={t("dashboard.guided.roleLabel")} hint={t("dashboard.guided.roleHint")}>
              <textarea
                rows={5}
                value={role}
                autoFocus
                placeholder={t("dashboard.guided.rolePlaceholder")}
                onChange={(e) => setRole(e.target.value)}
              />
            </Field>
            <Field label={t("dashboard.guided.remitLabel")} hint={t("dashboard.guided.remitHint")}>
              <input value={remit} onChange={(e) => setRemit(e.target.value)} />
            </Field>
          </>
        )}

        {step === "may" && (
          <>
            <p className="text-xs secondary">{t("dashboard.guided.mayHint")}</p>
            {(targets.data ?? []).filter((p) => p.enabled).length === 0 && (
              <p className="muted text-xs">{t("dashboard.guided.noTargets")}</p>
            )}
            {(targets.data ?? [])
              .filter((p) => p.enabled)
              .map((p) => {
                const picked = systems[p.name] !== undefined;
                const scopes = systems[p.name] ?? [];
                return (
                  <div key={p.name} className="tgt-cred">
                    <label className="text-xs">
                      <input
                        type="checkbox"
                        checked={picked}
                        onChange={(e) =>
                          setSystems((s) => {
                            const next = { ...s };
                            if (e.target.checked) next[p.name] = (p.scopes ?? []).slice(0, 2);
                            else delete next[p.name];
                            return next;
                          })
                        }
                      />{" "}
                      {p.label || p.name}
                    </label>
                    {picked && (p.scopes ?? []).length > 0 && (
                      <div className="tgt-scopes">
                        {(p.scopes ?? []).map((sc) => (
                          <label key={sc} className="text-xs mono">
                            <input
                              type="checkbox"
                              checked={scopes.includes(sc)}
                              onChange={(e) =>
                                setSystems((s) => ({
                                  ...s,
                                  [p.name]: e.target.checked
                                    ? [...scopes, sc]
                                    : scopes.filter((x) => x !== sc),
                                }))
                              }
                            />
                            {sc}
                          </label>
                        ))}
                      </div>
                    )}
                  </div>
                );
              })}
          </>
        )}

        {step === "org" && (
          <>
            <Field label={t("dashboard.guided.supervisorLabel")} hint={t("dashboard.guided.supervisorHint")}>
              <select value={supervisor} onChange={(e) => setSupervisor(e.target.value)}>
                <option value="">—</option>
                {(org.data?.humans ?? []).map((h) => (
                  <option key={h.id} value={h.id}>
                    {h.display_name}
                  </option>
                ))}
              </select>
            </Field>
            <Field label={t("dashboard.guided.departmentLabel")} hint={t("dashboard.guided.departmentHint")}>
              <select value={department} onChange={(e) => setDepartment(e.target.value)}>
                <option value="">—</option>
                {(org.data?.departments ?? []).map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.name}
                  </option>
                ))}
              </select>
            </Field>
          </>
        )}

        {create.isError && (
          <div className="danger-text text-xs">{String((create.error as Error)?.message ?? create.error)}</div>
        )}
      </div>

      <div className="flex gap-2 justify-end" style={{ marginTop: 14 }}>
        <button className="btn" onClick={() => (idx === 0 ? onBack() : setStep(STEPS[idx - 1]))}>
          {t("dashboard.back")}
        </button>
        {!last && (
          <button
            className="btn primary"
            disabled={!name.trim() || !slug.trim()}
            onClick={() => setStep(STEPS[idx + 1])}
          >
            {t("public.docs.next")}
          </button>
        )}
        {last && (
          <button
            className="btn primary"
            disabled={!name.trim() || !slug.trim() || create.isPending}
            onClick={() => create.mutate()}
          >
            {create.isPending ? t("dashboard.creatingAgent") : t("dashboard.createAgentBtn")}
          </button>
        )}
      </div>
    </div>
  );
}

/* Ein Feld mit dem Satz darunter, der sagt, wo der Wert später auftaucht.
   Das ist der eigentliche Unterschied zum alten Formular: Die Felder waren
   dieselben, nur wusste niemand, was sie bedeuten. */
function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <label>{label}</label>
      {children}
      <div className="muted text-xs" style={{ marginTop: 3 }}>
        {hint}
      </div>
    </div>
  );
}

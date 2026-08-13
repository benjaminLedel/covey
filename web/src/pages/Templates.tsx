import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, post, type AgentTemplate, type Agent, type Principal } from "../api";
import { slugify } from "../names";
import { Modal } from "../components/Modal";

export default function Templates({ me }: { me: Principal }) {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const canManage = me.Role === "org_admin" || me.Role === "agent_owner";

  const templates = useQuery({
    queryKey: ["templates", i18n.language],
    queryFn: () => api<AgentTemplate[]>(`/templates?lang=${encodeURIComponent(i18n.language)}`),
  });

  const deleteTemplate = useMutation({
    mutationFn: (id: string) => del(`/templates/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["templates"] }),
  });

  const [instantiating, setInstantiating] = useState<AgentTemplate | null>(null);
  const [previewing, setPreviewing] = useState<AgentTemplate | null>(null);
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  const list = templates.data ?? [];

  return (
    <div style={{ maxWidth: 860 }}>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-[22px]">{t("templates.title")}</h1>
      </div>

      {templates.isLoading && <p className="muted text-sm">{t("templates.loading")}</p>}
      {templates.isError && <p className="danger-text text-sm">{t("templates.loadError")}</p>}

      {list.length === 0 && !templates.isLoading && (
        <div className="card" style={{ padding: "32px 24px", textAlign: "center" }}>
          <p className="muted text-sm" style={{ marginBottom: 8 }}>
            {t("templates.noTemplates")}
          </p>
          <p className="muted text-xs">
            {t("templates.noTemplatesHint")}
          </p>
        </div>
      )}

      <div style={{ display: "grid", gap: 12 }}>
        {list.map((tpl) => (
          <TemplateCard
            key={tpl.id}
            template={tpl}
            canManage={canManage}
            locale={locale}
            onPreview={() => setPreviewing(tpl)}
            onInstantiate={() => setInstantiating(tpl)}
            onDelete={() => {
              if (confirm(t("templates.deleteConfirm", { name: tpl.name }))) deleteTemplate.mutate(tpl.id);
            }}
          />
        ))}
      </div>

      {previewing && (
        <PreviewModal
          template={previewing}
          canManage={canManage}
          onClose={() => setPreviewing(null)}
          onUse={() => {
            const tpl = previewing;
            setPreviewing(null);
            setInstantiating(tpl);
          }}
        />
      )}

      {instantiating && (
        <InstantiateModal
          template={instantiating}
          onClose={() => setInstantiating(null)}
          onDone={(agent) => {
            setInstantiating(null);
            qc.invalidateQueries({ queryKey: ["agents"] });
            window.location.href = `/agents/${agent.id}`;
          }}
        />
      )}
    </div>
  );
}

function TemplateCard({
  template,
  canManage,
  locale,
  onPreview,
  onInstantiate,
  onDelete,
}: {
  template: AgentTemplate;
  canManage: boolean;
  locale: string;
  onPreview: () => void;
  onInstantiate: () => void;
  onDelete: () => void;
}) {
  const { t } = useTranslation();
  const bundle = template.bundle as { agent?: { runtime?: string; model?: string } };
  const runtime = bundle?.agent?.runtime ?? "—";
  const model = bundle?.agent?.model;

  return (
    <div className="card" style={{ padding: "14px 18px" }}>
      <div className="flex items-start justify-between gap-4">
        <div style={{ flex: 1 }}>
          <div className="flex items-center gap-2">
            <div className="font-medium" style={{ fontSize: 15 }}>{template.name}</div>
            {template.builtin && (
              <span className="badge" style={{ fontSize: 11 }}>{t("templates.builtinBadge")}</span>
            )}
          </div>
          {template.description && (
            <div className="muted text-xs" style={{ marginTop: 2 }}>{template.description}</div>
          )}
          <div className="muted text-xs" style={{ marginTop: 6 }}>
            Runtime: <span className="mono">{runtime}</span>
            {model && <> · {t("templates.modelLabel")} <span className="mono">{model}</span></>}
            {template.builtin ? (
              <> · {t("templates.builtinSource")}</>
            ) : (
              <> · {t("templates.savedDate", { date: new Date(template.created_at).toLocaleDateString(locale) })}</>
            )}
          </div>
        </div>
        <div className="flex gap-2 shrink-0">
          <button className="btn sm" onClick={onPreview}>
            {t("templates.preview")}
          </button>
          {canManage && (
            <button className="btn sm primary" onClick={onInstantiate}>
              {t("templates.use")}
            </button>
          )}
          {canManage && !template.builtin && (
            <button className="btn sm" style={{ color: "var(--error)" }} onClick={onDelete}>
              {t("templates.delete")}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

// Bevorzugte Lesereihenfolge der Config-Dateien (Rest alphabetisch dahinter).
const FILE_ORDER = [
  "SOUL.md",
  "CAPABILITIES.md",
  "PLAYBOOKS.md",
  "ORG.md",
  "ACCESS.md",
  "TOOLS.md",
  "EGRESS.md",
  "HEARTBEAT.md",
];

function orderedFileNames(files: Record<string, string>): string[] {
  return Object.keys(files).sort((a, b) => {
    const ia = FILE_ORDER.indexOf(a);
    const ib = FILE_ORDER.indexOf(b);
    return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib) || a.localeCompare(b);
  });
}

// PreviewModal zeigt das Bundle einer Vorlage read-only an: Agenten-Eckdaten
// plus die enthaltenen Config-Dateien (umschaltbar), damit man vor dem Anlegen
// sieht, was die Vorlage mitbringt.
function PreviewModal({
  template,
  canManage,
  onClose,
  onUse,
}: {
  template: AgentTemplate;
  canManage: boolean;
  onClose: () => void;
  onUse: () => void;
}) {
  const { t } = useTranslation();
  const bundle = template.bundle as {
    agent?: { slug?: string; runtime?: string; model?: string };
    files?: Record<string, string>;
    stages?: unknown[];
    guardrails?: unknown[];
    egress_templates?: unknown[];
    secrets?: { org_keys?: string[]; agent_keys?: string[] };
  };
  const files = bundle.files ?? {};
  const names = orderedFileNames(files);
  const [active, setActive] = useState(names[0] ?? "");
  const current = active && files[active] !== undefined ? active : names[0] ?? "";

  const meta: string[] = [];
  if (bundle.agent?.slug) meta.push(bundle.agent.slug);
  if (bundle.agent?.runtime) meta.push(bundle.agent.runtime);
  if (bundle.agent?.model) meta.push(bundle.agent.model);
  const secretKeys = [...(bundle.secrets?.org_keys ?? []), ...(bundle.secrets?.agent_keys ?? [])];

  return (
    <Modal
      title={t("templates.previewTitle", { name: template.name })}
      onClose={onClose}
      size="lg"
      footer={
        <div className="flex gap-2 justify-end">
          <button className="btn" onClick={onClose}>
            {t("templates.close")}
          </button>
          {canManage && (
            <button className="btn primary" onClick={onUse}>
              {t("templates.use")}
            </button>
          )}
        </div>
      }
    >
      {template.description && (
        <p className="text-sm" style={{ marginBottom: 10 }}>{template.description}</p>
      )}
      <div className="muted text-xs mono" style={{ marginBottom: 12 }}>
        {meta.join(" · ") || "—"}
      </div>

      {(secretKeys.length > 0 ||
        (bundle.stages?.length ?? 0) > 0 ||
        (bundle.guardrails?.length ?? 0) > 0 ||
        (bundle.egress_templates?.length ?? 0) > 0) && (
        <div className="muted text-xs" style={{ marginBottom: 12 }}>
          {(bundle.stages?.length ?? 0) > 0 && <>{t("templates.previewStages", { n: bundle.stages!.length })} · </>}
          {(bundle.guardrails?.length ?? 0) > 0 && <>{t("templates.previewGuardrails", { n: bundle.guardrails!.length })} · </>}
          {(bundle.egress_templates?.length ?? 0) > 0 && <>{t("templates.previewEgress", { n: bundle.egress_templates!.length })} · </>}
          {secretKeys.length > 0 && (
            <>{t("templates.previewSecrets")} <span className="mono">{secretKeys.join(", ")}</span></>
          )}
        </div>
      )}

      {names.length === 0 ? (
        <p className="muted text-sm">{t("templates.previewNoFiles")}</p>
      ) : (
        <>
          <div className="seg" role="tablist" aria-label={t("templates.previewFilesLabel")} style={{ flexWrap: "wrap", marginBottom: 10 }}>
            {names.map((name) => (
              <button
                key={name}
                role="tab"
                aria-selected={name === current}
                className={`mono ${name === current ? "active" : ""}`}
                onClick={() => setActive(name)}
              >
                {name}
              </button>
            ))}
          </div>
          <pre
            className="mono"
            style={{
              margin: 0,
              padding: "10px 12px",
              background: "var(--surface-1)",
              border: "0.5px solid var(--border)",
              borderRadius: 8,
              fontSize: 12,
              lineHeight: 1.55,
              whiteSpace: "pre-wrap",
              wordBreak: "break-word",
              maxHeight: "52vh",
              overflowY: "auto",
            }}
          >
            {files[current]}
          </pre>
        </>
      )}
    </Modal>
  );
}

function InstantiateModal({
  template,
  onClose,
  onDone,
}: {
  template: AgentTemplate;
  onClose: () => void;
  onDone: (agent: Agent) => void;
}) {
  const { t } = useTranslation();
  const suggestedSlug = slugify(template.name);
  const [slug, setSlug] = useState(suggestedSlug);
  const [displayName, setDisplayName] = useState(template.name);

  const mut = useMutation({
    mutationFn: () =>
      post<{ agent: Agent; warnings: string[] }>(`/templates/${template.id}/instantiate`, {
        slug: slug.trim(),
        display_name: displayName.trim(),
      }),
    onSuccess: (result) => {
      onDone(result.agent);
    },
  });

  return (
    <Modal title={t("templates.useTemplate", { name: template.name })} onClose={onClose} size="sm">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          mut.mutate();
        }}
        style={{ display: "flex", flexDirection: "column", gap: 12 }}
      >
        <div>
          <label>{t("templates.displayName")}</label>
          <input
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            required
            autoFocus
          />
        </div>
        <div>
          <label>{t("templates.slug")}</label>
          <input
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
            className="mono"
            required
          />
          <div className="muted text-xs" style={{ marginTop: 4 }}>
            {t("templates.slugHint")}
          </div>
        </div>
        {mut.isError && (
          <div className="danger-text text-xs">{String((mut.error as Error)?.message ?? mut.error)}</div>
        )}
        <div className="flex gap-2 justify-end" style={{ marginTop: 8 }}>
          <button type="button" className="btn" onClick={onClose}>
            {t("templates.cancel")}
          </button>
          <button type="submit" className="btn primary" disabled={mut.isPending}>
            {mut.isPending ? t("templates.creating") : t("templates.create")}
          </button>
        </div>
      </form>
    </Modal>
  );
}

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, post, type AgentTemplate, type Agent, type Principal } from "../api";
import { slugify } from "../names";
import { Modal } from "../components/Modal";

export default function Templates({ me }: { me: Principal }) {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const canManage = me.Role === "platform_admin" || me.Role === "agent_owner";

  const templates = useQuery({
    queryKey: ["templates"],
    queryFn: () => api<AgentTemplate[]>("/templates"),
  });

  const deleteTemplate = useMutation({
    mutationFn: (id: string) => del(`/templates/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["templates"] }),
  });

  const [instantiating, setInstantiating] = useState<AgentTemplate | null>(null);
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
            onInstantiate={() => setInstantiating(tpl)}
            onDelete={() => {
              if (confirm(t("templates.deleteConfirm", { name: tpl.name }))) deleteTemplate.mutate(tpl.id);
            }}
          />
        ))}
      </div>

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
  onInstantiate,
  onDelete,
}: {
  template: AgentTemplate;
  canManage: boolean;
  locale: string;
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
          <div className="font-medium" style={{ fontSize: 15 }}>{template.name}</div>
          {template.description && (
            <div className="muted text-xs" style={{ marginTop: 2 }}>{template.description}</div>
          )}
          <div className="muted text-xs" style={{ marginTop: 6 }}>
            Runtime: <span className="mono">{runtime}</span>
            {model && <> · {t("templates.modelLabel")} <span className="mono">{model}</span></>}
            <> · {t("templates.savedDate", { date: new Date(template.created_at).toLocaleDateString(locale) })}</>
          </div>
        </div>
        {canManage && (
          <div className="flex gap-2 shrink-0">
            <button className="btn sm primary" onClick={onInstantiate}>
              {t("templates.use")}
            </button>
            <button className="btn sm" style={{ color: "var(--error)" }} onClick={onDelete}>
              {t("templates.delete")}
            </button>
          </div>
        )}
      </div>
    </div>
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

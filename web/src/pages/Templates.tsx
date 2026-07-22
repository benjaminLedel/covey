import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, del, post, type AgentTemplate, type Agent, type Principal } from "../api";
import { slugify } from "../names";
import { Modal } from "../components/Modal";

export default function Templates({ me }: { me: Principal }) {
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

  const list = templates.data ?? [];

  return (
    <div style={{ maxWidth: 860 }}>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-[22px]">Vorlagen</h1>
      </div>

      {templates.isLoading && <p className="muted text-sm">Lade…</p>}
      {templates.isError && <p className="danger-text text-sm">Fehler beim Laden.</p>}

      {list.length === 0 && !templates.isLoading && (
        <div className="card" style={{ padding: "32px 24px", textAlign: "center" }}>
          <p className="muted text-sm" style={{ marginBottom: 8 }}>
            Noch keine Vorlagen vorhanden.
          </p>
          <p className="muted text-xs">
            Öffne einen Agenten → Konfiguration → „Als Vorlage speichern".
          </p>
        </div>
      )}

      <div style={{ display: "grid", gap: 12 }}>
        {list.map((t) => (
          <TemplateCard
            key={t.id}
            template={t}
            canManage={canManage}
            onInstantiate={() => setInstantiating(t)}
            onDelete={() => {
              if (confirm(`Vorlage „${t.name}" löschen?`)) deleteTemplate.mutate(t.id);
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
            // Browser-Navigation zum neuen Agenten
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
  onInstantiate,
  onDelete,
}: {
  template: AgentTemplate;
  canManage: boolean;
  onInstantiate: () => void;
  onDelete: () => void;
}) {
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
            {model && <> · Modell: <span className="mono">{model}</span></>}
            <> · gespeichert {new Date(template.created_at).toLocaleDateString("de-DE")}</>
          </div>
        </div>
        {canManage && (
          <div className="flex gap-2 shrink-0">
            <button className="btn sm primary" onClick={onInstantiate}>
              Verwenden
            </button>
            <button className="btn sm" style={{ color: "var(--error)" }} onClick={onDelete}>
              Löschen
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
    <Modal title={`Vorlage verwenden: ${template.name}`} onClose={onClose} size="sm">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          mut.mutate();
        }}
        style={{ display: "flex", flexDirection: "column", gap: 12 }}
      >
        <div>
          <label>Anzeigename</label>
          <input
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            required
            autoFocus
          />
        </div>
        <div>
          <label>Slug</label>
          <input
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
            className="mono"
            required
          />
          <div className="muted text-xs" style={{ marginTop: 4 }}>
            Eindeutiger Bezeichner — nur Kleinbuchstaben, Ziffern, Bindestriche.
          </div>
        </div>
        {mut.isError && (
          <div className="danger-text text-xs">{String((mut.error as Error)?.message ?? mut.error)}</div>
        )}
        <div className="flex gap-2 justify-end" style={{ marginTop: 8 }}>
          <button type="button" className="btn" onClick={onClose}>
            Abbrechen
          </button>
          <button type="submit" className="btn primary" disabled={mut.isPending}>
            {mut.isPending ? "Erstelle…" : "Erstellen"}
          </button>
        </div>
      </form>
    </Modal>
  );
}

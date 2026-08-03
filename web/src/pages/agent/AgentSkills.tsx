import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  api,
  post,
  del,
  put,
  type Skill,
} from "../../api";
import { SkillEditor, type SkillDraft } from "../../components/SkillEditor";

export function AgentSkills({ agentId, canManage }: { agentId: string; canManage: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const own = useQuery({
    queryKey: ["agent-skills", agentId],
    queryFn: () => api<Skill[]>(`/agents/${agentId}/skills`),
    retry: false,
  });
  const library = useQuery({ queryKey: ["skills"], queryFn: () => api<Skill[]>("/skills"), retry: false });
  const [creating, setCreating] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  // Zum Ändern den frischen Stand holen statt den Eintrag aus der Liste: Der
  // Editor übernimmt seinen Anfangszustand beim Einhängen und PUT ersetzt den
  // Dateisatz vollständig — aus einer veralteten Liste heraus gespeichert,
  // verschwände die Änderung, die inzwischen jemand anderes gemacht hat.
  const editing = useQuery({
    queryKey: ["skill", editingId],
    queryFn: () => api<Skill>(`/skills/${editingId}`),
    enabled: !!editingId,
    gcTime: 0,
    staleTime: 0,
    refetchOnMount: "always",
  });
  const inval = () => {
    qc.invalidateQueries({ queryKey: ["agent-skills", agentId] });
    qc.invalidateQueries({ queryKey: ["skills"] });
  };

  const create = useMutation({
    mutationFn: (d: SkillDraft) => post<Skill>(`/agents/${agentId}/skills`, d),
    onSuccess: () => {
      setCreating(false);
      inval();
    },
  });
  const save = useMutation({
    mutationFn: (d: SkillDraft) => put<Skill>(`/skills/${editingId}`, d),
    onSuccess: () => {
      setEditingId(null);
      inval();
    },
  });
  const remove = useMutation({ mutationFn: (id: string) => del(`/skills/${id}`), onSuccess: inval });
  const link = useMutation({
    mutationFn: (id: string) => put(`/skills/${id}/agents/${agentId}`, {}),
    onSuccess: inval,
  });
  const unlink = useMutation({
    mutationFn: (id: string) => del(`/skills/${id}/agents/${agentId}`),
    onSuccess: inval,
  });

  const resolved = own.data ?? [];
  const linkable = (library.data ?? []).filter((s) => !(s.assigned_to ?? []).includes(agentId));
  const linked = (library.data ?? []).filter((s) => (s.assigned_to ?? []).includes(agentId));
  const shadowed = (s: Skill) =>
    resolved.some((r) => r.name === s.name && r.origin === "agent");

  return (
    <div>
      <p className="muted text-xs mb-3" style={{ maxWidth: 680 }}>
        {t("agent.skills.desc")}
      </p>

      {canManage && (
        <div className="card mb-4 flex gap-3 items-end flex-wrap">
          <div className="min-w-64">
            <label>{t("agent.skills.linkLibrary")}</label>
            <select
              value=""
              onChange={(e) => e.target.value && link.mutate(e.target.value)}
              disabled={link.isPending || linkable.length === 0}
            >
              <option value="">
                {linkable.length === 0 ? t("agent.skills.noMoreLibrary") : t("agent.skills.selectSkill")}
              </option>
              {linkable.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
          <button className="btn primary sm" onClick={() => setCreating(true)}>
            {t("agent.skills.newOwn")}
          </button>
          {link.isError && <span className="danger-text text-xs">{(link.error as Error).message}</span>}
        </div>
      )}

      {resolved.map((s) => (
        <div key={s.id} className="card mb-2" style={{ padding: "11px 15px" }}>
          <div className="flex items-center gap-4">
            <span className="mono text-sm">{s.name}</span>
            <span className={`badge ${s.origin === "agent" ? "st-triage" : "st-open"}`}>
              {s.origin === "agent" ? t("agent.skills.own") : t("agent.skills.fromLibrary")}
            </span>
            <span className="muted text-xs flex-1">{s.description}</span>
            <span className="muted text-xs">
              {t("agent.skills.fileCount", { count: s.files?.length ?? 0 })}
            </span>
            {canManage && s.origin === "agent" && (
              <>
                <button className="btn sm" onClick={() => setEditingId(s.id)}>
                  {t("skills.edit")}
                </button>
                <button
                  className="btn sm"
                  onClick={() => {
                    if (confirm(t("skills.deleteConfirm", { name: s.name }))) remove.mutate(s.id);
                  }}
                >
                  {t("skills.delete")}
                </button>
              </>
            )}
            {canManage && s.origin === "library" && (
              <button className="btn sm" disabled={unlink.isPending} onClick={() => unlink.mutate(s.id)}>
                {t("agent.skills.unlink")}
              </button>
            )}
          </div>
        </div>
      ))}
      {resolved.length === 0 && <p className="muted mb-3">{t("agent.skills.none")}</p>}
      {/* Ein wirkungsloser Klick sieht ohne Meldung aus wie ein erfolgreicher. */}
      {(remove.isError || unlink.isError) && (
        <p className="danger-text text-xs mb-2">{((remove.error ?? unlink.error) as Error).message}</p>
      )}

      {/* Verlinkt, aber wirkungslos: ein gleichnamiger eigener Skill verdeckt ihn. */}
      {linked.filter(shadowed).map((s) => (
        <div
          key={s.id}
          className="card mb-2 flex items-center gap-4"
          style={{ padding: "11px 15px", opacity: 0.55 }}
        >
          <span className="mono text-sm">{s.name}</span>
          <span className="muted text-xs flex-1">{t("agent.skills.shadowed")}</span>
          {canManage && (
            <button className="btn sm" disabled={unlink.isPending} onClick={() => unlink.mutate(s.id)}>
              {t("agent.skills.unlink")}
            </button>
          )}
        </div>
      ))}

      {creating && (
        <SkillEditor
          title={t("agent.skills.newOwn")}
          saving={create.isPending}
          error={create.isError ? (create.error as Error).message : undefined}
          onSave={(d) => create.mutate(d)}
          onClose={() => setCreating(false)}
        />
      )}
      {editingId && editing.data && !editing.isFetching && (
        <SkillEditor
          skill={editing.data}
          title={editing.data.name}
          saving={save.isPending}
          error={save.isError ? (save.error as Error).message : undefined}
          onSave={(d) => save.mutate(d)}
          onClose={() => setEditingId(null)}
        />
      )}
    </div>
  );
}

// Inline-Parser für einen Text-Abschnitt: löst [[Wikilinks]] in klickbare
// Links auf (rot & inert, wenn die Zielseite fehlt — Wiki-Konvention) und

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, post, put, type Agent, type Principal, type Skill } from "../api";
import { SkillEditor, type SkillDraft } from "../components/SkillEditor";

const canEdit = (role: string) => role === "org_admin" || role === "agent_owner";

// Die Org-Bibliothek: Fähigkeiten, die jeder Agent bekommen KANN, aber nur nach
// ausdrücklicher Verlinkung bekommt. Deshalb steht an jedem Eintrag, wer ihn
// hat — ein Skill ohne Verlinkung erreicht niemanden und kostet trotzdem
// Pflege.
export default function Skills({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const skills = useQuery({ queryKey: ["skills"], queryFn: () => api<Skill[]>("/skills"), retry: false });
  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[]>("/agents") });
  const [creating, setCreating] = useState(false);
  const editable = canEdit(me.Role);

  const create = useMutation({
    mutationFn: (d: SkillDraft) => post<Skill>("/skills", d),
    onSuccess: () => {
      setCreating(false);
      qc.invalidateQueries({ queryKey: ["skills"] });
    },
  });

  if (skills.isError) {
    return (
      <div>
        <h1 className="text-[22px] mb-3">{t("skills.title")}</h1>
        <p className="muted">{(skills.error as Error).message}</p>
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">{t("skills.title")}</h1>
        <span className="muted">{t("skills.subtitle")}</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 680 }}>
        {t("skills.desc")}
      </p>

      {editable && (
        <button className="btn primary sm mb-4" onClick={() => setCreating(true)}>
          {t("skills.new")}
        </button>
      )}

      {(skills.data ?? []).map((s) => (
        <SkillCard key={s.id} skill={s} agents={agents.data ?? []} editable={editable} />
      ))}
      {skills.data?.length === 0 && <p className="muted">{t("skills.empty")}</p>}

      {creating && (
        <SkillEditor
          title={t("skills.new")}
          saving={create.isPending}
          error={create.isError ? (create.error as Error).message : undefined}
          onSave={(d) => create.mutate(d)}
          onClose={() => setCreating(false)}
        />
      )}
    </div>
  );
}

function SkillCard({ skill, agents, editable }: { skill: Skill; agents: Agent[]; editable: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const assigned = skill.assigned_to ?? [];
  const inval = () => qc.invalidateQueries({ queryKey: ["skills"] });

  // Zum Ändern braucht es die Dateien — die Liste liefert sie nicht mit.
  //
  // gcTime/staleTime 0 und refetchOnMount sind hier kein Feintuning, sondern
  // Schutz vor Datenverlust: Der Editor übernimmt seinen Anfangszustand beim
  // Einhängen, und PUT ersetzt den Dateisatz vollständig. Käme der Stand aus
  // dem Cache, überschriebe ein Speichern die Änderung, die inzwischen jemand
  // anderes gemacht hat — ohne dass irgendwo ein Konflikt sichtbar würde.
  const full = useQuery({
    queryKey: ["skill", skill.id],
    queryFn: () => api<Skill>(`/skills/${skill.id}`),
    enabled: editing,
    gcTime: 0,
    staleTime: 0,
    refetchOnMount: "always",
  });
  const save = useMutation({
    mutationFn: (d: SkillDraft) => put<Skill>(`/skills/${skill.id}`, d),
    onSuccess: () => {
      setEditing(false);
      qc.invalidateQueries({ queryKey: ["skill", skill.id] });
      inval();
    },
  });
  const remove = useMutation({ mutationFn: () => del(`/skills/${skill.id}`), onSuccess: inval });
  const assign = useMutation({
    mutationFn: (agentId: string) => put(`/skills/${skill.id}/agents/${agentId}`, {}),
    onSuccess: inval,
  });
  const unassign = useMutation({
    mutationFn: (agentId: string) => del(`/skills/${skill.id}/agents/${agentId}`),
    onSuccess: inval,
  });

  const name = (id: string) => agents.find((a) => a.id === id)?.display_name ?? id.slice(0, 8);
  const free = agents.filter((a) => !assigned.includes(a.id));

  return (
    <div className="card mb-2" style={{ padding: "11px 15px" }}>
      <div className="flex items-center gap-4">
        <span className="mono text-sm">{skill.name}</span>
        <span className="muted text-xs flex-1">{skill.description}</span>
        {editable && (
          <>
            <button className="btn sm" onClick={() => setEditing(true)}>
              {t("skills.edit")}
            </button>
            <button
              className="btn sm"
              onClick={() => {
                if (confirm(t("skills.deleteConfirm", { name: skill.name }))) remove.mutate();
              }}
            >
              {t("skills.delete")}
            </button>
          </>
        )}
      </div>
      {/* Fehlgeschlagene Aktionen müssen sichtbar sein: Ohne Meldung sieht ein
          wirkungsloser Klick genauso aus wie ein erfolgreicher. */}
      {(remove.isError || unassign.isError || assign.isError) && (
        <p className="danger-text text-xs mt-1 mb-0">
          {((remove.error ?? unassign.error ?? assign.error) as Error).message}
        </p>
      )}

      <div className="flex items-center gap-2 flex-wrap mt-2 text-xs">
        <span className="muted">{t("skills.assignedTo")}</span>
        {assigned.length === 0 && (
          <span style={{ color: "var(--text-warning, #b45309)" }}>{t("skills.nobody")}</span>
        )}
        {assigned.map((id) => (
          <span key={id} className="badge st-triage">
            {name(id)}
            {editable && (
              <button
                className="btn sm"
                style={{ border: "none", padding: "0 4px", background: "none" }}
                title={t("skills.unlink")}
                onClick={() => unassign.mutate(id)}
              >
                ×
              </button>
            )}
          </span>
        ))}
        {editable && free.length > 0 && (
          <select
            value=""
            onChange={(e) => e.target.value && assign.mutate(e.target.value)}
            disabled={assign.isPending}
            style={{ width: "auto" }}
          >
            <option value="">{t("skills.linkTo")}</option>
            {free.map((a) => (
              <option key={a.id} value={a.id}>
                {a.display_name}
              </option>
            ))}
          </select>
        )}
      </div>

      {/* Erst öffnen, wenn der frische Stand da ist: Der Editor übernimmt
          seinen Anfangszustand beim Einhängen, ein Nachladen käme nicht mehr
          an — und Speichern würde den echten Inhalt überschreiben. */}
      {editing && full.isFetching && <span className="muted text-xs">{t("skills.loading")}</span>}
      {editing && full.data && !full.isFetching && (
        <SkillEditor
          skill={full.data}
          title={skill.name}
          saving={save.isPending}
          error={save.isError ? (save.error as Error).message : undefined}
          onSave={(d) => save.mutate(d)}
          onClose={() => setEditing(false)}
        />
      )}
    </div>
  );
}

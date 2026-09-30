import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { api, patch, put, type Agent, type AgentSpokenVoice, type Department, type Voice, type VoiceUse } from "../../api";
import { Modal } from "../../components/Modal";
import {
  AUDIENCE_NOTE_MAX,
  OCCASIONS,
  deptSlotValues,
  levelText,
  slotOptions,
  slotValue,
  slotsBody,
  type AgentVoices as AgentVoicesData,
  type Assignments,
  type Occasion,
  type OrgVoices,
} from "./occasions";

/* The screens of voices per occasion (#471): the agent's three slots, the
   organisation's defaults, a department's line and voices, the table of who
   gets what, and where one voice is named. Every change needs a manage role;
   the others see the same fields, locked. */

/** Everything that shows a resolved voice reads again after a change. */
function useInvalidateVoices() {
  const qc = useQueryClient();
  return () => {
    for (const key of ["agent-voices", "agent-spoken-voice", "org-voices", "voice-assignments", "voices", "voice", "orgchart", "departments"]) {
      qc.invalidateQueries({ queryKey: [key] });
    }
  };
}

function useVoiceList() {
  return useQuery({ queryKey: ["voices"], queryFn: () => api<Voice[]>("/voices"), retry: false });
}

function SlotSelect({
  occasion,
  value,
  voices,
  disabled,
  onChange,
}: {
  occasion: Occasion;
  value: string;
  voices: Voice[];
  disabled: boolean;
  onChange: (id: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <select
      aria-label={t(`voices.occ.occasion.${occasion}`)}
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
    >
      <option value="">{t("voices.occ.notSet")}</option>
      {slotOptions(voices, value).map((v) => (
        <option key={v.id} value={v.id}>
          {v.name}
          {v.language ? ` (${v.language})` : ""}
        </option>
      ))}
    </select>
  );
}

function ErrorText({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <p className="text-xs" style={{ color: "var(--error)", margin: "4px 0 8px" }}>
      {(error as Error).message}
    </p>
  );
}

const slotRow = {
  display: "grid",
  gridTemplateColumns: "180px minmax(200px, 320px) 1fr",
  alignItems: "center",
  gap: 12,
  padding: "10px 0",
  borderBottom: "0.5px solid var(--border)",
} as const;

/* The agent's voices: a slot per occasion, and for an empty one what applies
   instead. A department's voice can still take its place when the agent
   writes to somebody of that department — the hint says so, the table on the
   voices page shows it per department. */
export function AgentVoices({ agent, editable }: { agent: Pick<Agent, "id">; editable: boolean }) {
  const { t } = useTranslation();
  const invalidate = useInvalidateVoices();
  const voices = useVoiceList();
  const slots = useQuery({
    queryKey: ["agent-voices", agent.id],
    queryFn: () => api<AgentVoicesData>(`/agents/${agent.id}/voices`),
    retry: false,
  });
  const qc = useQueryClient();
  const save = useMutation({
    mutationFn: (body: Record<Occasion, string>) => put<AgentVoicesData>(`/agents/${agent.id}/voices`, body),
    // The answer is the new state: the next change builds on it at once,
    // not on the list from before the refetch.
    onSuccess: (now) => {
      if (now?.slots) qc.setQueryData(["agent-voices", agent.id], now);
      invalidate();
    },
  });
  // An instance without voices answers with an error: the section stays away.
  if (slots.isError || voices.isError) return null;
  const data = slots.data;

  return (
    <div className="card mb-4" style={{ maxWidth: 760, padding: "14px 18px 4px" }}>
      <div className="text-sm font-medium mb-1">{t("voices.occ.agentTitle")}</div>
      <p className="muted text-xs mt-0 mb-2">{t("voices.occ.agentHint")}</p>
      {OCCASIONS.map((occ, i) => {
        const value = slotValue(data?.slots[occ]);
        const eff = data?.effective[occ];
        return (
          <div key={occ} style={i === OCCASIONS.length - 1 ? { ...slotRow, borderBottom: "none" } : slotRow}>
            <span className="text-sm">
              {t(`voices.occ.occasion.${occ}`)}
              <span className="muted text-xs" style={{ display: "block" }}>
                {t(`voices.occ.occasionHint.${occ}`)}
              </span>
            </span>
            <SlotSelect
              occasion={occ}
              value={value}
              voices={voices.data ?? []}
              disabled={!editable || !data || !voices.isSuccess || save.isPending}
              onChange={(id) => {
                if (!data) return;
                const current = Object.fromEntries(OCCASIONS.map((o) => [o, slotValue(data.slots[o])]));
                save.mutate(slotsBody({ ...current, [occ]: id }));
              }}
            />
            <span className="muted text-xs">
              {!value && eff &&
                (eff.voice_id
                  ? t("voices.occ.fallsBack", { voice: eff.voice, level: levelText(t, eff) })
                  : t("voices.occ.fallsBackNone"))}
            </span>
          </div>
        );
      })}
      <SpokenVoiceLine agentId={agent.id} />
      <ErrorText error={save.error} />
      {!editable && <p className="muted text-xs" style={{ margin: "0 0 10px" }}>{t("voices.occ.readOnly")}</p>}
    </div>
  );
}

/* Which voice at the voice provider the agent speaks with in a call, and
   why (#518): its chat voice's own, one assigned to it from the provider's
   list, or the default. Absent without a provider. */
function SpokenVoiceLine({ agentId }: { agentId: string }) {
  const { t } = useTranslation();
  const q = useQuery({
    queryKey: ["agent-spoken-voice", agentId],
    queryFn: () => api<AgentSpokenVoice>(`/agents/${agentId}/spoken-voice`),
    retry: false,
  });
  const d = q.data;
  if (!d?.provider) return null;
  const sp = d.spoken;
  const voice = sp.name ? sp.display_name || sp.name : t("voiceSpeech.providerDefaultVoice");
  const why =
    sp.source === "voice"
      ? t("voiceSpeech.sourceVoice", { name: d.voice?.name ?? "" })
      : sp.source === "assigned"
        ? t("voiceSpeech.sourceAssigned")
        : t("voiceSpeech.sourceDefault");
  return (
    <p className="text-xs" style={{ margin: "8px 0 10px" }}>
      {t("voiceSpeech.agentSpoken", { voice })} <span className="muted">({why})</span>
    </p>
  );
}

/* The organisation's default voice per occasion: what applies to every agent
   and department that names none. A change sends only its own occasion
   (PATCH), so two people setting two defaults do not undo each other. */
export function OrgVoiceDefaults({ me }: { me: { Role: string } }) {
  const { t } = useTranslation();
  const invalidate = useInvalidateVoices();
  const voices = useVoiceList();
  const org = useQuery({ queryKey: ["org-voices"], queryFn: () => api<OrgVoices>("/org/voices"), retry: false });
  const save = useMutation({
    mutationFn: (body: Partial<Record<Occasion, string>>) => patch<OrgVoices>("/org/voices", body),
    onSuccess: invalidate,
  });
  if (!org.data || voices.isError) return null;
  const editable = me.Role === "org_admin" || me.Role === "agent_owner";
  return (
    <div className="card mb-4">
      <h2 className="text-sm mb-1" style={{ fontWeight: 600 }}>{t("voices.occ.orgTitle")}</h2>
      <p className="muted text-xs mt-0 mb-2" style={{ maxWidth: 640 }}>{t("voices.occ.orgHint")}</p>
      <div className="vo-slots">
        {OCCASIONS.map((occ) => (
          <label key={occ} className="vo-slot">
            <span className="text-xs muted">{t(`voices.occ.occasion.${occ}`)}</span>
            <SlotSelect
              occasion={occ}
              value={slotValue(org.data.slots[occ])}
              voices={voices.data ?? []}
              disabled={!editable || !voices.isSuccess || save.isPending}
              onChange={(id) => save.mutate({ [occ]: id })}
            />
          </label>
        ))}
      </div>
      <ErrorText error={save.error} />
    </div>
  );
}

/* A department's audience note and its voices, as a dialog from the org
   chart. The note is a line, not a style guide: it stands in every turn that
   speaks to somebody of the department, so the counter keeps it short. */
export function DepartmentVoicesDialog({
  dept,
  editable,
  onClose,
}: {
  dept: Department;
  editable: boolean;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const invalidate = useInvalidateVoices();
  const voices = useVoiceList();
  const [note, setNote] = useState(dept.audience_note ?? "");
  const initial = deptSlotValues(dept.voices);
  const [slots, setSlots] = useState<Record<Occasion, string>>(initial);
  const noteChanged = note.trim() !== (dept.audience_note ?? "").trim();
  const slotsChanged = OCCASIONS.some((o) => slots[o] !== initial[o]);
  const tooLong = [...note].length > AUDIENCE_NOTE_MAX;

  const save = useMutation({
    mutationFn: async () => {
      if (noteChanged) await patch(`/departments/${dept.id}/audience`, { audience_note: note });
      if (slotsChanged) await put(`/departments/${dept.id}/voices`, slotsBody(slots));
    },
    onSuccess: () => {
      invalidate();
      onClose();
    },
  });

  return (
    <Modal
      title={t("voices.occ.deptTitle", { name: dept.name })}
      onClose={onClose}
      footer={
        <>
          <button className="btn sm" type="button" onClick={onClose}>
            {t("modal.cancel")}
          </button>
          {editable && (
            <button
              className="btn sm primary"
              type="button"
              disabled={save.isPending || tooLong || (!noteChanged && !slotsChanged)}
              onClick={() => save.mutate()}
            >
              {t("voices.occ.save")}
            </button>
          )}
        </>
      }
    >
      <label className="vo-field">
        <span className="text-sm font-medium">{t("voices.occ.audienceLabel")}</span>
        <span className="muted text-xs">{t("voices.occ.audienceHint")}</span>
        <textarea
          rows={3}
          value={note}
          disabled={!editable}
          placeholder={t("voices.occ.audiencePlaceholder")}
          onChange={(e) => setNote(e.target.value)}
          aria-describedby={`vo-count-${dept.id}`}
        />
        <span id={`vo-count-${dept.id}`} className={`text-xs vo-count${tooLong ? " over" : " muted"}`}>
          {t("voices.occ.counter", { n: [...note].length, max: AUDIENCE_NOTE_MAX })}
        </span>
      </label>
      {!voices.isError && (
        <div className="vo-field">
          <span className="text-sm font-medium">{t("voices.occ.deptVoices")}</span>
          <span className="muted text-xs">{t("voices.occ.deptVoicesHint")}</span>
          <div className="vo-slots">
            {OCCASIONS.map((occ) => (
              <label key={occ} className="vo-slot">
                <span className="text-xs muted">{t(`voices.occ.occasion.${occ}`)}</span>
                <SlotSelect
                  occasion={occ}
                  value={slots[occ]}
                  voices={voices.data ?? []}
                  disabled={!editable || !voices.isSuccess}
                  onChange={(id) => setSlots((s) => ({ ...s, [occ]: id }))}
                />
              </label>
            ))}
          </div>
        </div>
      )}
      <ErrorText error={save.error} />
    </Modal>
  );
}

/* Who gets what: every department, and everybody without one, against the
   three occasions — the voice the rule chooses and from where. With an agent
   picked, its own slots count; without one the table shows what departments
   and organisation say on their own. */
export function WhoGetsWhat() {
  const { t } = useTranslation();
  const [agentId, setAgentId] = useState("");
  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[]>("/agents") });
  const table = useQuery({
    queryKey: ["voice-assignments", agentId],
    queryFn: () =>
      api<Assignments>(`/voices/assignments${agentId ? `?agent_id=${encodeURIComponent(agentId)}` : ""}`),
    retry: false,
  });
  if (table.isError && !agentId) return null;
  const occasions = table.data?.occasions?.length ? table.data.occasions : [...OCCASIONS];

  return (
    <section className="vo-matrix" aria-labelledby="vo-matrix-h">
      <div className="flex items-baseline gap-3 flex-wrap mb-1">
        <h2 id="vo-matrix-h" className="vp-h">
          {t("voices.occ.matrixTitle")}
        </h2>
        <label className="vo-filter ml-auto">
          <span className="muted text-xs">{t("voices.occ.matrixFor")}</span>
          <select value={agentId} onChange={(e) => setAgentId(e.target.value)}>
            <option value="">{t("voices.occ.matrixForNone")}</option>
            {(agents.data ?? [])
              .filter((a) => !a.killed)
              .map((a) => (
                <option key={a.id} value={a.id}>
                  {a.display_name}
                </option>
              ))}
          </select>
        </label>
      </div>
      <p className="muted vp-lead">{t("voices.occ.matrixHint")}</p>
      {table.isError && <ErrorText error={table.error} />}
      {table.data && (
        <div className="card vo-table-wrap">
          <table className="tbl vo-table">
            <thead>
              <tr>
                <th scope="col">{t("voices.occ.matrixDept")}</th>
                {occasions.map((o) => (
                  <th key={o} scope="col">
                    {t(`voices.occ.occasion.${o}`)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {table.data.rows.map((row) => (
                <tr key={row.department?.id ?? "none"}>
                  <th scope="row">
                    <span className="vo-dept">{row.department?.name ?? t("voices.occ.matrixNoDept")}</span>
                    {row.audience_note && (
                      <span className="muted text-xs vo-note" title={row.audience_note}>
                        {row.audience_note}
                      </span>
                    )}
                  </th>
                  {occasions.map((o) => {
                    const c = row.cells[o];
                    return (
                      <td key={o}>
                        {c?.voice_id ? (
                          <Link to={`/voices/${c.voice_id}`}>{c.voice || c.voice_id.slice(0, 8)}</Link>
                        ) : (
                          <span className="muted">{t("voices.occ.level.none")}</span>
                        )}
                        {c?.voice_id && (
                          <span className={`vo-level l-${c.level}`} title={c.reason}>
                            {levelText(t, c)}
                          </span>
                        )}
                      </td>
                    );
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

/* Where one voice is named: per occasion, by an agent, a department or the
   organisation's defaults. */
export function UsedBy({ uses }: { uses: VoiceUse[] | undefined }) {
  const { t } = useTranslation();
  const list = uses ?? [];
  return (
    <div className="vo-usedby">
      <span className="muted text-xs">{t("voices.occ.usedByTitle")}</span>
      {list.length === 0 ? (
        <span className="muted text-xs">{t("voices.occ.usedByNone")}</span>
      ) : (
        <ul>
          {list.map((u, i) => (
            <li key={`${u.holder}:${u.id ?? ""}:${u.occasion}:${i}`}>
              <span className="pill mut">{t(`voices.occ.occasion.${u.occasion}`, u.occasion)}</span>
              {u.holder === "agent" && u.id ? <Link to={`/agents/${u.id}`}>{u.name}</Link> : <span>{u.holder === "org" ? t("voices.occ.holder.org") : u.name}</span>}
              {u.holder !== "org" && <span className="muted">{t(`voices.occ.holder.${u.holder}`)}</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

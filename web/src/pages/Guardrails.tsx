import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, del, post, type Guardrail, type Principal } from "../api";

const canEdit = (role: string) => role === "platform_admin" || role === "security";

const ruleTypeLabel: Record<string, string> = {
  deny_system: "System verboten",
  deny_action: "Aktion verboten",
  require_approval: "Freigabe-Pflicht",
  budget_limit: "Budget-Deckel",
};

export default function Guardrails({ me }: { me: Principal }) {
  const qc = useQueryClient();
  const rails = useQuery({
    queryKey: ["guardrails"],
    queryFn: () => api<Guardrail[] | null>("/guardrails"),
  });
  const remove = useMutation({
    mutationFn: (id: string) => del(`/guardrails/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["guardrails"] }),
  });

  const list = rails.data ?? [];

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">Guard-Rails</h1>
        <span className="muted">zentral erzwungen, fail-closed</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        Regeln greifen außerhalb der Runtime — am Broker und im Tool-Layer. Eine engere Ebene kann
        verschärfen, eine globale Deny-Regel nie aufgeweicht werden.
      </p>

      {canEdit(me.Role) && <CreateRule />}

      {list.map((r) => (
        <div key={r.id} className="card mb-2 flex items-center gap-4" style={{ padding: "11px 15px" }}>
          <span className={`badge ${r.rule_type.startsWith("deny") ? "st-failed" : "st-blocked"}`}>
            {ruleTypeLabel[r.rule_type] ?? r.rule_type}
          </span>
          <span className="mono text-sm flex-1">{r.pattern}</span>
          <span className="muted text-xs">{r.scope_level}</span>
          {canEdit(me.Role) && (
            <button className="btn sm" onClick={() => remove.mutate(r.id)}>
              Entfernen
            </button>
          )}
        </div>
      ))}
      {list.length === 0 && <p className="muted">Keine Regeln definiert — Default ist trotzdem fail-closed am Broker.</p>}
    </div>
  );
}

function CreateRule() {
  const qc = useQueryClient();
  const [ruleType, setRuleType] = useState("require_approval");
  const [pattern, setPattern] = useState("");
  const mut = useMutation({
    mutationFn: () => post("/guardrails", { rule_type: ruleType, pattern, scope_level: "global" }),
    onSuccess: () => {
      setPattern("");
      qc.invalidateQueries({ queryKey: ["guardrails"] });
    },
  });
  return (
    <form
      className="card mb-4 flex gap-3 items-end flex-wrap"
      onSubmit={(e) => {
        e.preventDefault();
        mut.mutate();
      }}
    >
      <div className="min-w-44">
        <label>Regeltyp</label>
        <select value={ruleType} onChange={(e) => setRuleType(e.target.value)}>
          <option value="require_approval">Freigabe-Pflicht</option>
          <option value="deny_action">Aktion verboten</option>
          <option value="deny_system">System verboten</option>
        </select>
      </div>
      <div className="flex-1 min-w-52">
        <label>Muster (z. B. zammad:reply_external oder hr*)</label>
        <input value={pattern} onChange={(e) => setPattern(e.target.value)} className="mono" required />
      </div>
      <button className="btn primary" disabled={mut.isPending}>
        Regel anlegen
      </button>
    </form>
  );
}

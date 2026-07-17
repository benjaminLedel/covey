import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api,
  del,
  patch,
  post,
  type Agent,
  type Guardrail,
  type GuardrailVerdict,
  type Principal,
  type RecordingEvent,
} from "../api";

const canEdit = (role: string) => role === "platform_admin" || role === "security";

const ruleTypeLabel: Record<string, string> = {
  deny_system: "System verboten",
  deny_action: "Aktion verboten",
  require_approval: "Freigabe-Pflicht",
  budget_limit: "Budget-Deckel",
};

const ruleTypeHint: Record<string, string> = {
  require_approval:
    "Die Aktion läuft erst nach menschlicher Freigabe — der Agent wird blocked und wartet.",
  deny_action: "Die Aktion wird hart verweigert, der Agent bekommt den Grund mitgeteilt.",
  deny_system: "Der Broker gibt für dieses System gar kein Credential erst heraus.",
  budget_limit:
    "Überschreitet der Agent den Kosten-Deckel (USD), wird er pausiert — fail-closed.",
};

// Muster-Vorschläge für den Schnellstart — Klick übernimmt sie ins Feld.
const patternSuggestions = ["zammad:reply_external", "zammad:*", "gitlab:comment_external", "gitlab:*", "mail:*", "hr*", "*"];

const decisionBadge: Record<string, { cls: string; label: string }> = {
  allow: { cls: "st-done", label: "erlaubt" },
  deny: { cls: "st-failed", label: "verboten" },
  require_approval: { cls: "st-blocked", label: "Freigabe nötig" },
};

export default function Guardrails({ me }: { me: Principal }) {
  const qc = useQueryClient();
  const rails = useQuery({
    queryKey: ["guardrails"],
    queryFn: () => api<Guardrail[] | null>("/guardrails"),
  });
  const agents = useQuery({
    queryKey: ["agents"],
    queryFn: () => api<Agent[] | null>("/agents"),
  });
  const remove = useMutation({
    mutationFn: (id: string) => del(`/guardrails/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["guardrails"] }),
  });
  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      patch(`/guardrails/${id}`, { enabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["guardrails"] }),
  });

  const list = rails.data ?? [];
  const agentName = (id?: string) =>
    agents.data?.find((a) => a.id === id)?.display_name ?? "unbekannter Agent";

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">Guard-Rails</h1>
        <span className="muted">zentral erzwungen, fail-closed</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        Regeln greifen außerhalb der Runtime — am Broker und im Tool-Layer. Eine engere Ebene kann
        verschärfen, eine globale Deny-Regel nie aufgeweicht werden. Pausierte Regeln bleiben
        erhalten, greifen aber nicht.
      </p>

      {canEdit(me.Role) && <CreateRule agents={agents.data ?? []} />}

      {list.map((r) => (
        <div key={r.id} className="card mb-2 flex items-center gap-4" style={{ padding: "11px 15px" }}>
          <span
            className={`badge ${r.rule_type.startsWith("deny") ? "st-failed" : "st-blocked"}`}
            style={r.enabled ? undefined : { opacity: 0.45 }}
          >
            {ruleTypeLabel[r.rule_type] ?? r.rule_type}
          </span>
          <span className="mono text-sm flex-1" style={r.enabled ? undefined : { opacity: 0.45 }}>
            {r.rule_type === "budget_limit" && r.params?.usd
              ? `≤ ${r.params.usd.toFixed(2)} USD`
              : r.pattern}
          </span>
          <span className="muted text-xs" title={r.agent_id ? agentName(r.agent_id) : undefined}>
            {r.scope_level === "agent" ? `Agent: ${agentName(r.agent_id)}` : r.scope_level}
          </span>
          {!r.enabled && <span className="muted text-xs">pausiert</span>}
          {canEdit(me.Role) && (
            <>
              <button
                className="btn sm"
                disabled={toggle.isPending}
                onClick={() => toggle.mutate({ id: r.id, enabled: !r.enabled })}
              >
                {r.enabled ? "Pausieren" : "Aktivieren"}
              </button>
              <button className="btn sm" onClick={() => remove.mutate(r.id)}>
                Entfernen
              </button>
            </>
          )}
        </div>
      ))}
      {list.length === 0 && (
        <p className="muted">Keine Regeln definiert — Default ist trotzdem fail-closed am Broker.</p>
      )}

      <RuleTester agents={agents.data ?? []} />
      <RecentHits agentNameOf={agentName} />
    </div>
  );
}

function CreateRule({ agents }: { agents: Agent[] }) {
  const qc = useQueryClient();
  const [ruleType, setRuleType] = useState("require_approval");
  const [pattern, setPattern] = useState("");
  const [usd, setUsd] = useState("");
  const [agentID, setAgentID] = useState("");
  const [error, setError] = useState("");
  const isBudget = ruleType === "budget_limit";
  const mut = useMutation({
    mutationFn: () =>
      post("/guardrails", {
        rule_type: ruleType,
        pattern: isBudget ? "*" : pattern,
        scope_level: agentID ? "agent" : "global",
        agent_id: agentID || undefined,
        params: isBudget ? { usd: Number(usd) } : undefined,
      }),
    onSuccess: () => {
      setPattern("");
      setUsd("");
      setError("");
      qc.invalidateQueries({ queryKey: ["guardrails"] });
    },
    onError: (e: Error) => setError(e.message),
  });
  return (
    <form
      className="card mb-4"
      onSubmit={(e) => {
        e.preventDefault();
        mut.mutate();
      }}
    >
      <div className="flex gap-3 items-end flex-wrap">
        <div className="min-w-44">
          <label>Regeltyp</label>
          <select value={ruleType} onChange={(e) => setRuleType(e.target.value)}>
            <option value="require_approval">Freigabe-Pflicht</option>
            <option value="deny_action">Aktion verboten</option>
            <option value="deny_system">System verboten</option>
            <option value="budget_limit">Budget-Deckel</option>
          </select>
        </div>
        {isBudget ? (
          <div className="min-w-44">
            <label>Deckel (USD)</label>
            <input
              type="number"
              min="0.01"
              step="0.01"
              value={usd}
              onChange={(e) => setUsd(e.target.value)}
              className="mono"
              placeholder="z. B. 25.00"
              required
            />
          </div>
        ) : (
          <div className="flex-1 min-w-52">
            <label>Muster</label>
            <input
              value={pattern}
              onChange={(e) => setPattern(e.target.value)}
              className="mono"
              placeholder="z. B. zammad:reply_external"
              required
            />
          </div>
        )}
        <div className="min-w-44">
          <label>Gilt für</label>
          <select value={agentID} onChange={(e) => setAgentID(e.target.value)}>
            <option value="">alle Agenten (global)</option>
            {agents.map((a) => (
              <option key={a.id} value={a.id}>
                nur {a.display_name}
              </option>
            ))}
          </select>
        </div>
        <button className="btn primary" disabled={mut.isPending}>
          Regel anlegen
        </button>
      </div>
      {!isBudget && (
        <div className="flex flex-wrap gap-1 mt-2 items-center">
          <span className="muted text-xs">Vorschläge:</span>
          {patternSuggestions.map((p) => (
            <button
              key={p}
              type="button"
              className="chip mono"
              style={{ cursor: "pointer" }}
              onClick={() => setPattern(p)}
            >
              {p}
            </button>
          ))}
        </div>
      )}
      <p className="muted text-xs mt-2" style={{ margin: "8px 0 0", maxWidth: 640 }}>
        {ruleTypeHint[ruleType]}
      </p>
      {error && (
        <p className="text-xs mt-1" style={{ color: "var(--text-danger)", margin: "6px 0 0" }}>
          {error}
        </p>
      )}
    </form>
  );
}

// Regel-Tester: wertet ein Subjekt trocken gegen die aktuellen Regeln aus —
// so lässt sich eine Policy prüfen, bevor ein Agent hineinläuft.
function RuleTester({ agents }: { agents: Agent[] }) {
  const [subject, setSubject] = useState("");
  const [agentID, setAgentID] = useState("");
  const test = useMutation({
    mutationFn: () =>
      post<GuardrailVerdict>("/guardrails/test", {
        subject,
        agent_id: agentID || undefined,
      }),
  });
  const v = test.data;
  const badge = v ? decisionBadge[v.decision] : undefined;
  return (
    <div className="card mt-6 mb-4">
      <h2 className="text-base font-medium mb-1">Regel-Tester</h2>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        Prüft trocken, wie die Policy für ein System (<span className="mono">zammad</span>) oder
        eine Aktion (<span className="mono">zammad:reply_external</span>) entscheiden würde —
        es wird nichts ausgeführt.
      </p>
      <form
        className="flex gap-3 items-end flex-wrap"
        onSubmit={(e) => {
          e.preventDefault();
          test.mutate();
        }}
      >
        <div className="flex-1 min-w-52">
          <label>System oder Aktion</label>
          <input
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
            className="mono"
            placeholder="zammad:reply_external"
            required
          />
        </div>
        <div className="min-w-44">
          <label>Als Agent</label>
          <select value={agentID} onChange={(e) => setAgentID(e.target.value)}>
            <option value="">ohne Agent-Kontext</option>
            {agents.map((a) => (
              <option key={a.id} value={a.id}>
                {a.display_name}
              </option>
            ))}
          </select>
        </div>
        <button className="btn" disabled={test.isPending}>
          Testen
        </button>
      </form>
      {v && badge && (
        <div className="flex items-center gap-3 mt-3 flex-wrap">
          <span className={`badge ${badge.cls}`}>{badge.label}</span>
          <span className="mono text-sm">{v.subject}</span>
          {v.rule && (
            <span className="muted text-xs">
              ausgelöst durch {ruleTypeLabel[v.rule.rule_type] ?? v.rule.rule_type}{" "}
              <span className="mono">{v.rule.pattern}</span> ({v.rule.scope_level})
            </span>
          )}
          {!v.rule && <span className="muted text-xs">keine Regel greift</span>}
          {v.budget_limit_usd && (
            <span className="muted text-xs">Budget-Deckel: {v.budget_limit_usd.toFixed(2)} USD</span>
          )}
        </div>
      )}
    </div>
  );
}

// Letzte Auslösungen: das Audit-Feed aller Guard-Rail-Treffer der Organisation.
function RecentHits({ agentNameOf }: { agentNameOf: (id?: string) => string }) {
  const events = useQuery({
    queryKey: ["guardrails", "events"],
    queryFn: () => api<RecordingEvent[] | null>("/guardrails/events?limit=20"),
    refetchInterval: 15000,
  });
  const list = events.data ?? [];
  return (
    <div className="mt-6">
      <h2 className="text-base font-medium mb-1">Letzte Auslösungen</h2>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        Jeder Guard-Rail-Treffer landet im Recording — hier die jüngsten der Organisation.
      </p>
      {list.length === 0 && <p className="muted text-sm">Noch keine Guard-Rail ausgelöst.</p>}
      {list.map((e) => {
        const p = (e.payload ?? {}) as Record<string, unknown>;
        const subject = (p.action ?? p.system ?? p.pattern ?? "") as string;
        return (
          <div key={e.id} className="card mb-2 flex items-center gap-4" style={{ padding: "9px 15px" }}>
            <span className="badge st-failed">{String(p.rule ?? "guardrail")}</span>
            <span className="mono text-sm flex-1 min-w-0 truncate">{subject}</span>
            <span className="muted text-xs">{agentNameOf(e.agent_id)}</span>
            <span className="muted text-xs">{new Date(e.created_at).toLocaleString("de-DE")}</span>
          </div>
        );
      })}
    </div>
  );
}

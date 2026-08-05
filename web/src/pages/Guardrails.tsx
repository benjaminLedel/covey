import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
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

const patternSuggestions = ["zammad:reply_external", "zammad:*", "gitlab:comment_external", "gitlab:*", "github:comment", "github:*", "mail:*", "hr*", "*"];

const decisionClass: Record<string, string> = {
  allow: "st-done",
  deny: "st-failed",
  require_approval: "st-blocked",
};

export default function Guardrails({ me }: { me: Principal }) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
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
    agents.data?.find((a) => a.id === id)?.display_name ?? "?";

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">{t("guardrails.title")}</h1>
        <span className="muted">{t("guardrails.subtitle")}</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        {t("guardrails.desc")}
      </p>

      {canEdit(me.Role) && <CreateRule agents={agents.data ?? []} />}

      {list.map((r) => (
        <div key={r.id} className="card mb-2 flex items-center gap-4" style={{ padding: "11px 15px" }}>
          <span
            className={`badge ${r.rule_type.startsWith("deny") ? "st-failed" : "st-blocked"}`}
            style={r.enabled ? undefined : { opacity: 0.45 }}
          >
            {t(`guardrails.ruleTypes.${r.rule_type}`, r.rule_type)}
          </span>
          <span className="mono text-sm flex-1" style={r.enabled ? undefined : { opacity: 0.45 }}>
            {r.rule_type === "budget_limit" && r.params?.usd
              ? `≤ ${r.params.usd.toFixed(2)} USD`
              : r.pattern}
          </span>
          <span className="muted text-xs" title={r.agent_id ? agentName(r.agent_id) : undefined}>
            {r.scope_level === "agent"
              ? t("guardrails.scopeAgent", { name: agentName(r.agent_id) })
              : r.scope_level}
          </span>
          {!r.enabled && <span className="muted text-xs">{t("guardrails.paused")}</span>}
          {canEdit(me.Role) && (
            <>
              <button
                className="btn sm"
                disabled={toggle.isPending}
                onClick={() => toggle.mutate({ id: r.id, enabled: !r.enabled })}
              >
                {r.enabled ? t("guardrails.pause") : t("guardrails.activate")}
              </button>
              <button className="btn sm" onClick={() => remove.mutate(r.id)}>
                {t("guardrails.remove")}
              </button>
            </>
          )}
        </div>
      ))}
      {list.length === 0 && (
        <p className="muted">{t("guardrails.noneByDefault")}</p>
      )}

      <RuleTester agents={agents.data ?? []} />
      <RecentHits agentNameOf={agentName} locale={locale} />
    </div>
  );
}

function CreateRule({ agents }: { agents: Agent[] }) {
  const { t } = useTranslation();
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
          <label>{t("guardrails.ruleType")}</label>
          <select value={ruleType} onChange={(e) => setRuleType(e.target.value)}>
            <option value="require_approval">{t("guardrails.ruleTypes.require_approval")}</option>
            <option value="deny_action">{t("guardrails.ruleTypes.deny_action")}</option>
            <option value="deny_system">{t("guardrails.ruleTypes.deny_system")}</option>
            <option value="budget_limit">{t("guardrails.ruleTypes.budget_limit")}</option>
          </select>
        </div>
        {isBudget ? (
          <div className="min-w-44">
            <label>{t("guardrails.cap")}</label>
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
            <label>{t("guardrails.pattern")}</label>
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
          <label>{t("guardrails.scope")}</label>
          <select value={agentID} onChange={(e) => setAgentID(e.target.value)}>
            <option value="">{t("guardrails.allAgents")}</option>
            {agents.map((a) => (
              <option key={a.id} value={a.id}>
                {t("guardrails.onlyAgent", { name: a.display_name })}
              </option>
            ))}
          </select>
        </div>
        <button className="btn primary" disabled={mut.isPending}>
          {t("guardrails.createRule")}
        </button>
      </div>
      {!isBudget && (
        <div className="flex flex-wrap gap-1 mt-2 items-center">
          <span className="muted text-xs">{t("guardrails.suggestions")}</span>
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
        {t(`guardrails.ruleHints.${ruleType}`, "")}
      </p>
      {error && (
        <p className="text-xs mt-1" style={{ color: "var(--text-danger)", margin: "6px 0 0" }}>
          {error}
        </p>
      )}
    </form>
  );
}

function RuleTester({ agents }: { agents: Agent[] }) {
  const { t } = useTranslation();
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
  return (
    <div className="card mt-6 mb-4">
      <h2 className="text-base font-medium mb-1">{t("guardrails.tester.title")}</h2>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        {t("guardrails.tester.desc")}
      </p>
      <form
        className="flex gap-3 items-end flex-wrap"
        onSubmit={(e) => {
          e.preventDefault();
          test.mutate();
        }}
      >
        <div className="flex-1 min-w-52">
          <label>{t("guardrails.tester.subject")}</label>
          <input
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
            className="mono"
            placeholder="zammad:reply_external"
            required
          />
        </div>
        <div className="min-w-44">
          <label>{t("guardrails.tester.asAgent")}</label>
          <select value={agentID} onChange={(e) => setAgentID(e.target.value)}>
            <option value="">{t("guardrails.tester.noAgentContext")}</option>
            {agents.map((a) => (
              <option key={a.id} value={a.id}>
                {a.display_name}
              </option>
            ))}
          </select>
        </div>
        <button className="btn" disabled={test.isPending}>
          {t("guardrails.tester.test")}
        </button>
      </form>
      {v && (
        <div className="flex items-center gap-3 mt-3 flex-wrap">
          <span className={`badge ${decisionClass[v.decision] ?? "st-triage"}`}>
            {t(`guardrails.decisionLabels.${v.decision}`, v.decision)}
          </span>
          <span className="mono text-sm">{v.subject}</span>
          {v.rule && (
            <span className="muted text-xs">
              {t("guardrails.tester.triggeredBy", {
                type: t(`guardrails.ruleTypes.${v.rule.rule_type}`, v.rule.rule_type),
                pattern: v.rule.pattern,
                scope: v.rule.scope_level,
              })}
            </span>
          )}
          {!v.rule && <span className="muted text-xs">{t("guardrails.tester.noRule")}</span>}
          {v.budget_limit_usd && (
            <span className="muted text-xs">{t("guardrails.tester.budgetCap", { usd: v.budget_limit_usd.toFixed(2) })}</span>
          )}
        </div>
      )}
    </div>
  );
}

function RecentHits({ agentNameOf, locale }: { agentNameOf: (id?: string) => string; locale: string }) {
  const { t } = useTranslation();
  const events = useQuery({
    queryKey: ["guardrails", "events"],
    queryFn: () => api<RecordingEvent[] | null>("/guardrails/events?limit=20"),
    refetchInterval: 15000,
  });
  const list = events.data ?? [];
  return (
    <div className="mt-6">
      <h2 className="text-base font-medium mb-1">{t("guardrails.recentHits.title")}</h2>
      <p className="muted text-xs mb-3" style={{ maxWidth: 640 }}>
        {t("guardrails.recentHits.desc")}
      </p>
      {list.length === 0 && <p className="muted text-sm">{t("guardrails.recentHits.none")}</p>}
      {list.map((e) => {
        const p = (e.payload ?? {}) as Record<string, unknown>;
        const subject = (p.action ?? p.system ?? p.pattern ?? "") as string;
        return (
          <div key={e.id} className="card mb-2 flex items-center gap-4" style={{ padding: "9px 15px" }}>
            <span className="badge st-failed">{String(p.rule ?? "guardrail")}</span>
            <span className="mono text-sm flex-1 min-w-0 truncate">{subject}</span>
            <span className="muted text-xs">{agentNameOf(e.agent_id)}</span>
            <span className="muted text-xs">{new Date(e.created_at).toLocaleString(locale)}</span>
          </div>
        );
      })}
    </div>
  );
}

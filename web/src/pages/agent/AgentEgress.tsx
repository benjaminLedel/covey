import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import {
  api,
  post,
  del,
  put,
  type EgressStatus,
  type EgressTemplate,
  type AgentEgress as AgentEgressCfg,
} from "../../api";
import { AddHostForm, EgressLogTable, HostChips } from "../../components/EgressBits";

function effectiveEgress(
  status: EgressStatus | undefined,
  templates: EgressTemplate[],
  cfg: AgentEgressCfg | undefined,
): Map<string, string> {
  const assigned = new Set(cfg?.template_ids ?? []);
  const effective = new Map<string, string>();
  for (const d of status?.defaults ?? []) effective.set(d.pattern, "Basis");
  for (const p of status?.env ?? []) if (!effective.has(p)) effective.set(p, "ENV");
  for (const tpl of templates.filter((tpl) => assigned.has(tpl.id)))
    for (const h of tpl.hosts) if (!effective.has(h.pattern)) effective.set(h.pattern, tpl.name);
  for (const h of cfg?.hosts ?? []) if (!effective.has(h.pattern)) effective.set(h.pattern, "eigener Host");
  return effective;
}
export function AgentEgress({ agentId, canEdit }: { agentId: string; canEdit: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();

  const status = useQuery({ queryKey: ["egress", "status"], queryFn: () => api<EgressStatus>("/egress") });
  const templates = useQuery({ queryKey: ["egress", "templates"], queryFn: () => api<EgressTemplate[]>("/egress/templates") });
  const cfg = useQuery({ queryKey: ["egress", "agent", agentId], queryFn: () => api<AgentEgressCfg>(`/agents/${agentId}/egress`) });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["egress", "agent", agentId] });
    qc.invalidateQueries({ queryKey: ["config", agentId] });
  };

  const toggleTpl = useMutation({
    mutationFn: ({ tid, on }: { tid: string; on: boolean }) =>
      on ? put(`/agents/${agentId}/egress/templates/${tid}`, {}) : del(`/agents/${agentId}/egress/templates/${tid}`),
    onSuccess: invalidate,
  });
  const delHost = useMutation({ mutationFn: (id: string) => del(`/agents/${agentId}/egress/hosts/${id}`), onSuccess: invalidate });

  const assigned = new Set(cfg.data?.template_ids ?? []);
  const effective = effectiveEgress(status.data, templates.data ?? [], cfg.data);

  return (
    <div style={{ maxWidth: 780 }}>
      <p className="muted text-xs mb-4">
        {t("agent.egress.desc")}
      </p>

      <div className="card mb-5" style={{ padding: "13px 15px" }}>
        <p className="text-xs font-medium mb-2">{t("agent.egress.effectiveAllowlist")}</p>
        <div className="flex flex-wrap gap-1">
          {[...effective.entries()].map(([pattern, source]) => (
            <span key={pattern} className={`chip${source === "ENV" ? " is-fixed" : ""}`}>
              {pattern}
              <span className="src">{source}</span>
            </span>
          ))}
          {effective.size === 0 && <span className="muted text-xs">{t("agent.egress.allBlocked")}</span>}
        </div>
      </div>

      <p className="text-xs font-medium mb-2">{t("agent.egress.templates")}</p>
      <div className="grid gap-2 mb-5" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(240px, 1fr))" }}>
        {(templates.data ?? []).map((tpl) => (
          <label
            key={tpl.id}
            className="card flex items-start gap-2"
            style={{ padding: "10px 12px", margin: 0, cursor: canEdit ? "pointer" : "default", opacity: assigned.has(tpl.id) ? 1 : 0.75 }}
          >
            <input
              type="checkbox"
              style={{ width: "auto", marginTop: 2 }}
              checked={assigned.has(tpl.id)}
              disabled={!canEdit || toggleTpl.isPending}
              onChange={(e) => toggleTpl.mutate({ tid: tpl.id, on: e.target.checked })}
            />
            <span style={{ minWidth: 0 }}>
              <Link
                to={`/egress/templates/${tpl.id}`}
                className="block text-sm font-medium"
                style={{ color: "var(--text-primary)", textDecoration: "none" }}
                title="Template-Detailseite öffnen"
                onClick={(e) => e.stopPropagation()}
              >
                {tpl.name}
              </Link>
              <span className="block mono text-[11px] muted" style={{ overflowWrap: "anywhere" }}>
                {tpl.hosts.length === 0 ? t("agent.egress.none") : tpl.hosts.map((h) => h.pattern).join(", ")}
              </span>
            </span>
          </label>
        ))}
        {(templates.data ?? []).length === 0 && (
          <span className="muted text-xs">
            {t("agent.egress.noTemplates")}
          </span>
        )}
      </div>

      <p className="text-xs font-medium mb-2">{t("agent.egress.ownHosts")}</p>
      <div className="flex flex-wrap gap-1 mb-2">
        <HostChips
          hosts={cfg.data?.hosts ?? []}
          canEdit={canEdit}
          onDelete={(id) => delHost.mutate(id)}
          emptyText={t("agent.egress.none")}
        />
      </div>
      {canEdit && (
        <div className="mb-5" style={{ maxWidth: 560 }}>
          <AddHostForm onAdd={(pattern, note) => post(`/agents/${agentId}/egress/hosts`, { pattern, note }).then(invalidate)} />
        </div>
      )}

      <p className="text-xs font-medium mt-5 mb-2">{t("agent.egress.lastDecisions")}</p>
      <EgressLogTable agentId={agentId} />
    </div>
  );
}

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  api,
  put,
  type AgentSystem,
  type MCPTool,
  type TargetPlugin,
} from "../../api";
import { TargetIcon } from "../../components/TargetIcon";

import { AgentSkills } from "./AgentSkills";

export function AgentTooling({
  agentId,
  canManage,
  canSecrets,
}: {
  agentId: string;
  canManage: boolean;
  canSecrets: boolean;
}) {
  const { t } = useTranslation();
  const [sp, setSp] = useSearchParams();
  const subs = [
    ["systeme", t("agent.tooling.subSystems")],
    ["mcp", t("agent.tooling.subMCP")],
    ["skills", t("agent.tabs.skills")],
  ] as const;
  const wanted = sp.get("sub") ?? "systeme";
  const sub = subs.some(([k]) => k === wanted) ? wanted : "systeme";
  const setSub = (key: string) =>
    setSp(
      (prev) => {
        const n = new URLSearchParams(prev);
        n.set("tab", "werkzeuge");
        n.set("sub", key);
        return n;
      },
      { replace: false },
    );

  return (
    <div className="settings-panes">
      <nav className="settings-nav" role="tablist">
        {subs.map(([key, label]) => (
          <button
            key={key}
            role="tab"
            aria-selected={sub === key}
            className={`nav-item${sub === key ? " active" : ""}`}
            onClick={() => setSub(key)}
          >
            {label}
          </button>
        ))}
      </nav>
      <div className="min-w-0">
        {sub === "systeme" && <AgentSystems agentId={agentId} />}
        {sub === "mcp" && <AgentTools agentId={agentId} canEdit={canSecrets} />}
        {sub === "skills" && <AgentSkills agentId={agentId} canManage={canManage} />}
      </div>
    </div>
  );
}

// AgentSystems beantwortet „was kann dieser Agent in den angebundenen
// Zielsystemen tun?" — mit den Aktionen im Wortlaut seines System-Prompts.
// Genau diesen Text liest der Agent; eine geglättete Zweitfassung wäre eine
// zweite Wahrheit, die irgendwann von der ersten abweicht.
function AgentSystems({ agentId }: { agentId: string }) {
  const { t } = useTranslation();
  const systems = useQuery({
    queryKey: ["agent-systems", agentId],
    queryFn: () => api<AgentSystem[]>(`/agents/${agentId}/systems`),
  });
  const list = systems.data ?? [];
  const mit = list.filter((s) => s.access);
  const ohne = list.filter((s) => !s.access);

  return (
    <div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 680 }}>
        {t("agent.tooling.systemsDesc")}
      </p>
      {systems.isError && <p className="danger-text text-xs">{(systems.error as Error).message}</p>}
      {systems.data && mit.length === 0 && (
        <p className="muted mb-4">{t("agent.tooling.noAccess")}</p>
      )}
      {mit.map((s) => (
        <SystemCard key={s.name} system={s} />
      ))}
      {ohne.length > 0 && (
        <>
          <h3 className="text-sm mt-6 mb-1">{t("agent.tooling.otherSystems")}</h3>
          <p className="muted text-xs mb-3" style={{ maxWidth: 680 }}>
            {t("agent.tooling.otherSystemsDesc")}
          </p>
          <div className="flex gap-2 flex-wrap">
            {ohne.map((s) => (
              <span
                key={s.name}
                className="badge st-open"
                title={s.enabled ? t("agent.tooling.enabledNoAccess") : t("agent.tooling.notEnabled")}
                style={{ opacity: s.enabled ? 1 : 0.6 }}
              >
                {s.label || s.name}
                {!s.enabled && ` · ${t("agent.tooling.off")}`}
              </span>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

// actionNames zieht die Aktionsnamen aus einer Prompt-Doku: die Plugins
// schreiben sie durchgehend als `name {"param":…}`. Eine Heuristik auf dem
// Prompt-Text statt eines zweiten, gepflegten Feldes im Plugin — der Prompt
// ist das, was der Agent wirklich liest, und darf nicht auseinanderlaufen.
// Findet sie nichts, bleibt es beim Volltext; falsch ist dann nichts.
function actionNames(doc: string): string[] {
  const out: string[] = [];
  for (const m of doc.matchAll(/(?:^|[\s,(])([a-z][a-z0-9_]{2,})\s*\{/g)) {
    if (!out.includes(m[1])) out.push(m[1]);
  }
  return out;
}

function SystemCard({ system }: { system: AgentSystem }) {
  const { t } = useTranslation();
  const actions = system.doc ? actionNames(system.doc) : [];
  return (
    <div className="card mb-3">
      <div className="flex items-center gap-2 mb-2 flex-wrap">
        <TargetIcon name={system.name} kind={system.kind} category={system.category} size={16} />
        <span className="font-medium">{system.label || system.name}</span>
        <span className="mono text-xs muted">{system.name}</span>
        {!system.enabled && (
          <span className="badge st-killed">{t("agent.tooling.notEnabledBadge")}</span>
        )}
        <span className="ml-auto" />
        {system.scopes && system.scopes.length > 0 && (
          <span className="muted text-xs mono">scope: {system.scopes.join(", ")}</span>
        )}
      </div>
      {system.description && <p className="muted text-xs mb-2">{system.description}</p>}
      {system.tools && system.tools.length > 0 && (
        <p className="text-xs mb-2">
          {t("agent.tooling.restricted", { tools: system.tools.join(", ") })}
        </p>
      )}
      {system.doc ? (
        <>
          {actions.length > 0 && (
            <div className="flex gap-1 flex-wrap mb-2">
              {actions.map((a) => (
                <span key={a} className="badge st-open mono" style={{ fontSize: 11 }}>
                  {a}
                </span>
              ))}
            </div>
          )}
          {/* Zugeklappt der Wortlaut aus dem System-Prompt: die Chips sagen,
              WAS geht, der Text sagt WIE — mit Parametern und Arbeitsweise. */}
          <details className="rec-details">
            <summary className="rec-summary text-xs">{t("agent.tooling.showDoc")}</summary>
            <pre
              className="mono text-xs"
              style={{ whiteSpace: "pre-wrap", margin: "6px 0 0", color: "var(--text-secondary)" }}
            >
              {system.doc}
            </pre>
          </details>
        </>
      ) : (
        <p className="muted text-xs">
          {system.enabled ? t("agent.tooling.noActions") : t("agent.tooling.noActionsDisabled")}
        </p>
      )}
    </div>
  );
}

// Die Einstellungen buendeln alles, was man einmal einrichtet und dann in Ruhe
// laesst: Stammdaten, Heartbeat, der Webhook-Auslöser, die Config-Dateien und
// die Zugangsdaten. Als eigene Reiter waren das fuenf von zwoelf — nebeneinander
function AgentTools({ agentId, canEdit }: { agentId: string; canEdit: boolean }) {
  const { t } = useTranslation();
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<TargetPlugin[] | null>("/targets"),
  });
  const mcp = (targets.data ?? []).filter((tgt) => tgt.kind === "mcp");

  return (
    <div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        {t("agent.tools.desc")}
      </p>
      {mcp.length === 0 && (
        <p className="muted">
          {t("agent.tools.noMcp")}
        </p>
      )}
      <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(300px, 1fr))" }}>
        {mcp.map((p) => (
          <MCPToolAssign key={p.name} agentId={agentId} plugin={p} canEdit={canEdit} />
        ))}
      </div>
    </div>
  );
}

function MCPToolAssign({
  agentId,
  plugin,
  canEdit,
}: {
  agentId: string;
  plugin: TargetPlugin;
  canEdit: boolean;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const tools: MCPTool[] = plugin.manifest?.tools ?? [];
  const assigned = useQuery({
    queryKey: ["agent-tools", agentId, plugin.name],
    queryFn: () => api<string[]>(`/agents/${agentId}/tools/${plugin.name}`),
  });

  const [restrict, setRestrict] = useState<boolean | null>(null);
  const [sel, setSel] = useState<Set<string>>(new Set());

  const loaded = assigned.data;
  const effectiveRestrict = restrict ?? (loaded ? loaded.length > 0 : false);
  const effectiveSel = restrict === null && loaded ? new Set(loaded) : sel;

  const save = useMutation({
    mutationFn: (toolList: string[]) => put(`/agents/${agentId}/tools/${plugin.name}`, { tools: toolList }),
    onSuccess: () => {
      setRestrict(null);
      qc.invalidateQueries({ queryKey: ["agent-tools", agentId, plugin.name] });
      qc.invalidateQueries({ queryKey: ["config", agentId] });
    },
  });

  const toggleSel = (name: string) => {
    const next = new Set(effectiveSel);
    next.has(name) ? next.delete(name) : next.add(name);
    setRestrict(true);
    setSel(next);
  };
  const setMode = (r: boolean) => {
    setRestrict(r);
    if (r && effectiveSel.size === 0) setSel(new Set(tools.map((tl) => tl.name)));
    else setSel(new Set(effectiveSel));
  };

  const dirty = restrict !== null;

  return (
    <div className="card" style={{ padding: "14px 16px", opacity: plugin.enabled ? 1 : 0.6 }}>
      <div className="flex items-center gap-2 mb-2">
        <TargetIcon name={plugin.name} kind={plugin.kind} category={plugin.category} size={16} />
        <span className="font-medium">{plugin.label || plugin.name}</span>
        <span className="mono text-xs muted">{plugin.name}</span>
        {!plugin.enabled && <span className="text-[11px] ml-auto" style={{ color: "var(--clay)" }}>{t("agent.tools.deactivated")}</span>}
      </div>
      {tools.length === 0 ? (
        <p className="muted text-xs">{t("agent.tools.noTools")}</p>
      ) : (
        <>
          <div className="flex gap-3 text-xs mb-2">
            <label className="flex items-center gap-1">
              <input type="radio" checked={!effectiveRestrict} disabled={!canEdit} onChange={() => setMode(false)} />
              {t("agent.tools.allTools")}
            </label>
            <label className="flex items-center gap-1">
              <input type="radio" checked={effectiveRestrict} disabled={!canEdit} onChange={() => setMode(true)} />
              {t("agent.tools.selectedOnly")}
            </label>
          </div>
          <ul className="text-xs" style={{ listStyle: "none", padding: 0, margin: 0 }}>
            {tools.map((tl) => (
              <li key={tl.name} className="mb-1">
                <label className="flex items-start gap-2" style={{ opacity: effectiveRestrict ? 1 : 0.5 }}>
                  <input
                    type="checkbox"
                    style={{ marginTop: 2 }}
                    disabled={!canEdit || !effectiveRestrict}
                    checked={effectiveRestrict ? effectiveSel.has(tl.name) : true}
                    onChange={() => toggleSel(tl.name)}
                  />
                  <span>
                    <span className="mono">{tl.name}</span>
                    {tl.description && <span className="muted"> — {tl.description.split("\n")[0]}</span>}
                  </span>
                </label>
              </li>
            ))}
          </ul>
          {canEdit && (
            <div className="flex items-center gap-2 mt-3">
              <span className="text-[11px] muted">
                {effectiveRestrict
                  ? t("agent.tools.allowedOf", { sel: effectiveSel.size, total: tools.length })
                  : t("agent.tools.allAllowed")}
              </span>
              <button
                className="btn sm primary ml-auto"
                disabled={!dirty || save.isPending || (effectiveRestrict && effectiveSel.size === 0)}
                onClick={() => save.mutate(effectiveRestrict ? [...effectiveSel] : [])}
              >
                {t("agent.tools.save")}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

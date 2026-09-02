import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, type Agent, type EgressHost, type EgressLogEntry } from "../api";

export function HostChips({
  hosts,
  canEdit,
  onDelete,
  emptyText,
}: {
  hosts: EgressHost[];
  canEdit: boolean;
  onDelete: (id: string) => void;
  emptyText?: string;
}) {
  const { t } = useTranslation();
  const empty = emptyText ?? t("egress.log.noEvents");
  if (hosts.length === 0) return <span className="muted text-xs">{empty}</span>;
  return (
    <>
      {hosts.map((h) => (
        <span key={h.id} className="chip" title={h.note || undefined}>
          {h.pattern}
          {canEdit && (
            <button onClick={() => onDelete(h.id)} title={t("egress.log.removeHost")} aria-label={`${h.pattern} ${t("egress.log.removeHost")}`}>
              ×
            </button>
          )}
        </span>
      ))}
    </>
  );
}

export function AddHostForm({ onAdd }: { onAdd: (pattern: string, note: string) => Promise<unknown> }) {
  const { t } = useTranslation();
  const [pattern, setPattern] = useState("");
  const [note, setNote] = useState("");
  const [pending, setPending] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const submit = () => {
    if (!pattern.trim() || pending) return;
    setPending(true);
    onAdd(pattern.trim(), note.trim())
      .then(() => {
        setPattern("");
        setNote("");
        setErr(null);
      })
      .catch((e: Error) => setErr(e.message))
      .finally(() => setPending(false));
  };

  return (
    <div>
      <div className="flex items-center gap-2">
        <input
          className="mono"
          style={{ flex: 2, minWidth: 0, fontSize: 12 }}
          placeholder={t("egress.log.hostPlaceholder")}
          value={pattern}
          onChange={(e) => setPattern(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
        <input
          style={{ flex: 1, minWidth: 0, fontSize: 12 }}
          placeholder={t("egress.log.notePlaceholder")}
          value={note}
          onChange={(e) => setNote(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
        <button className="btn sm" disabled={pending || !pattern.trim()} onClick={submit}>
          {t("egress.log.addHost")}
        </button>
      </div>
      {err && (
        <p className="text-xs mt-1" style={{ color: "var(--text-danger)" }}>
          {err}
        </p>
      )}
    </div>
  );
}

export function EgressLogTable({ agentId, withAgentFilter = false }: { agentId?: string; withAgentFilter?: boolean }) {
  const { t } = useTranslation();
  const [onlyBlocked, setOnlyBlocked] = useState(false);
  const [filterAgent, setFilterAgent] = useState("");
  const agents = useQuery({
    queryKey: ["agents"],
    queryFn: () => api<Agent[]>("/agents"),
    enabled: withAgentFilter,
  });

  const effectiveAgent = agentId ?? (filterAgent || undefined);
  const params = new URLSearchParams({ limit: agentId ? "50" : "100" });
  if (effectiveAgent) params.set("agent", effectiveAgent);
  if (onlyBlocked) params.set("blocked", "true");

  const log = useQuery({
    queryKey: ["egress", "log", effectiveAgent ?? "all", onlyBlocked],
    queryFn: () => api<EgressLogEntry[]>(`/egress/log?${params.toString()}`),
    refetchInterval: 10000,
  });
  const rows = log.data ?? [];
  const blockedCount = rows.filter((r) => !r.allowed).length;

  return (
    <div>
      <div className="flex items-center gap-3 mb-2">
        <label className="flex items-center gap-1 text-xs secondary" style={{ margin: 0 }}>
          <input type="checkbox" style={{ width: "auto" }} checked={onlyBlocked} onChange={(e) => setOnlyBlocked(e.target.checked)} />
          {t("egress.log.onlyBlocked")}
        </label>
        {withAgentFilter && (
          <select
            value={filterAgent}
            onChange={(e) => setFilterAgent(e.target.value)}
            style={{ width: "auto", padding: "4px 8px", fontSize: 12 }}
            aria-label={t("egress.log.filterByAgent")}
          >
            <option value="">{t("egress.log.allAgents")}</option>
            {(agents.data ?? []).map((a) => (
              <option key={a.id} value={a.id}>{a.display_name || a.slug}</option>
            ))}
          </select>
        )}
        <span className="muted text-xs ml-auto">
          {t("egress.log.entries", { count: rows.length })}
          {!onlyBlocked && blockedCount > 0 ? t("egress.log.withBlocked", { blocked: blockedCount }) : ""}
          {" · "}{t("egress.log.refresh")}
        </span>
      </div>
      <div className="card" style={{ padding: 0, overflowX: "auto" }}>
        <table className="tbl">
          <thead>
            <tr>
              <th>{t("egress.log.colTime")}</th>
              {!agentId && <th>{t("egress.log.colAgent")}</th>}
              <th>{t("egress.log.colHost")}</th>
              <th>{t("egress.log.colMethod")}</th>
              <th>{t("egress.log.colResult")}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((e) => (
              <tr key={e.id}>
                <td style={{ whiteSpace: "nowrap" }} className="secondary">{formatLogTime(e.created_at)}</td>
                {!agentId && <td className="mono">{e.agent_slug || "—"}</td>}
                <td className="mono">{e.host}</td>
                <td className="secondary">{e.method}</td>
                <td>
                  <span className={`badge state ${e.allowed ? "st-done" : "st-failed"}`}>
                    {e.allowed ? t("egress.log.allowed") : t("egress.log.blocked")}
                  </span>
                </td>
              </tr>
            ))}
            {rows.length === 0 && (
              <tr>
                <td colSpan={agentId ? 4 : 5} className="muted" style={{ padding: "14px" }}>
                  {onlyBlocked ? t("egress.log.noBlockedEvents") : t("egress.log.noEvents")}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function formatLogTime(iso: string) {
  const d = new Date(iso);
  const today = new Date();
  const sameDay = d.toDateString() === today.toDateString();
  const time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  return sameDay ? time : `${d.toLocaleDateString([], { day: "2-digit", month: "2-digit" })} ${time}`;
}

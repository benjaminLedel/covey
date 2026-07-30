import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, type Agent, type Principal, type RequestLogEntry, type RequestLogPage } from "../api";
import { ConfirmDialog } from "../components/Modal";

// Plattform → Requests: das Request-Log (spec/06). Es zeigt, was auf der
// Leitung stand — der Bot-Connector-Call nach Teams, die Antwort des
// Zielsystems, der eingehende Webhook, der an der Signaturprüfung scheiterte.
// Das Recording sagt, WAS ein Agent tat; hier steht, WIE es über die
// Schnittstelle ging. Diagnose-Daten, deshalb mit eigener kurzer Retention.
export default function Requests({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const canClear = me.Role === "platform_admin" || me.Role === "security";

  const [direction, setDirection] = useState("");
  const [system, setSystem] = useState("");
  const [agentId, setAgentId] = useState("");
  const [onlyErrors, setOnlyErrors] = useState(false);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<number | null>(null);
  const [confirmClear, setConfirmClear] = useState(false);

  const params = new URLSearchParams({ limit: "150" });
  if (direction) params.set("direction", direction);
  if (system) params.set("system", system);
  if (agentId) params.set("agent_id", agentId);
  if (onlyErrors) params.set("only_errors", "true");
  if (query.trim()) params.set("q", query.trim());

  const log = useQuery({
    queryKey: ["requests", direction, system, agentId, onlyErrors, query],
    queryFn: () => api<RequestLogPage>(`/platform/requests?${params.toString()}`),
    refetchInterval: 10000,
  });
  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[]>("/agents") });

  const clear = useMutation({
    mutationFn: () => del<{ deleted: number }>("/platform/requests"),
    onSuccess: () => {
      setSelected(null);
      qc.invalidateQueries({ queryKey: ["requests"] });
    },
  });

  const page = log.data;
  const rows = page?.entries ?? [];

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-1">
        <h1 className="text-[22px]">{t("requests.title")}</h1>
        <span className="muted">{t("requests.subtitle")}</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 720 }}>
        {t("requests.intro")}
      </p>

      {page && !page.enabled && (
        <div className="card mb-4">
          <b>{t("requests.disabled")}</b>
          <p className="muted text-xs mt-1">{t("requests.disabledHint")}</p>
        </div>
      )}

      {page?.enabled && (
        <>
          <div className="flex flex-wrap items-center gap-3 mb-2">
            <select
              value={direction}
              onChange={(e) => setDirection(e.target.value)}
              style={{ width: "auto", padding: "4px 8px", fontSize: 12 }}
              aria-label={t("requests.filterDirection")}
            >
              <option value="">{t("requests.allDirections")}</option>
              <option value="in">{t("requests.dirIn")}</option>
              <option value="out">{t("requests.dirOut")}</option>
            </select>
            <select
              value={system}
              onChange={(e) => setSystem(e.target.value)}
              style={{ width: "auto", padding: "4px 8px", fontSize: 12 }}
              aria-label={t("requests.filterSystem")}
            >
              <option value="">{t("requests.allSystems")}</option>
              {(page.systems ?? []).map((s) => (
                <option key={s} value={s}>{s}</option>
              ))}
            </select>
            <select
              value={agentId}
              onChange={(e) => setAgentId(e.target.value)}
              style={{ width: "auto", padding: "4px 8px", fontSize: 12 }}
              aria-label={t("requests.filterAgent")}
            >
              <option value="">{t("requests.allAgents")}</option>
              {(agents.data ?? []).map((a) => (
                <option key={a.id} value={a.id}>{a.display_name || a.slug}</option>
              ))}
            </select>
            <label className="flex items-center gap-1 text-xs secondary" style={{ margin: 0 }}>
              <input
                type="checkbox"
                style={{ width: "auto" }}
                checked={onlyErrors}
                onChange={(e) => setOnlyErrors(e.target.checked)}
              />
              {t("requests.onlyErrors")}
            </label>
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("requests.searchPlaceholder")}
              style={{ width: 220, padding: "4px 8px", fontSize: 12 }}
              aria-label={t("requests.search")}
            />
            {canClear && (
              <button className="btn ghost text-xs" onClick={() => setConfirmClear(true)}>
                {t("requests.clear")}
              </button>
            )}
          </div>

          <div className="flex items-center gap-2 mb-2">
            <span className="muted text-xs">
              {t("requests.count", { count: rows.length })} · {t("requests.retention", { hours: page.retention_hours })}
              {!page.bodies ? ` · ${t("requests.bodiesOff")}` : ""}
              {page.dropped > 0 ? ` · ${t("requests.dropped", { count: page.dropped })}` : ""}
            </span>
          </div>

          <div className="card" style={{ padding: 0, overflowX: "auto" }}>
            <table className="tbl">
              <thead>
                <tr>
                  <th>{t("requests.colTime")}</th>
                  <th>{t("requests.colDirection")}</th>
                  <th>{t("requests.colSystem")}</th>
                  <th>{t("requests.colAgent")}</th>
                  <th>{t("requests.colRequest")}</th>
                  <th>{t("requests.colStatus")}</th>
                  <th>{t("requests.colDuration")}</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((e) => (
                  <tr
                    key={e.id}
                    onClick={() => setSelected(selected === e.id ? null : e.id)}
                    style={{ cursor: "pointer", background: selected === e.id ? "var(--surface-1)" : undefined }}
                  >
                    <td style={{ whiteSpace: "nowrap" }} className="secondary">{formatTime(e.created_at)}</td>
                    <td className="secondary" title={e.direction === "in" ? t("requests.dirIn") : t("requests.dirOut")}>
                      {e.direction === "in" ? "⇢" : "⇠"} {e.direction === "in" ? t("requests.dirInShort") : t("requests.dirOutShort")}
                    </td>
                    <td className="mono">{e.system || "—"}</td>
                    <td className="mono">{e.agent_slug || "—"}</td>
                    <td className="mono" style={{ maxWidth: 420, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={e.url}>
                      <span className="secondary">{e.method}</span> {shortenURL(e.url)}
                    </td>
                    <td>
                      <span className={`badge ${badgeClass(e)}`}>{e.status || t("requests.noStatus")}</span>
                    </td>
                    <td className="secondary" style={{ whiteSpace: "nowrap" }}>{e.duration_ms} ms</td>
                  </tr>
                ))}
                {rows.length === 0 && (
                  <tr>
                    <td colSpan={7} className="muted" style={{ padding: "14px" }}>
                      {log.isLoading ? t("requests.loading") : t("requests.empty")}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          {selected !== null && <Detail id={selected} onClose={() => setSelected(null)} />}
        </>
      )}

      {confirmClear && (
        <ConfirmDialog
          title={t("requests.clearTitle")}
          confirmLabel={t("requests.clear")}
          onConfirm={() => {
            clear.mutate();
            setConfirmClear(false);
          }}
          onClose={() => setConfirmClear(false)}
        >
          {t("requests.clearBody")}
        </ConfirmDialog>
      )}
    </div>
  );
}

// Detail zeigt einen Eintrag samt (gekappten, redigierten) Bodies — erst hier
// werden sie geladen, damit die Liste schlank bleibt.
function Detail({ id, onClose }: { id: number; onClose: () => void }) {
  const { t } = useTranslation();
  const entry = useQuery({
    queryKey: ["requests", "detail", id],
    queryFn: () => api<RequestLogEntry>(`/platform/requests/${id}`),
  });
  const e = entry.data;

  return (
    <div className="card mt-4">
      <div className="flex items-baseline gap-3">
        <h2 className="text-base font-medium">{t("requests.detailTitle")}</h2>
        <button className="btn ghost text-xs ml-auto" onClick={onClose}>{t("requests.close")}</button>
      </div>
      {!e && <p className="muted text-xs mt-2">{t("requests.loading")}</p>}
      {e && (
        <>
          <div className="mt-2 text-xs" style={{ display: "grid", gridTemplateColumns: "auto 1fr", gap: "4px 14px" }}>
            <Field label={t("requests.colTime")} value={new Date(e.created_at).toLocaleString()} />
            <Field label={t("requests.colRequest")} value={`${e.method} ${e.url}`} />
            <Field
              label={t("requests.colStatus")}
              value={`${e.status || t("requests.noStatus")} · ${e.duration_ms} ms · ${e.resp_bytes} B`}
            />
            {e.agent_slug && <Field label={t("requests.colAgent")} value={e.agent_slug} />}
            {e.remote && <Field label={t("requests.remote")} value={e.remote} />}
            {e.error && <Field label={t("requests.error")} value={e.error} danger />}
          </div>
          <BodyBlock label={t("requests.reqBody")} body={e.req_body} shown={e.bodies_shown !== false} />
          <BodyBlock label={t("requests.respBody")} body={e.resp_body} shown={e.bodies_shown !== false} />
          <p className="muted text-xs mt-2">{t("requests.redactHint")}</p>
        </>
      )}
    </div>
  );
}

function Field({ label, value, danger = false }: { label: string; value: string; danger?: boolean }) {
  return (
    <>
      <span className="secondary" style={{ whiteSpace: "nowrap" }}>{label}</span>
      <span className="mono" style={{ wordBreak: "break-all", color: danger ? "var(--text-danger)" : undefined }}>
        {value}
      </span>
    </>
  );
}

function BodyBlock({ label, body, shown }: { label: string; body?: string; shown: boolean }) {
  const { t } = useTranslation();
  return (
    <div className="mt-3">
      <div className="text-xs secondary mb-1">{label}</div>
      <pre
        className="mono text-xs"
        style={{
          margin: 0, padding: "8px 10px", maxHeight: 260, overflow: "auto",
          background: "var(--surface-1)", borderRadius: 6, whiteSpace: "pre-wrap", wordBreak: "break-word",
        }}
      >
        {body || (shown ? t("requests.noBody") : t("requests.bodiesOff"))}
      </pre>
    </div>
  );
}

function badgeClass(e: RequestLogEntry): string {
  if (e.error && !e.status) return "st-failed";
  if (e.status >= 500 || e.status === 0) return "st-failed";
  if (e.status >= 400) return "st-blocked";
  return "st-done";
}

// shortenURL kürzt den Host weg, wenn der Pfad das Interessante ist — die
// vollständige URL steht im title und im Detail.
function shortenURL(url: string): string {
  try {
    const u = new URL(url);
    return u.pathname + u.search;
  } catch {
    return url;
  }
}

function formatTime(iso: string) {
  const d = new Date(iso);
  const today = new Date();
  const sameDay = d.toDateString() === today.toDateString();
  return sameDay ? d.toLocaleTimeString() : d.toLocaleString();
}

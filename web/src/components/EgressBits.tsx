import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, type EgressHost, type EgressLogEntry } from "../api";

// Gemeinsame Bausteine der Egress-Verwaltung — genutzt von der Egress-Seite
// (Templates + globales Monitoring) und dem Egress-Reiter der Agenten-Seite.

// HostChips zeigt Allowlist-Einträge als Chips; die Notiz erscheint als
// Tooltip, optional mit Entfernen-Knopf.
export function HostChips({
  hosts,
  canEdit,
  onDelete,
  emptyText = "keine Hosts",
}: {
  hosts: EgressHost[];
  canEdit: boolean;
  onDelete: (id: string) => void;
  emptyText?: string;
}) {
  if (hosts.length === 0) return <span className="muted text-xs">{emptyText}</span>;
  return (
    <>
      {hosts.map((h) => (
        <span key={h.id} className="chip" title={h.note || undefined}>
          {h.pattern}
          {canEdit && (
            <button onClick={() => onDelete(h.id)} title="entfernen" aria-label={`${h.pattern} entfernen`}>
              ×
            </button>
          )}
        </span>
      ))}
    </>
  );
}

// AddHostForm ist die Eingabezeile für ein neues Host-Muster (+ Notiz).
// onAdd liefert das Promise des API-Aufrufs; das Formular leert sich bei
// Erfolg selbst und zeigt Fehler (z. B. „ungültiges Muster") inline an.
export function AddHostForm({ onAdd }: { onAdd: (pattern: string, note: string) => Promise<unknown> }) {
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
          placeholder="host.example.com oder *.example.com"
          value={pattern}
          onChange={(e) => setPattern(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
        <input
          style={{ flex: 1, minWidth: 0, fontSize: 12 }}
          placeholder="Notiz (optional)"
          value={note}
          onChange={(e) => setNote(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
        <button className="btn sm" disabled={pending || !pattern.trim()} onClick={submit}>
          Hinzufügen
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

// EgressLogTable zeigt die jüngsten Egress-Entscheidungen — global (mit
// Agent-Spalte) oder für einen Agenten. Aktualisiert sich alle 10 s selbst.
export function EgressLogTable({ agentId }: { agentId?: string }) {
  const [onlyBlocked, setOnlyBlocked] = useState(false);
  const params = new URLSearchParams({ limit: agentId ? "50" : "100" });
  if (agentId) params.set("agent", agentId);
  if (onlyBlocked) params.set("blocked", "true");

  const log = useQuery({
    queryKey: ["egress", "log", agentId ?? "all", onlyBlocked],
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
          nur blockierte
        </label>
        <span className="muted text-xs ml-auto">
          {rows.length} Einträge{!onlyBlocked && blockedCount > 0 ? `, davon ${blockedCount} blockiert` : ""} · aktualisiert alle 10 s
        </span>
      </div>
      <div className="card" style={{ padding: 0, overflowX: "auto" }}>
        <table className="tbl">
          <thead>
            <tr>
              <th>Zeit</th>
              {!agentId && <th>Agent</th>}
              <th>Host</th>
              <th>Methode</th>
              <th>Ergebnis</th>
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
                  <span className={`badge ${e.allowed ? "st-done" : "st-failed"}`}>
                    {e.allowed ? "erlaubt" : "blockiert"}
                  </span>
                </td>
              </tr>
            ))}
            {rows.length === 0 && (
              <tr>
                <td colSpan={agentId ? 4 : 5} className="muted" style={{ padding: "14px" }}>
                  {onlyBlocked ? "Keine blockierten Egress-Ereignisse." : "Noch keine Egress-Ereignisse."}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// formatLogTime: heutige Einträge nur als Uhrzeit, ältere mit Datum.
function formatLogTime(iso: string) {
  const d = new Date(iso);
  const today = new Date();
  const sameDay = d.toDateString() === today.toDateString();
  const time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  return sameDay ? time : `${d.toLocaleDateString([], { day: "2-digit", month: "2-digit" })} ${time}`;
}

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api, del, post,
  type EgressLogEntry, type EgressStatus, type EgressTemplate, type Principal,
} from "../api";

// Egress-Seite: per-Agent-Allowlist über Templates + eigene Hosts, plus
// Monitoring. Der Egress-Proxy lässt pro Agent nur Verbindungen zu dessen
// effektiver Allowlist zu (Anthropic-Default + Templates + eigene Hosts).
export default function Egress({ me }: { me: Principal }) {
  const canEdit = me.Role === "platform_admin" || me.Role === "security";
  const status = useQuery({ queryKey: ["egress", "status"], queryFn: () => api<EgressStatus>("/egress") });

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">Egress</h1>
        <span className="muted">Netzwerk-Ausgang der Sandboxen — pro Agent erlaubte Ziel-Hosts</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 660 }}>
        Jeder Agent darf nur zu Hosts auf seiner Allowlist ausgehend verbinden — alles andere blockt
        der Proxy fail-closed. Hier verwaltest du die wiederverwendbaren <b>Templates</b> und siehst
        das globale <b>Monitoring</b>. Die Zuweisung pro Agent (Templates + eigene Hosts) sitzt im
        Reiter <span className="mono">Egress</span> der jeweiligen Agenten-Seite.
      </p>

      {status.data && (
        <div
          className="card mb-6"
          style={{ padding: "10px 14px", borderLeft: `3px solid ${status.data.enforced ? "var(--moss,#5a7d5a)" : "var(--clay)"}` }}
        >
          {status.data.enforced ? (
            <span className="text-xs">● Egress-Enforcement <b>aktiv</b> (docker). Fest erlaubt: {status.data.defaults.map((d) => <span key={d} className="mono">{d} </span>)}</span>
          ) : (
            <span className="text-xs">○ Egress-Enforcement <b>nicht aktiv</b> — Einträge werden gespeichert, greifen mit <span className="mono">COVEY_SANDBOX_PROVIDER=docker</span> + <span className="mono">COVEY_EGRESS_ENFORCE=true</span>.</span>
          )}
        </div>
      )}

      <Templates canEdit={canEdit} />
      <Monitoring />
    </div>
  );
}

// --- Templates ---
function Templates({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const templates = useQuery({ queryKey: ["egress", "templates"], queryFn: () => api<EgressTemplate[]>("/egress/templates") });
  const invalidate = () => qc.invalidateQueries({ queryKey: ["egress"] });

  const create = useMutation({
    mutationFn: () => post("/egress/templates", { name, description: desc }),
    onSuccess: () => { setName(""); setDesc(""); setErr(null); invalidate(); },
    onError: (e: Error) => setErr(e.message),
  });
  const remove = useMutation({ mutationFn: (id: string) => del(`/egress/templates/${id}`), onSuccess: invalidate });

  const list = templates.data ?? [];
  return (
    <section className="mb-8">
      <h2 className="text-base font-medium mb-2">Templates</h2>
      <p className="muted text-xs mb-3" style={{ maxWidth: 620 }}>Wiederverwendbare Host-Sets, die du Agenten zuweist (z.&nbsp;B. „Zammad-Prod").</p>
      <div className="grid gap-3 mb-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(300px, 1fr))" }}>
        {list.map((t) => <TemplateCard key={t.id} tpl={t} canEdit={canEdit} onChange={invalidate} onDelete={() => remove.mutate(t.id)} />)}
        {list.length === 0 && !templates.isLoading && <p className="muted text-xs">Noch keine Templates.</p>}
      </div>
      {canEdit && (
        <div className="flex flex-wrap items-end gap-2">
          <label className="block"><span className="muted text-xs block mb-1">Name</span>
            <input placeholder="Zammad-Prod" value={name} onChange={(e) => setName(e.target.value)} /></label>
          <label className="block flex-1" style={{ minWidth: 180 }}><span className="muted text-xs block mb-1">Beschreibung</span>
            <input placeholder="optional" value={desc} onChange={(e) => setDesc(e.target.value)} /></label>
          <button className="btn primary" disabled={create.isPending || !name.trim()} onClick={() => create.mutate()}>Template anlegen</button>
        </div>
      )}
      {err && <p className="text-xs mt-2" style={{ color: "var(--clay)" }}>{err}</p>}
    </section>
  );
}

function TemplateCard({ tpl, canEdit, onChange, onDelete }: { tpl: EgressTemplate; canEdit: boolean; onChange: () => void; onDelete: () => void }) {
  const [pattern, setPattern] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const addHost = useMutation({
    mutationFn: () => post(`/egress/templates/${tpl.id}/hosts`, { pattern }),
    onSuccess: () => { setPattern(""); setErr(null); onChange(); },
    onError: (e: Error) => setErr(e.message),
  });
  const delHost = useMutation({ mutationFn: (id: string) => del(`/egress/template-hosts/${id}`), onSuccess: onChange });
  return (
    <div className="card" style={{ padding: "12px 14px" }}>
      <div className="flex items-center gap-2 mb-1">
        <span className="font-medium">{tpl.name}</span>
        {canEdit && <button className="btn sm danger ml-auto" onClick={() => { if (confirm(`Template „${tpl.name}" löschen?`)) onDelete(); }}>Löschen</button>}
      </div>
      {tpl.description && <p className="muted text-xs mb-2">{tpl.description}</p>}
      <div className="flex flex-wrap gap-1 mb-2">
        {tpl.hosts.map((h) => (
          <span key={h.id} className="mono text-xs" style={{ background: "var(--surface-2,#f2efe9)", padding: "2px 8px", borderRadius: 6 }}>
            {h.pattern}{canEdit && <button className="ml-1" style={{ color: "var(--clay)" }} onClick={() => delHost.mutate(h.id)} title="entfernen">×</button>}
          </span>
        ))}
        {tpl.hosts.length === 0 && <span className="muted text-xs">keine Hosts</span>}
      </div>
      {canEdit && (
        <div className="flex items-center gap-1">
          <input className="mono" style={{ flex: 1, fontSize: 12 }} placeholder="host.example.com / *.example.com" value={pattern} onChange={(e) => setPattern(e.target.value)} />
          <button className="btn sm" disabled={addHost.isPending || !pattern.trim()} onClick={() => addHost.mutate()}>+</button>
        </div>
      )}
      {err && <p className="text-xs mt-1" style={{ color: "var(--clay)" }}>{err}</p>}
    </div>
  );
}

// --- Monitoring ---
function Monitoring() {
  const [onlyBlocked, setOnlyBlocked] = useState(false);
  const log = useQuery({
    queryKey: ["egress", "log", onlyBlocked],
    queryFn: () => api<EgressLogEntry[]>(`/egress/log?limit=100${onlyBlocked ? "&blocked=true" : ""}`),
    refetchInterval: 10000,
  });
  const rows = log.data ?? [];
  return (
    <section>
      <div className="flex items-center gap-3 mb-2">
        <h2 className="text-base font-medium">Monitoring</h2>
        <label className="flex items-center gap-1 text-xs muted">
          <input type="checkbox" checked={onlyBlocked} onChange={(e) => setOnlyBlocked(e.target.checked)} /> nur blockierte
        </label>
      </div>
      <div className="card" style={{ padding: 0, overflowX: "auto" }}>
        <table style={{ width: "100%", fontSize: 12, borderCollapse: "collapse" }}>
          <thead>
            <tr style={{ textAlign: "left", color: "var(--text-secondary)" }}>
              <th style={{ padding: "6px 10px" }}>Zeit</th><th>Agent</th><th>Host</th><th>Methode</th><th style={{ paddingRight: 10 }}>Ergebnis</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((e) => (
              <tr key={e.id} style={{ borderTop: "1px solid var(--border,#e6e1d8)" }}>
                <td style={{ padding: "5px 10px", whiteSpace: "nowrap" }}>{new Date(e.created_at).toLocaleTimeString()}</td>
                <td className="mono">{e.agent_slug || "—"}</td>
                <td className="mono">{e.host}</td>
                <td>{e.method}</td>
                <td style={{ paddingRight: 10, color: e.allowed ? "var(--text-secondary)" : "var(--clay)" }}>{e.allowed ? "erlaubt" : "blockiert"}</td>
              </tr>
            ))}
            {rows.length === 0 && <tr><td colSpan={5} className="muted" style={{ padding: "10px" }}>Noch keine Egress-Ereignisse.</td></tr>}
          </tbody>
        </table>
      </div>
    </section>
  );
}

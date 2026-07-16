import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, del, post, type EgressConfig, type Principal } from "../api";

// Egress-Seite: die Allowlist erlaubter Ziel-Hosts für ausgehenden
// Sandbox-Verkehr (spec/06, Prinzip #7). Feste Hosts (Anthropic/ENV) sind nicht
// löschbar; die betrieblich gepflegten Hosts (Zielsysteme wie Zammad) werden
// hier verwaltet und wirken sofort — der laufende Proxy lädt neu.
export default function Egress({ me }: { me: Principal }) {
  const qc = useQueryClient();
  const [pattern, setPattern] = useState("");
  const [note, setNote] = useState("");
  const [error, setError] = useState<string | null>(null);

  const cfg = useQuery({
    queryKey: ["egress"],
    queryFn: () => api<EgressConfig>("/egress"),
  });

  const canEdit = me.Role === "platform_admin" || me.Role === "security";
  const invalidate = () => qc.invalidateQueries({ queryKey: ["egress"] });

  const add = useMutation({
    mutationFn: () => post("/egress", { pattern, note }),
    onSuccess: () => {
      setPattern("");
      setNote("");
      setError(null);
      invalidate();
    },
    onError: (e: Error) => setError(e.message),
  });

  const remove = useMutation({
    mutationFn: (id: string) => del(`/egress/${id}`),
    onSuccess: invalidate,
  });

  const data = cfg.data;
  const defaults = data?.defaults ?? [];
  const entries = data?.entries ?? [];

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">Egress</h1>
        <span className="muted">Erlaubte Ziel-Hosts für ausgehenden Sandbox-Verkehr</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        Sandboxen dürfen nur zu Hosts auf dieser Allowlist ausgehend verbinden — alles andere blockt
        der Egress-Proxy fail-closed. Der LLM-Endpunkt (<span className="mono">api.anthropic.com</span>)
        ist fest erlaubt; hier ergänzen Sie die Hosts Ihrer Zielsysteme (z.&nbsp;B. das Zammad-Host).
        Muster: exakter Host (<span className="mono">helpdesk.example.com</span>) oder Wildcard
        (<span className="mono">*.example.com</span>).
      </p>

      {data && (
        <div
          className="card mb-5"
          style={{
            padding: "10px 14px",
            borderLeft: `3px solid ${data.enforced ? "var(--moss, #5a7d5a)" : "var(--clay)"}`,
          }}
        >
          {data.enforced ? (
            <span className="text-xs">
              ● Egress-Enforcement <b>aktiv</b> (docker-Provider). Änderungen wirken sofort.
            </span>
          ) : (
            <span className="text-xs">
              ○ Egress-Enforcement <b>nicht aktiv</b>. Einträge werden gespeichert, greifen aber erst
              mit <span className="mono">COVEY_SANDBOX_PROVIDER=docker</span> und
              <span className="mono"> COVEY_EGRESS_ENFORCE=true</span>.
            </span>
          )}
        </div>
      )}

      <h2 className="text-base font-medium mb-2">Fest erlaubt</h2>
      <div className="flex flex-wrap gap-2 mb-6">
        {defaults.map((d) => (
          <span key={d} className="mono text-xs" style={{ background: "var(--surface-2, #f2efe9)", padding: "3px 9px", borderRadius: 6 }}>
            {d}
          </span>
        ))}
        {defaults.length === 0 && <span className="muted text-xs">—</span>}
      </div>

      <h2 className="text-base font-medium mb-2">Verwaltete Hosts</h2>
      <div className="grid gap-2 mb-5" style={{ maxWidth: 720 }}>
        {entries.map((e) => (
          <div key={e.id} className="card flex items-center gap-3" style={{ padding: "10px 14px" }}>
            <span className="mono text-sm">{e.pattern}</span>
            {e.note && <span className="muted text-xs truncate">{e.note}</span>}
            {canEdit && (
              <button
                className="btn sm danger ml-auto"
                disabled={remove.isPending}
                onClick={() => {
                  if (confirm(`Host „${e.pattern}" von der Allowlist entfernen?`)) remove.mutate(e.id);
                }}
              >
                Entfernen
              </button>
            )}
          </div>
        ))}
        {entries.length === 0 && !cfg.isLoading && (
          <p className="muted text-xs">Noch keine verwalteten Hosts — nur die fest erlaubten gelten.</p>
        )}
      </div>

      {canEdit ? (
        <div className="card" style={{ padding: "14px 16px", maxWidth: 720 }}>
          <h2 className="text-base font-medium mb-2">Host erlauben</h2>
          <div className="flex flex-wrap items-end gap-2">
            <label className="block">
              <span className="muted text-xs block mb-1">Host / Muster</span>
              <input
                className="mono"
                style={{ minWidth: 260 }}
                placeholder="helpdesk.example.com oder *.example.com"
                value={pattern}
                onChange={(ev) => setPattern(ev.target.value)}
              />
            </label>
            <label className="block flex-1" style={{ minWidth: 180 }}>
              <span className="muted text-xs block mb-1">Notiz (optional)</span>
              <input
                placeholder="z. B. Zammad-Produktiv"
                value={note}
                onChange={(ev) => setNote(ev.target.value)}
              />
            </label>
            <button
              className="btn primary"
              disabled={add.isPending || !pattern.trim()}
              onClick={() => add.mutate()}
            >
              Hinzufügen
            </button>
          </div>
          {error && (
            <p className="text-xs mt-2" style={{ color: "var(--clay)" }}>
              {error}
            </p>
          )}
        </div>
      ) : (
        <p className="muted text-xs mt-3">
          Ihre Rolle ({me.Role}) kann die Allowlist ansehen, aber nicht ändern.
        </p>
      )}
    </div>
  );
}

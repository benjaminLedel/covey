import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, del, put, type Agent, type Principal, type SecretCheck, type SecretPreview } from "../api";

const canEdit = (role: string) => role === "platform_admin" || role === "security";

export default function Secrets({ me }: { me: Principal }) {
  const qc = useQueryClient();
  const keys = useQuery({
    queryKey: ["secrets"],
    queryFn: () => api<SecretPreview[]>("/secrets"),
    retry: false,
  });
  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[]>("/agents") });
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [check, setCheck] = useState<({ key: string } & SecretCheck) | null>(null);

  const save = useMutation({
    mutationFn: () =>
      put<{ ok: boolean; check: SecretCheck }>(`/secrets/${encodeURIComponent(key)}`, { value }),
    onSuccess: (res) => {
      setCheck({ key, ...res.check });
      setKey("");
      setValue("");
      qc.invalidateQueries({ queryKey: ["secrets"] });
    },
  });
  const remove = useMutation({
    mutationFn: (k: string) => del(`/secrets/${encodeURIComponent(k)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["secrets"] }),
  });

  if (keys.isError) {
    return (
      <div>
        <h1 className="text-[22px] mb-3">Secrets</h1>
        <p className="muted">Ihre Rolle ({me.Role}) hat auf den Secret-Store keinen Zugriff.</p>
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">Secrets</h1>
        <span className="muted">AES-GCM-verschlüsselt in Postgres, write-only</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        Der Broker reicht Secrets zur Laufzeit kurzlebig an Sandboxen durch — der volle Wert ist über
        die API nie wieder lesbar, nur die ersten Zeichen dienen als Wiedererkennungshilfe. Konvention: <span className="mono">&lt;system&gt;_token</span> und{" "}
        <span className="mono">&lt;system&gt;_url</span> (z. B. zammad), plus{" "}
        <span className="mono">anthropic_api_key</span> für die Runtime (Abo-Accounts stattdessen:{" "}
        <span className="mono">claude_code_oauth_token</span> aus <span className="mono">claude setup-token</span>).
        Ein Secret erreicht einen Agenten nur, wenn es ihm <strong>explizit zugewiesen</strong> ist —
        ohne Zuweisung bleibt es wirkungslos. Zuweisen und agent-eigene Secrets pflegst du auf der
        Agenten-Seite (Tab „Secrets"); hier ist nur der zentrale Vault.
      </p>

      {canEdit(me.Role) && (
        <form
          className="card mb-4 flex gap-3 items-end flex-wrap"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <div className="min-w-48">
            <label>Key</label>
            <input value={key} onChange={(e) => setKey(e.target.value)} className="mono" placeholder="zammad_token" required />
          </div>
          <div className="flex-1 min-w-52">
            <label>Wert</label>
            <input type="password" value={value} onChange={(e) => setValue(e.target.value)} required />
          </div>
          <button className="btn primary" disabled={save.isPending}>
            Speichern
          </button>
          {check && (
            <p
              className="text-xs w-full m-0"
              style={{ color: check.checked && !check.valid ? "var(--danger, #b91c1c)" : check.valid ? "var(--success, #15803d)" : "var(--text-secondary)" }}
            >
              {check.checked && check.valid && (
                <>
                  <span className="mono">{check.key}</span> gespeichert — Credential live geprüft, gültig. ✓
                </>
              )}
              {check.checked && !check.valid && (
                <>
                  <span className="mono">{check.key}</span> gespeichert, aber das Credential ist <strong>ungültig</strong>: {check.hint}
                </>
              )}
              {!check.checked && (
                <>
                  <span className="mono">{check.key}</span> gespeichert.{check.hint ? ` ${check.hint}` : ""}
                </>
              )}
            </p>
          )}
        </form>
      )}

      {(keys.data ?? []).map((s) => (
        <div key={s.key} className="card mb-2" style={{ padding: "11px 15px" }}>
          <div className="flex items-center gap-4">
            <span className="mono text-sm flex-1">{s.key}</span>
            <span className="mono muted text-xs">
              {s.prefix ? <span style={{ color: "var(--text-secondary)" }}>{s.prefix}</span> : null}
              ••••••••
            </span>
            {canEdit(me.Role) && (
              <button className="btn sm" onClick={() => remove.mutate(s.key)}>
                Löschen
              </button>
            )}
          </div>
          <Assignments secret={s} agents={agents.data ?? []} />
        </div>
      ))}
      {keys.data?.length === 0 && <p className="muted">Noch keine Secrets hinterlegt.</p>}
    </div>
  );
}

// Assignments: rein passive Anzeige, wem ein Org-Secret zugewiesen ist.
// Gepflegt wird die Zuweisung auf der Agenten-Seite (Tab „Secrets").
function Assignments({ secret, agents }: { secret: SecretPreview; agents: Agent[] }) {
  const assigned = secret.agent_ids ?? [];
  const name = (id: string) => agents.find((a) => a.id === id)?.display_name ?? id.slice(0, 8);

  return (
    <div className="flex items-center gap-2 flex-wrap mt-2 text-xs">
      <span className="muted">zugewiesen an:</span>
      {assigned.length === 0 && (
        <span style={{ color: "var(--text-warning, #b45309)" }}>
          niemanden — erreicht keinen Agenten
        </span>
      )}
      {assigned.map((id) => (
        <span key={id} className="badge st-triage">
          {name(id)}
        </span>
      ))}
    </div>
  );
}

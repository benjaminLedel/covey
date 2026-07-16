import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api, del, post,
  type EgressStatus, type EgressTemplate, type Principal,
} from "../api";
import { AddHostForm, EgressLogTable, HostChips } from "../components/EgressBits";

// Egress-Seite: wiederverwendbare Host-Templates und globales Monitoring.
// Die Zuweisung (Templates + eigene Hosts) geschieht pro Agent im Egress-
// Reiter der Agenten-Seite; der Proxy erzwingt die effektive Allowlist
// fail-closed.
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
        Jeder Agent darf ausgehend nur Hosts auf seiner Allowlist erreichen — alles andere blockt der
        Proxy fail-closed. Hier pflegst du die wiederverwendbaren <b>Templates</b> (Host-Sets) und
        siehst das <b>Monitoring</b> über alle Agenten. Zugewiesen wird pro Agent im Reiter{" "}
        <span className="mono">Egress</span> der jeweiligen Agenten-Seite.
      </p>

      {status.data && <StatusBanner status={status.data} />}

      <Templates canEdit={canEdit} />

      <section>
        <h2 className="text-base font-medium mb-2">Monitoring</h2>
        <p className="muted text-xs mb-3" style={{ maxWidth: 620 }}>
          Jede Egress-Entscheidung über alle Agenten. Häufungen von „blockiert" deuten auf fehlende
          Allowlist-Einträge — oder auf einen Agenten, der wohin will, wo er nicht hingehört.
        </p>
        <EgressLogTable />
      </section>
    </div>
  );
}

function StatusBanner({ status }: { status: EgressStatus }) {
  return (
    <div
      className="card mb-6 text-xs"
      style={{
        padding: "10px 14px",
        borderLeft: `3px solid ${status.enforced ? "var(--text-success)" : "var(--text-warning)"}`,
      }}
    >
      {status.enforced ? (
        <span>
          <b>Enforcement aktiv</b> (docker) — fest erlaubt für alle Agenten:{" "}
          {status.defaults.map((d) => (
            <span key={d} className="chip fixed" style={{ marginRight: 4 }}>{d}</span>
          ))}
        </span>
      ) : (
        <span>
          <b>Enforcement nicht aktiv</b> — Einträge werden gespeichert und greifen, sobald der Server
          mit <span className="mono">COVEY_SANDBOX_PROVIDER=docker</span> und{" "}
          <span className="mono">COVEY_EGRESS_ENFORCE=true</span> läuft.
        </span>
      )}
    </div>
  );
}

// --- Templates ---

function Templates({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const templates = useQuery({ queryKey: ["egress", "templates"], queryFn: () => api<EgressTemplate[]>("/egress/templates") });
  const invalidate = () => qc.invalidateQueries({ queryKey: ["egress"] });
  const remove = useMutation({ mutationFn: (id: string) => del(`/egress/templates/${id}`), onSuccess: invalidate });

  const list = templates.data ?? [];
  return (
    <section className="mb-8">
      <h2 className="text-base font-medium mb-2">Templates</h2>
      <p className="muted text-xs mb-3" style={{ maxWidth: 620 }}>
        Wiederverwendbare Host-Sets, z.&nbsp;B. „Zammad-Prod" oder „Paket-Registries". Ein Template
        mehreren Agenten zuzuweisen ist der Normalfall — Einzel-Hosts nur für Ausnahmen direkt am
        Agenten pflegen.
      </p>
      <div className="grid gap-3 mb-4" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(320px, 1fr))" }}>
        {list.map((t) => (
          <TemplateCard key={t.id} tpl={t} canEdit={canEdit} onChange={invalidate} onDelete={() => remove.mutate(t.id)} />
        ))}
        {list.length === 0 && !templates.isLoading && (
          <p className="muted text-xs">
            Noch keine Templates — lege unten das erste an{canEdit ? "" : " (Rolle platform_admin oder security nötig)"}.
          </p>
        )}
      </div>
      {canEdit && <CreateTemplate onCreated={invalidate} />}
    </section>
  );
}

function TemplateCard({
  tpl, canEdit, onChange, onDelete,
}: {
  tpl: EgressTemplate; canEdit: boolean; onChange: () => void; onDelete: () => void;
}) {
  const delHost = useMutation({ mutationFn: (id: string) => del(`/egress/template-hosts/${id}`), onSuccess: onChange });
  return (
    <div className="card fade" style={{ padding: "13px 15px" }}>
      <div className="flex items-baseline gap-2 mb-1">
        <span className="font-medium">{tpl.name}</span>
        <span className="muted text-xs">{tpl.hosts.length} Host{tpl.hosts.length === 1 ? "" : "s"}</span>
        {canEdit && (
          <button
            className="btn sm danger ml-auto"
            onClick={() => { if (confirm(`Template „${tpl.name}" löschen? Agenten verlieren die zugehörigen Hosts.`)) onDelete(); }}
          >
            Löschen
          </button>
        )}
      </div>
      {tpl.description && <p className="muted text-xs mb-2">{tpl.description}</p>}
      <div className="flex flex-wrap gap-1 mb-3">
        <HostChips
          hosts={tpl.hosts}
          canEdit={canEdit}
          onDelete={(id) => delHost.mutate(id)}
          emptyText="noch keine Hosts — unten hinzufügen"
        />
      </div>
      {canEdit && <AddHostForm onAdd={(pattern, note) => post(`/egress/templates/${tpl.id}/hosts`, { pattern, note }).then(onChange)} />}
    </div>
  );
}

// CreateTemplate: erst ein Button, aufgeklappt eine Karte (Muster wie beim
// Manifest-Upload auf der Zielsysteme-Seite).
function CreateTemplate({ onCreated }: { onCreated: () => void }) {
  const [show, setShow] = useState(false);
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [err, setErr] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () => post("/egress/templates", { name: name.trim(), description: desc.trim() }),
    onSuccess: () => { setName(""); setDesc(""); setErr(null); setShow(false); onCreated(); },
    onError: (e: Error) => setErr(e.message),
  });

  if (!show) return <button className="btn" onClick={() => setShow(true)}>Template anlegen…</button>;
  return (
    <div className="card" style={{ padding: "14px 16px", maxWidth: 560 }}>
      <div className="grid gap-2 mb-2" style={{ gridTemplateColumns: "1fr 2fr" }}>
        <label className="text-xs">
          Name
          <input placeholder="Zammad-Prod" value={name} autoFocus onChange={(e) => setName(e.target.value)} />
        </label>
        <label className="text-xs">
          Beschreibung (optional)
          <input placeholder="Helpdesk-Produktion + Wissensdatenbank" value={desc} onChange={(e) => setDesc(e.target.value)} />
        </label>
      </div>
      {err && <p className="text-xs mb-2" style={{ color: "var(--text-danger)" }}>{err}</p>}
      <div className="flex items-center gap-2">
        <button className="btn sm primary" disabled={create.isPending || !name.trim()} onClick={() => create.mutate()}>
          Anlegen
        </button>
        <button className="btn sm" onClick={() => { setShow(false); setErr(null); }}>Abbrechen</button>
      </div>
    </div>
  );
}

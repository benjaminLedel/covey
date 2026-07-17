import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, NavLink, Route, Routes, useNavigate, useParams } from "react-router-dom";
import {
  api, del, post,
  type EgressStats, type EgressStatus, type EgressTemplate, type Principal,
} from "../api";
import { AddHostForm, EgressLogTable, HostChips } from "../components/EgressBits";
import { ConfirmDialog, Modal } from "../components/Modal";

// Egress-Bereich mit Subseiten: Übersicht (Status + Monitoring) und Templates
// (Liste + Detailseite je Template). Die Zuweisung geschieht pro Agent im
// Egress-Reiter der Agenten-Seite; der Proxy erzwingt die effektive Allowlist
// fail-closed.
export default function Egress({ me }: { me: Principal }) {
  const canEdit = me.Role === "platform_admin" || me.Role === "security";
  return (
    <Routes>
      <Route index element={<Overview />} />
      <Route path="templates" element={<TemplatesPage canEdit={canEdit} />} />
      <Route path="templates/:id" element={<TemplateDetail canEdit={canEdit} />} />
    </Routes>
  );
}

// Kopf mit Titel + Sub-Navigation, auf allen Egress-Seiten gleich.
function Header() {
  return (
    <>
      <div className="flex items-baseline gap-3 mb-1">
        <h1 className="text-[22px]">Egress</h1>
        <span className="muted">Netzwerk-Ausgang der Sandboxen</span>
      </div>
      <nav className="subnav">
        <NavLink to="/egress" end className={({ isActive }) => (isActive ? "active" : "")}>
          Übersicht
        </NavLink>
        <NavLink to="/egress/templates" className={({ isActive }) => (isActive ? "active" : "")}>
          Templates
        </NavLink>
      </nav>
    </>
  );
}

// --- Übersicht: Status, 24h-Kennzahlen, Monitoring-Log ---

function Overview() {
  const status = useQuery({ queryKey: ["egress", "status"], queryFn: () => api<EgressStatus>("/egress") });
  const stats = useQuery({
    queryKey: ["egress", "stats"],
    queryFn: () => api<EgressStats>("/egress/stats"),
    refetchInterval: 30000,
  });
  const templates = useQuery({ queryKey: ["egress", "templates"], queryFn: () => api<EgressTemplate[]>("/egress/templates") });

  return (
    <div>
      <Header />

      <HowItWorks />

      {status.data && <StatusBanner status={status.data} />}

      <div className="stat-grid mb-6">
        <div className="card stat">
          <div className="v" style={{ color: "var(--text-success)" }}>{stats.data?.allowed_24h ?? "–"}</div>
          <div className="l">erlaubt, letzte 24 h</div>
        </div>
        <div className="card stat">
          <div className="v" style={{ color: stats.data?.blocked_24h ? "var(--text-danger)" : undefined }}>
            {stats.data?.blocked_24h ?? "–"}
          </div>
          <div className="l">blockiert, letzte 24 h</div>
        </div>
        <div className="card stat">
          <div className="v">{templates.data?.length ?? "–"}</div>
          <div className="l">
            <Link to="/egress/templates" style={{ color: "inherit" }}>Templates</Link>
          </div>
        </div>
        {(stats.data?.top_blocked.length ?? 0) > 0 && (
          <div className="card stat" style={{ gridColumn: "span 2" }}>
            <div className="l" style={{ marginTop: 0, marginBottom: 6 }}>am häufigsten blockiert (24 h)</div>
            <div className="flex flex-wrap gap-1">
              {stats.data!.top_blocked.map((t) => (
                <span key={t.host} className="chip" title={`${t.count}× blockiert`}>
                  {t.host}
                  <span className="src">{t.count}×</span>
                </span>
              ))}
            </div>
          </div>
        )}
      </div>

      <section>
        <h2 className="text-base font-medium mb-2">Monitoring</h2>
        <p className="muted text-xs mb-3" style={{ maxWidth: 620 }}>
          Jede Egress-Entscheidung über alle Agenten. Häufungen von „blockiert" deuten auf fehlende
          Allowlist-Einträge — oder auf einen Agenten, der wohin will, wo er nicht hingehört.
        </p>
        <EgressLogTable withAgentFilter />
      </section>
    </div>
  );
}

// HowItWorks erklärt das Modell in drei Schritten — einmal verstanden, per
// Klick dauerhaft ausblendbar.
function HowItWorks() {
  const [hidden, setHidden] = useState(() => localStorage.getItem("covey.egress.howto") === "1");
  if (hidden) return null;
  return (
    <div className="mb-5">
      <div className="steps">
        <div className="card step">
          <span className="n">1</span>
          <span>
            <b>Template anlegen</b>
            <p>
              Unter <Link to="/egress/templates">Templates</Link> ein wiederverwendbares Host-Set
              pflegen, z. B. „Zammad-Prod" oder „Paket-Registries".
            </p>
          </span>
        </div>
        <div className="card step">
          <span className="n">2</span>
          <span>
            <b>Agenten zuweisen</b>
            <p>
              Auf der Agenten-Seite im Reiter <span className="mono">Egress</span> Templates
              ankreuzen — Einzel-Hosts dort nur für Ausnahmen.
            </p>
          </span>
        </div>
        <div className="card step">
          <span className="n">3</span>
          <span>
            <b>Proxy erzwingt</b>
            <p>
              Jeder Agent erreicht nur Hosts seiner Allowlist, alles andere blockt der Proxy
              fail-closed — hier im Monitoring sichtbar.
            </p>
          </span>
        </div>
      </div>
      <button
        className="btn sm mt-2"
        style={{ border: "none", color: "var(--text-muted)", padding: "2px 4px" }}
        onClick={() => { localStorage.setItem("covey.egress.howto", "1"); setHidden(true); }}
      >
        Verstanden, ausblenden
      </button>
    </div>
  );
}

function StatusBanner({ status }: { status: EgressStatus }) {
  return (
    <div
      className="card mb-4 text-xs"
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

// --- Templates: Liste ---

function TemplatesPage({ canEdit }: { canEdit: boolean }) {
  const navigate = useNavigate();
  const templates = useQuery({ queryKey: ["egress", "templates"], queryFn: () => api<EgressTemplate[]>("/egress/templates") });
  const [showCreate, setShowCreate] = useState(false);

  const list = templates.data ?? [];
  return (
    <div>
      <Header />
      <div className="flex items-start gap-3 mb-4">
        <p className="muted text-xs" style={{ maxWidth: 560, margin: 0 }}>
          Wiederverwendbare Host-Sets. Ein Template mehreren Agenten zuzuweisen ist der Normalfall —
          Einzel-Hosts nur für Ausnahmen direkt am Agenten pflegen. Ein Klick öffnet die Detailseite
          mit Hosts und Verwendung.
        </p>
        {canEdit && (
          <button className="btn primary ml-auto" style={{ flexShrink: 0 }} onClick={() => setShowCreate(true)}>
            Neues Template
          </button>
        )}
      </div>

      <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(300px, 1fr))" }}>
        {list.map((t) => (
          <Link
            key={t.id}
            to={`/egress/templates/${t.id}`}
            className="card fade block"
            style={{ padding: "13px 15px", textDecoration: "none", color: "inherit" }}
          >
            <div className="flex items-baseline gap-2 mb-1">
              <span className="font-medium">{t.name}</span>
              <span className="muted text-xs ml-auto">
                {t.agents.length === 0 ? "nicht zugewiesen" : `${t.agents.length} Agent${t.agents.length === 1 ? "" : "en"}`}
              </span>
            </div>
            {t.description && <p className="muted text-xs mb-2" style={{ margin: "0 0 8px" }}>{t.description}</p>}
            <div className="flex flex-wrap gap-1">
              {t.hosts.slice(0, 4).map((h) => (
                <span key={h.id} className="chip">{h.pattern}</span>
              ))}
              {t.hosts.length > 4 && <span className="muted text-xs" style={{ alignSelf: "center" }}>+{t.hosts.length - 4} weitere</span>}
              {t.hosts.length === 0 && <span className="muted text-xs">noch keine Hosts</span>}
            </div>
          </Link>
        ))}
        {list.length === 0 && !templates.isLoading && (
          <p className="muted text-xs">
            Noch keine Templates{canEdit ? ' — lege mit „Neues Template" das erste an.' : " (Anlegen erfordert Rolle platform_admin oder security)."}
          </p>
        )}
      </div>

      {showCreate && (
        <CreateTemplateModal
          onClose={() => setShowCreate(false)}
          onCreated={(id) => { setShowCreate(false); navigate(`/egress/templates/${id}`); }}
        />
      )}
    </div>
  );
}

// CreateTemplateModal legt ein Template an und führt direkt auf dessen
// Detailseite, wo die Hosts gepflegt werden.
function CreateTemplateModal({ onClose, onCreated }: { onClose: () => void; onCreated: (id: string) => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [err, setErr] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () => post<EgressTemplate>("/egress/templates", { name: name.trim(), description: desc.trim() }),
    onSuccess: (t) => { qc.invalidateQueries({ queryKey: ["egress"] }); onCreated(t.id); },
    onError: (e: Error) => setErr(e.message),
  });

  return (
    <Modal
      title="Neues Template"
      onClose={onClose}
      footer={
        <>
          <button className="btn sm" onClick={onClose}>Abbrechen</button>
          <button className="btn sm primary" disabled={create.isPending || !name.trim()} onClick={() => create.mutate()}>
            Anlegen
          </button>
        </>
      }
    >
      <p className="muted text-xs" style={{ margin: "0 0 12px" }}>
        Ein Template bündelt Ziel-Hosts, die zusammengehören — die Hosts pflegst du gleich auf der
        Detailseite.
      </p>
      <label>
        Name
        <input
          placeholder="Zammad-Prod"
          value={name}
          autoFocus
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && name.trim() && create.mutate()}
        />
      </label>
      <label style={{ marginTop: 10 }}>
        Beschreibung (optional)
        <input
          placeholder="Helpdesk-Produktion + Wissensdatenbank"
          value={desc}
          onChange={(e) => setDesc(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && name.trim() && create.mutate()}
        />
      </label>
      {err && <p className="text-xs mt-2" style={{ color: "var(--text-danger)", margin: "8px 0 0" }}>{err}</p>}
    </Modal>
  );
}

// --- Templates: Detailseite ---

function TemplateDetail({ canEdit }: { canEdit: boolean }) {
  const { id } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const templates = useQuery({ queryKey: ["egress", "templates"], queryFn: () => api<EgressTemplate[]>("/egress/templates") });
  const invalidate = () => qc.invalidateQueries({ queryKey: ["egress"] });

  const [confirmDelete, setConfirmDelete] = useState(false);
  const remove = useMutation({
    mutationFn: () => del(`/egress/templates/${id}`),
    onSuccess: () => { invalidate(); navigate("/egress/templates"); },
  });
  const delHost = useMutation({ mutationFn: (hid: string) => del(`/egress/template-hosts/${hid}`), onSuccess: invalidate });

  const tpl = (templates.data ?? []).find((t) => t.id === id);
  if (templates.isLoading) return <Header />;
  if (!tpl) {
    return (
      <div>
        <Header />
        <p className="muted text-sm">
          Template nicht gefunden — <Link to="/egress/templates">zurück zur Liste</Link>.
        </p>
      </div>
    );
  }

  return (
    <div>
      <Header />
      <p className="text-xs mb-3">
        <Link to="/egress/templates" className="muted" style={{ textDecoration: "none" }}>← Alle Templates</Link>
      </p>

      <div className="flex items-baseline gap-3 mb-1">
        <h2 className="text-lg font-medium">{tpl.name}</h2>
        {canEdit && (
          <button className="btn sm danger ml-auto" onClick={() => setConfirmDelete(true)}>Löschen</button>
        )}
      </div>
      {tpl.description && <p className="muted text-xs mb-4">{tpl.description}</p>}

      <section className="card mb-5" style={{ padding: "14px 16px", maxWidth: 640 }}>
        <p className="text-xs font-medium mb-2" style={{ margin: "0 0 8px" }}>Erlaubte Hosts</p>
        <div className="flex flex-wrap gap-1 mb-3">
          <HostChips
            hosts={tpl.hosts}
            canEdit={canEdit}
            onDelete={(hid) => delHost.mutate(hid)}
            emptyText="noch keine Hosts — unten hinzufügen"
          />
        </div>
        {canEdit && (
          <AddHostForm onAdd={(pattern, note) => post(`/egress/templates/${tpl.id}/hosts`, { pattern, note }).then(invalidate)} />
        )}
        <p className="muted text-xs mt-2" style={{ margin: "8px 0 0" }}>
          Exakter Host (<span className="mono">api.example.com</span>) oder Wildcard für alle
          Subdomains (<span className="mono">*.example.com</span>) — ohne Schema, Pfad oder Port.
        </p>
      </section>

      <section className="mb-5">
        <p className="text-xs font-medium mb-2">Verwendet von</p>
        {tpl.agents.length === 0 ? (
          <p className="muted text-xs">
            Noch keinem Agenten zugewiesen — das geschieht auf der Agenten-Seite im Reiter{" "}
            <span className="mono">Egress</span>.
          </p>
        ) : (
          <div className="flex flex-wrap gap-2">
            {tpl.agents.map((a) => (
              <Link key={a.id} to={`/agents/${a.id}`} className="chip" style={{ textDecoration: "none" }}>
                {a.display_name || a.slug}
                <span className="src">{a.slug}</span>
              </Link>
            ))}
          </div>
        )}
      </section>

      {confirmDelete && (
        <ConfirmDialog
          title={`Template „${tpl.name}" löschen?`}
          onClose={() => setConfirmDelete(false)}
          onConfirm={() => remove.mutate()}
          pending={remove.isPending}
        >
          <p style={{ margin: 0 }}>
            {tpl.agents.length > 0 ? (
              <>
                Das Template ist <b>{tpl.agents.length} Agent{tpl.agents.length === 1 ? "en" : "en"}</b>{" "}
                zugewiesen — sie verlieren die zugehörigen Hosts und der Proxy blockt diese Ziele
                künftig.
              </>
            ) : (
              <>Das Template ist keinem Agenten zugewiesen; es gehen nur die gepflegten Hosts verloren.</>
            )}
          </p>
        </ConfirmDialog>
      )}
    </div>
  );
}

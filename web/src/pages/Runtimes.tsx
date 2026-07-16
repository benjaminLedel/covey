import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api,
  patch,
  statusLabel,
  type Agent,
  type Principal,
  type RuntimeInfo,
  type SetupStep,
} from "../api";

// Backtick-`code`-Spannen monospaced rendern — hält die Anleitungstexte
// datengetrieben (aus dem Runtime-Plugin), ohne HTML im Backend.
function renderText(text: string) {
  return text.split(/(`[^`]+`)/g).map((part, i) =>
    part.startsWith("`") && part.endsWith("`") ? (
      <span key={i} className="mono">
        {part.slice(1, -1)}
      </span>
    ) : (
      <span key={i}>{part}</span>
    ),
  );
}

function Steps({ steps }: { steps: SetupStep[] }) {
  return (
    <ol className="text-sm" style={{ paddingLeft: 18, listStyle: "decimal", lineHeight: 1.7 }}>
      {steps.map((step, i) => (
        <li key={i}>
          {renderText(step.text)}
          {step.items && (
            <ul className="muted" style={{ paddingLeft: 16, listStyle: "disc", marginTop: 2 }}>
              {step.items.map((it, j) => (
                <li key={j}>{renderText(it)}</li>
              ))}
            </ul>
          )}
        </li>
      ))}
    </ol>
  );
}

// Runtimes-Seite (nav nur für platform_admin): Übersicht der im Daemon
// registrierten Runtime-Plugins plus Umschalten der Runtime je Agent. Metadaten
// und Einrichtungs-Anleitung kommen aus der Plugin-Registry (GET /runtimes) —
// eine neue Runtime erscheint hier automatisch.
export default function Runtimes({ me }: { me: Principal }) {
  const qc = useQueryClient();
  const [openInfo, setOpenInfo] = useState<string | null>(null);
  const runtimes = useQuery({
    queryKey: ["runtimes"],
    queryFn: () => api<RuntimeInfo[]>("/runtimes"),
  });
  const agents = useQuery({
    queryKey: ["agents"],
    queryFn: () => api<Agent[] | null>("/agents"),
  });

  const canEdit = me.Role === "platform_admin" || me.Role === "agent_owner";

  const setRuntime = useMutation({
    mutationFn: ({ id, runtime }: { id: string; runtime: string }) =>
      patch(`/agents/${id}/runtime`, { runtime }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["agents"] });
      qc.invalidateQueries({ queryKey: ["agent"] });
    },
  });

  const list = runtimes.data ?? [];

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">Runtimes</h1>
        <span className="muted">im Daemon registriert, pro Agent umschaltbar</span>
      </div>
      <p className="muted text-xs mb-5" style={{ maxWidth: 640 }}>
        Runtimes sind die austauschbare Naht zwischen Control Plane und Sandbox — im Code
        selbstbeschreibende Plugins. Die Runtime eines Agenten greift beim nächsten Task-Dispatch,
        laufende Sessions bleiben unberührt. Das <span className="mono">ⓘ</span> je Runtime zeigt die
        Einrichtung Schritt für Schritt.
      </p>

      {/* Übersicht mit Info-Button je Runtime */}
      <div className="grid gap-3 mb-6" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(260px, 1fr))" }}>
        {list.map((rt) => (
          <div key={rt.name} className="card" style={{ padding: "14px 16px" }}>
            <div className="flex items-center gap-2 mb-1">
              <span className="font-medium">{rt.label}</span>
              <span className="mono text-xs muted">{rt.name}</span>
              {rt.setup?.length > 0 && (
                <button
                  className="btn sm ml-auto"
                  aria-expanded={openInfo === rt.name}
                  title="Einrichtung anzeigen"
                  onClick={() => setOpenInfo((cur) => (cur === rt.name ? null : rt.name))}
                >
                  ⓘ Info
                </button>
              )}
            </div>
            <p className="muted text-xs mb-2">{rt.description}</p>
            <span
              className="text-[11px]"
              style={{ color: rt.needs_credential ? "var(--clay)" : "var(--text-secondary)" }}
            >
              {rt.needs_credential ? "● braucht Credential (Secrets)" : "● keine Credentials"}
            </span>
            {openInfo === rt.name && (
              <div className="mt-3 pt-3" style={{ borderTop: "0.5px solid var(--border)" }}>
                <div className="text-xs font-medium mb-1">Einrichtung — {rt.label}</div>
                <Steps steps={rt.setup} />
              </div>
            )}
          </div>
        ))}
      </div>

      {/* Umschalten je Agent */}
      <h2 className="text-base font-medium mb-2">Agenten</h2>
      {(agents.data ?? []).map((a) => (
        <div key={a.id} className="card mb-2 flex items-center gap-4" style={{ padding: "11px 15px" }}>
          <div className="flex-1 min-w-0">
            <span className="font-medium">{a.display_name}</span>{" "}
            <span className="mono text-xs muted">{a.slug}</span>
          </div>
          <span className="muted text-xs">{statusLabel[a.status] ?? a.status}</span>
          <select
            value={a.runtime}
            disabled={!canEdit || setRuntime.isPending}
            onChange={(e) => setRuntime.mutate({ id: a.id, runtime: e.target.value })}
            className="mono"
            style={{ width: 160 }}
          >
            {list.length === 0 && <option value={a.runtime}>{a.runtime}</option>}
            {list.map((rt) => (
              <option key={rt.name} value={rt.name}>
                {rt.name}
              </option>
            ))}
          </select>
        </div>
      ))}
      {agents.data?.length === 0 && <p className="muted">Noch keine Agenten angelegt.</p>}
      {!canEdit && (
        <p className="muted text-xs mt-3">
          Ihre Rolle ({me.Role}) kann Runtimes ansehen, aber nicht umschalten.
        </p>
      )}
    </div>
  );
}

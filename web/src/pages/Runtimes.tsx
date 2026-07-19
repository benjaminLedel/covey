import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, type RuntimeInfo, type SetupStep } from "../api";

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
// registrierten Runtime-Plugins samt Einrichtungs-Anleitung. Metadaten kommen
// aus der Plugin-Registry (GET /runtimes) — eine neue Runtime erscheint hier
// automatisch. Runtime, Modell und Turn-Limit je Agent werden auf der
// Agenten-Seite im Reiter Einstellungen umgeschaltet.
export default function Runtimes() {
  const [openInfo, setOpenInfo] = useState<string | null>(null);
  const runtimes = useQuery({
    queryKey: ["runtimes"],
    queryFn: () => api<RuntimeInfo[]>("/runtimes"),
  });

  const list = runtimes.data ?? [];

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">Runtimes</h1>
        <span className="muted">im Daemon registrierte Plugins</span>
      </div>
      <p className="muted text-xs mb-5" style={{ maxWidth: 640 }}>
        Runtimes sind die austauschbare Naht zwischen Control Plane und Sandbox — im Code
        selbstbeschreibende Plugins. Das <span className="mono">ⓘ</span> je Runtime zeigt die
        Einrichtung Schritt für Schritt. Welche Runtime (und welches Modell) ein Agent nutzt,
        stellen Sie auf der Agenten-Seite im Reiter <b>Einstellungen</b> um.
      </p>

      <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(260px, 1fr))" }}>
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
    </div>
  );
}

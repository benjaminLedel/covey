import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, del, patch, type Principal, type TargetPlugin } from "../api";

const exampleManifest = `{
  "name": "helpdesk",
  "label": "Helpdesk",
  "description": "Beispiel: REST-Zielsystem per Manifest",
  "auth": { "header": "X-API-Key", "format": "{token}" },
  "webhook": {
    "id_field": "issue.id",
    "event_id_field": "comment.id",
    "title_field": "issue.title",
    "body_field": "comment.text",
    "ignore_when": [{ "field": "comment.author_type", "equals": "agent" }]
  },
  "actions": {
    "get_issue": { "method": "GET", "path": "/issues/{issue_id}" },
    "comment": { "method": "POST", "path": "/issues/{issue_id}/comments" }
  },
  "prompt_doc": "Verfügbare helpdesk-Aktionen: get_issue {\\"issue_id\\":N}, comment {\\"issue_id\\":N,\\"text\\":\\"...\\"}."
}`;

// Zielsysteme-Seite: kompilierte Built-in-Plugins und hochgeladene
// Manifest-Plugins der Organisation — aktivieren/deaktivieren wirkt sofort
// und fail-closed (Webhook-Eingang zu, Broker verweigert Credentials).
export default function Targets({ me }: { me: Principal }) {
  const qc = useQueryClient();
  const [manifestText, setManifestText] = useState("");
  const [showUpload, setShowUpload] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<TargetPlugin[] | null>("/targets"),
  });

  const canEdit = me.Role === "platform_admin" || me.Role === "security";
  const invalidate = () => qc.invalidateQueries({ queryKey: ["targets"] });

  const toggle = useMutation({
    mutationFn: ({ name, enabled }: { name: string; enabled: boolean }) =>
      patch(`/targets/${name}`, { enabled }),
    onSuccess: invalidate,
  });

  const remove = useMutation({
    mutationFn: (name: string) => del(`/targets/${name}`),
    onSuccess: invalidate,
  });

  const upload = useMutation({
    mutationFn: (raw: string) =>
      api("/targets", { method: "POST", body: raw }),
    onSuccess: () => {
      setManifestText("");
      setShowUpload(false);
      setError(null);
      invalidate();
    },
    onError: (e: Error) => setError(e.message),
  });

  const readFile = (file: File) => {
    const reader = new FileReader();
    reader.onload = () => setManifestText(String(reader.result ?? ""));
    reader.readAsText(file);
  };

  const list = targets.data ?? [];

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">Zielsysteme</h1>
        <span className="muted">Plugins — eingebaut oder als Manifest hochgeladen</span>
      </div>
      <p className="muted text-xs mb-5" style={{ maxWidth: 640 }}>
        Zielsysteme sind Plugins: Eingebaute (z.&nbsp;B. Zammad) werden mitkompiliert, eigene
        REST-Systeme binden Sie ohne Deploy als JSON-Manifest an. Deaktivieren wirkt sofort und
        fail-closed — der Webhook-Eingang schließt, der Broker gibt keine Credentials mehr heraus.
        Zugangsdaten gehören unter <span className="mono">Secrets</span> (
        <span className="mono">&lt;name&gt;_token</span> und <span className="mono">&lt;name&gt;_url</span>,
        dem Agenten zugewiesen).
      </p>

      <div className="grid gap-3 mb-6" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(280px, 1fr))" }}>
        {list.map((p) => (
          <div key={p.name} className="card" style={{ padding: "14px 16px", opacity: p.enabled ? 1 : 0.6 }}>
            <div className="flex items-center gap-2 mb-1">
              <span className="font-medium">{p.label || p.name}</span>
              <span className="mono text-xs muted">{p.name}</span>
              <span
                className="text-[11px] ml-auto"
                style={{ color: p.kind === "custom" ? "var(--clay)" : "var(--text-secondary)" }}
              >
                {p.kind === "custom" ? "Manifest" : "eingebaut"}
              </span>
            </div>
            <p className="muted text-xs mb-3">{p.description || "—"}</p>
            <div className="flex items-center gap-2">
              <span className="text-[11px]" style={{ color: p.enabled ? "var(--text-secondary)" : "var(--clay)" }}>
                {p.enabled ? "● aktiv" : "○ deaktiviert"}
              </span>
              {canEdit && (
                <button
                  className="btn sm ml-auto"
                  disabled={toggle.isPending}
                  onClick={() => toggle.mutate({ name: p.name, enabled: !p.enabled })}
                >
                  {p.enabled ? "Deaktivieren" : "Aktivieren"}
                </button>
              )}
              {canEdit && p.kind === "custom" && (
                <button
                  className="btn sm danger"
                  disabled={remove.isPending}
                  onClick={() => {
                    if (confirm(`Manifest-Plugin „${p.name}" wirklich löschen?`)) remove.mutate(p.name);
                  }}
                >
                  Löschen
                </button>
              )}
            </div>
          </div>
        ))}
        {list.length === 0 && !targets.isLoading && (
          <p className="muted">Keine Zielsysteme registriert — dieses Binary wurde ohne Built-in-Plugins gebaut.</p>
        )}
      </div>

      {canEdit && (
        <>
          <h2 className="text-base font-medium mb-2">Eigenes Zielsystem anbinden</h2>
          {!showUpload ? (
            <button className="btn" onClick={() => setShowUpload(true)}>
              Manifest hochladen…
            </button>
          ) : (
            <div className="card" style={{ padding: "14px 16px", maxWidth: 720 }}>
              <p className="muted text-xs mb-2">
                JSON-Manifest einfügen oder als Datei wählen. Es beschreibt Webhook-Mapping, Aktionen
                (Methode + Pfad mit <span className="mono">{"{param}"}</span>-Platzhaltern) und den
                Auth-Header — die generische REST-Engine übernimmt den Rest.
              </p>
              <textarea
                className="mono"
                style={{ width: "100%", minHeight: 220, fontSize: 12 }}
                placeholder={exampleManifest}
                value={manifestText}
                onChange={(e) => setManifestText(e.target.value)}
              />
              {error && (
                <p className="text-xs mb-2" style={{ color: "var(--clay)" }}>
                  {error}
                </p>
              )}
              <div className="flex items-center gap-2 mt-2">
                <input
                  ref={fileRef}
                  type="file"
                  accept=".json,application/json"
                  style={{ display: "none" }}
                  onChange={(e) => e.target.files?.[0] && readFile(e.target.files[0])}
                />
                <button className="btn sm" onClick={() => fileRef.current?.click()}>
                  Datei wählen…
                </button>
                <button className="btn sm" onClick={() => setManifestText(exampleManifest)}>
                  Beispiel einfügen
                </button>
                <button
                  className="btn sm primary ml-auto"
                  disabled={upload.isPending || !manifestText.trim()}
                  onClick={() => upload.mutate(manifestText)}
                >
                  Hochladen
                </button>
                <button
                  className="btn sm"
                  onClick={() => {
                    setShowUpload(false);
                    setError(null);
                  }}
                >
                  Abbrechen
                </button>
              </div>
            </div>
          )}
        </>
      )}
      {!canEdit && (
        <p className="muted text-xs mt-3">
          Ihre Rolle ({me.Role}) kann Zielsysteme ansehen, aber nicht verwalten.
        </p>
      )}
    </div>
  );
}

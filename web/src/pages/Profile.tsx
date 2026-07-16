import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, del, patch, roleLabel, type Principal } from "../api";

type Session = { created_at: string; expires_at: string; current: boolean };

// Persönliche Einstellungen des angemeldeten Nutzers: Anzeigename, Passwort,
// Sitzungen. Rolle und Manager verwaltet der Admin unter „Benutzer".
export default function Profile({ me }: { me: Principal }) {
  const qc = useQueryClient();
  const [name, setName] = useState(me.DisplayName);
  const [currentPw, setCurrentPw] = useState("");
  const [newPw, setNewPw] = useState("");
  const [note, setNote] = useState("");

  const sessions = useQuery({
    queryKey: ["sessions"],
    queryFn: () => api<Session[]>("/auth/sessions"),
  });

  const saveName = useMutation({
    mutationFn: () => patch("/auth/me", { display_name: name.trim() }),
    onSuccess: () => {
      setNote("Anzeigename gespeichert.");
      qc.invalidateQueries({ queryKey: ["me"] });
    },
    onError: (e) => setNote(String(e)),
  });

  const savePw = useMutation({
    mutationFn: () => patch("/auth/me", { password: newPw, current_password: currentPw }),
    // Ein Passwortwechsel widerruft alle Sessions — auch die aktuelle.
    // Die nächste API-Antwort ist 401 und die Login-Maske erscheint.
    onSuccess: () => qc.invalidateQueries({ queryKey: ["me"] }),
    onError: (e) => setNote(String(e)),
  });

  const revokeOthers = useMutation({
    mutationFn: () => del<{ revoked: number }>("/auth/sessions"),
    onSuccess: (res) => {
      setNote(res.revoked === 1 ? "1 andere Sitzung beendet." : `${res.revoked} andere Sitzungen beendet.`);
      qc.invalidateQueries({ queryKey: ["sessions"] });
    },
  });

  const others = (sessions.data ?? []).filter((s) => !s.current).length;

  return (
    <div style={{ maxWidth: 560 }}>
      <h1 className="text-[22px] mb-1">Profil</h1>
      <p className="muted text-xs mb-4">
        {me.Email} · {roleLabel[me.Role] ?? me.Role} — Rolle und Org-Zuordnung verwaltet ein Admin unter „Benutzer".
      </p>
      {note && <p className="text-xs mb-3" style={{ color: "var(--text-secondary)" }}>{note}</p>}

      <form
        className="card mb-4 flex gap-3 items-end flex-wrap"
        onSubmit={(e) => {
          e.preventDefault();
          saveName.mutate();
        }}
      >
        <div className="flex-1 min-w-52">
          <label>Anzeigename</label>
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <button className="btn primary" disabled={saveName.isPending || name.trim() === me.DisplayName}>
          Speichern
        </button>
      </form>

      <form
        className="card mb-4"
        onSubmit={(e) => {
          e.preventDefault();
          savePw.mutate();
        }}
      >
        <div className="flex gap-3 items-end flex-wrap">
          <div className="flex-1 min-w-44">
            <label>Aktuelles Passwort</label>
            <input type="password" value={currentPw} onChange={(e) => setCurrentPw(e.target.value)} autoComplete="current-password" required />
          </div>
          <div className="flex-1 min-w-44">
            <label>Neues Passwort</label>
            <input type="password" value={newPw} onChange={(e) => setNewPw(e.target.value)} autoComplete="new-password" minLength={8} required />
          </div>
          <button className="btn primary" disabled={savePw.isPending}>
            Ändern
          </button>
        </div>
        <p className="muted text-xs mt-2 mb-0">
          Nach dem Wechsel werden alle Sitzungen abgemeldet — auch diese.
        </p>
      </form>

      <div className="card">
        <div className="flex items-center gap-3 mb-2">
          <span className="text-sm font-medium">Sitzungen</span>
          <span className="spacer flex-1" />
          {others > 0 && (
            <button className="btn sm" onClick={() => revokeOthers.mutate()} disabled={revokeOthers.isPending}>
              Andere beenden
            </button>
          )}
        </div>
        {(sessions.data ?? []).map((s, i) => (
          <div key={i} className="flex items-center gap-3 text-xs py-1" style={{ borderTop: i > 0 ? "0.5px solid var(--border)" : "none" }}>
            <span>angemeldet {new Date(s.created_at).toLocaleString("de-DE")}</span>
            <span className="muted">läuft ab {new Date(s.expires_at).toLocaleString("de-DE")}</span>
            {s.current && <span className="badge st-done">diese Sitzung</span>}
          </div>
        ))}
        {sessions.data?.length === 0 && <p className="muted text-xs m-0">Keine aktiven Sitzungen.</p>}
      </div>
    </div>
  );
}

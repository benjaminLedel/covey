import { useState } from "react";
import { post } from "../api";

// Demo-Login nur lokal anbieten (localhost-Host — auch der Vite-Dev-Server läuft
// dort). Nutzt die Default-Bootstrap-Zugangsdaten (admin@covey.local /
// covey-admin, siehe cmd/covey bootstrap). In einem echten Deployment (anderer
// Host) erscheint der Button nicht.
const DEMO_EMAIL = "admin@covey.local";
const DEMO_PASSWORD = "covey-admin";
const isLocal = ["localhost", "127.0.0.1", "[::1]"].includes(
  window.location.hostname,
);

export default function Login({ onLogin }: { onLogin: () => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const login = async (mail: string, pass: string) => {
    setBusy(true);
    setError("");
    try {
      await post("/auth/login", { email: mail, password: pass });
      onLogin();
    } catch {
      setError("Anmeldung fehlgeschlagen — E-Mail oder Passwort falsch.");
    } finally {
      setBusy(false);
    }
  };

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    login(email, password);
  };

  return (
    <div className="fixed inset-0 flex items-center justify-center p-5" style={{ background: "var(--surface-0)" }}>
      <form
        onSubmit={submit}
        className="w-full"
        style={{
          maxWidth: 360,
          background: "var(--surface-2)",
          border: "0.5px solid var(--border)",
          borderRadius: 16,
          padding: "28px 26px",
        }}
      >
        <div className="flex items-center justify-center gap-2 text-xl font-medium mb-1">
          <span style={{ width: 7, height: 7, borderRadius: "50%", background: "var(--clay)" }} />
          Covey
        </div>
        <p className="text-center muted text-[13px] mb-5">Die Control Plane für Ihre Agenten-Belegschaft.</p>
        <label htmlFor="email">E-Mail</label>
        <input
          id="email"
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          autoComplete="username"
          className="mb-3"
          required
        />
        <label htmlFor="password">Passwort</label>
        <input
          id="password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="current-password"
          className="mb-4"
          required
        />
        {error && <p className="danger-text text-xs mb-3">{error}</p>}
        <button className="btn primary w-full justify-center" disabled={busy}>
          {busy ? "Anmelden…" : "Anmelden"}
        </button>
        {isLocal && (
          <>
            <div
              className="flex items-center gap-3 muted text-[11px] my-4"
              aria-hidden
            >
              <span style={{ flex: 1, height: 0.5, background: "var(--border)" }} />
              nur lokal
              <span style={{ flex: 1, height: 0.5, background: "var(--border)" }} />
            </div>
            <button
              type="button"
              className="btn w-full justify-center"
              disabled={busy}
              onClick={() => login(DEMO_EMAIL, DEMO_PASSWORD)}
            >
              Demo-Login (Admin)
            </button>
          </>
        )}
      </form>
    </div>
  );
}

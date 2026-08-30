import { useEffect, useState } from "react";
import { Link, useLocation } from "react-router";
import { useTranslation } from "react-i18next";
import { post } from "../api";
import { usePublicLang } from "./lang";
import { pathOf } from "./routes";
import { useSignupState } from "./signupState";

const DEMO_EMAIL = "admin@covey.local";
const DEMO_PASSWORD = "covey-admin";

/* Der Hostname steht beim Vorrendern nicht fest und darf die erste Darstellung
   im Browser nicht verändern — sonst weicht sie vom vorgerenderten HTML ab und
   React verwirft es. Deshalb erst nach der Hydration nachreichen. */
const localHost = () =>
  typeof window !== "undefined" &&
  ["localhost", "127.0.0.1", "[::1]"].includes(window.location.hostname);

/* Die Anmelde-Karte — auf der Home-Seite im Hero, außerdem unter /anmelden.
   Aus der früheren pages/Login.tsx herausgelöst. */
export default function LoginCard({ onLogin }: { onLogin: () => void }) {
  const { t } = useTranslation();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [isLocal, setIsLocal] = useState(false);
  const lang = usePublicLang();
  /* Der Verweis auf die Registrierung erscheint nur, wenn diese Installation
     sie überhaupt annimmt — sonst führte er zu einem „geschlossen" (FR-002). */
  const { state: signupState } = useSignupState();
  /* ?weiter= setzt nur die Weiterleitung aus der Oberfläche (App.tsx), wenn
     eine Sitzung abgelaufen ist. Ohne einen Satz dazu sieht es aus, als hätte
     die Anwendung einen zufällig hinausgeworfen. */
  const { search } = useLocation();
  const abgelaufen = new URLSearchParams(search).has("weiter");

  useEffect(() => setIsLocal(localHost()), []);

  const login = async (mail: string, pass: string) => {
    setBusy(true);
    setError("");
    try {
      await post("/auth/login", { email: mail, password: pass });
      onLogin();
    } catch {
      setError(t("login.error"));
    } finally {
      setBusy(false);
    }
  };

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    login(email, password);
  };

  return (
    <form onSubmit={submit} className="login-card login-rise" style={{ animationDelay: "0.24s" }}>
      <h2 className="login-card-title">{t("login.title")}</h2>
      {abgelaufen && !error && (
        <p className="login-note" role="status">
          {t("login.expired")}
        </p>
      )}
      <label htmlFor="email">{t("login.email")}</label>
      <input
        id="email"
        type="email"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        autoComplete="username"
        className="mb-3"
        required
      />
      <label htmlFor="password">{t("login.password")}</label>
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
        {busy ? t("login.submitting") : t("login.submit")}
      </button>
      {isLocal && (
        <>
          <div className="login-divider" aria-hidden>
            <span />
            {t("login.localOnly")}
            <span />
          </div>
          <button
            type="button"
            className="btn w-full justify-center"
            disabled={busy}
            onClick={() => login(DEMO_EMAIL, DEMO_PASSWORD)}
          >
            {t("login.demoLogin")}
          </button>
        </>
      )}
      {signupState.mode !== "off" && (
        <p className="login-alt">
          {t("public.signup.noAccount")}{" "}
          <Link to={pathOf("registrieren", lang)}>{t("public.signup.title")}</Link>
        </p>
      )}
    </form>
  );
}

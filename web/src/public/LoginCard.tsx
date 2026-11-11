import { useEffect, useState } from "react";
import { Link, useLocation } from "react-router";
import { useTranslation } from "react-i18next";
import { api, ApiError, post } from "../api";
import { usePublicLang } from "./lang";
import { pathOf } from "./routes";
import { useSignupState } from "./signupState";

const DEMO_EMAIL = "admin@covey.local";
const DEMO_PASSWORD = "covey-admin";

/* The hostname is not fixed while prerendering and must not change the first
   render in the browser — otherwise it deviates from the prerendered HTML and
   React discards it. That is why it is only filled in after hydration. */
const localHost = () =>
  typeof window !== "undefined" &&
  ["localhost", "127.0.0.1", "[::1]"].includes(window.location.hostname);

/* The login card — in the hero on the home page, and also under `/anmelden`.
   Lifted out of the earlier pages/Login.tsx. */
export default function LoginCard({ onLogin }: { onLogin: () => void }) {
  const { t } = useTranslation();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [isLocal, setIsLocal] = useState(false);
  const lang = usePublicLang();
  /* The link to registration only appears when this installation accepts it at
     all — otherwise it led to a closed signup (FR-002). */
  const { state: signupState } = useSignupState();
  /* `?weiter=` sets the redirect from the interface (App.tsx) only when a
     session expired. Without a line about it, it looks as if the application
     had thrown someone out at random. */
  const { search } = useLocation();
  const abgelaufen = new URLSearchParams(search).has("weiter");

  /* Single sign-on (#723): the address decides. Its domain may belong to an
     organisation that signs its people in through its own provider — then
     the card offers that, and if the organisation requires it, the password
     field goes. The callback comes back here with ?sso_error= when it fails. */
  const ssoError = new URLSearchParams(search).get("sso_error");
  const [sso, setSso] = useState<{ sso: boolean; required?: boolean; org_name?: string }>({ sso: false });

  useEffect(() => setIsLocal(localHost()), []);

  useEffect(() => {
    const mail = email.trim();
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(mail)) {
      setSso({ sso: false });
      return;
    }
    let stale = false;
    const timer = setTimeout(() => {
      api<{ sso: boolean; required?: boolean; org_name?: string }>(
        `/public/sso/discover?email=${encodeURIComponent(mail)}`,
      )
        .then((r) => !stale && setSso(r))
        .catch(() => !stale && setSso({ sso: false }));
    }, 300);
    return () => {
      stale = true;
      clearTimeout(timer);
    };
  }, [email]);

  const startSSO = () => {
    window.location.href = `/api/sso/start?email=${encodeURIComponent(email.trim())}`;
  };

  const login = async (mail: string, pass: string) => {
    setBusy(true);
    setError("");
    try {
      await post("/auth/login", { email: mail, password: pass });
      onLogin();
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setSso({ sso: true, required: true });
        setError(t("login.ssoRequired"));
      } else {
        setError(t("login.error"));
      }
    } finally {
      setBusy(false);
    }
  };

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (sso.required) startSSO();
    else login(email, password);
  };

  return (
    <form onSubmit={submit} className="login-card login-rise" style={{ animationDelay: "0.24s" }}>
      <h2 className="login-card-title">{t("login.title")}</h2>
      {abgelaufen && !error && (
        <p className="login-note" role="status">
          {t("login.expired")}
        </p>
      )}
      {ssoError && !error && (
        <p className="danger-text text-xs mb-3" role="alert">
          {t(`login.ssoError.${ssoError}`, t("login.ssoError.failed"))}
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
      {sso.sso && (
        <>
          <button type="button" className="btn primary w-full justify-center mb-3" onClick={startSSO}>
            {sso.org_name ? t("login.ssoWith", { org: sso.org_name }) : t("login.sso")}
          </button>
          {!sso.required && (
            <div className="login-divider" aria-hidden>
              <span />
              {t("login.orPassword")}
              <span />
            </div>
          )}
        </>
      )}
      {!sso.required && (
        <>
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
        </>
      )}
      {error && <p className="danger-text text-xs mb-3">{error}</p>}
      {!sso.required && (
        <button className={`btn ${sso.sso ? "" : "primary "}w-full justify-center`} disabled={busy}>
          {busy ? t("login.submitting") : t("login.submit")}
        </button>
      )}
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
      {/* The way back when the password is gone. It stands regardless of
          `signup.mode`: a closed installation still has accounts, and whoever
          forgets theirs is not a stranger who wants to be let in — they are
          already inside (#168). */}
      <p className="login-alt">
        <Link to="/reset">{t("public.reset.forgot")}</Link>
      </p>
      {signupState.mode !== "off" && (
        <p className="login-alt">
          {t("public.signup.noAccount")}{" "}
          <Link to={pathOf("registrieren", lang)}>{t("public.signup.title")}</Link>
        </p>
      )}
    </form>
  );
}

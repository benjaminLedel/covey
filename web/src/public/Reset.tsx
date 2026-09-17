import { useState } from "react";
import { Link, useLocation } from "react-router";
import { useTranslation } from "react-i18next";
import { ApiError } from "../api";
import { BirdMark } from "./chrome";
import { usePublicLang } from "./lang";
import { pathOf } from "./routes";
import { confirmPasswordReset, requestPasswordReset } from "./signupState";

/* Password reset (`/reset`, `/reset?token=…`) — one page with two states,
   because these are two steps of the same process: requesting the mail and,
   from inside the mail, setting a new password (#168).

   Without a token the request form stands here. Its answer is always the
   same: whether an account belongs to the address it never says — otherwise
   this endpoint would be a report on who works here. */

const MIN_PASSWORT = 8;

export default function Reset() {
  const { t } = useTranslation();
  const lang = usePublicLang();
  const { search } = useLocation();
  const token = new URLSearchParams(search).get("token") ?? "";

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");

  const rahmen = (inhalt: React.ReactNode) => (
    <div className="landing pub-signin">
      <div className="pub-signin-brand login-rise">
        <BirdMark size={52} />
        <h1 className="login-wordmark">covey</h1>
      </div>
      {inhalt}
    </div>
  );

  const zurAnmeldung = (
    <p className="login-alt">
      <Link to={pathOf("anmelden", lang)}>{t("public.signup.signIn")}</Link>
    </p>
  );

  const anfordern = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await requestPasswordReset(email.trim(), lang);
      setSent(true);
    } catch (err) {
      /* Only what says nothing about the address gets here: a missing mail
         delivery (503) or too many attempts (429). Both are states of the
         installation, not information about an account. */
      setError(err instanceof ApiError && err.message ? err.message : t("public.reset.error"));
    } finally {
      setBusy(false);
    }
  };

  const setzen = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await confirmPasswordReset(token, password);
      setDone(true);
    } catch (err) {
      setError(err instanceof ApiError && err.message ? err.message : t("public.reset.error"));
    } finally {
      setBusy(false);
    }
  };

  if (done) {
    return rahmen(
      <div className="login-card login-rise" style={{ animationDelay: "0.24s" }}>
        <h2 className="login-card-title">{t("public.reset.doneTitle")}</h2>
        <p className="landing-pitch">{t("public.reset.doneText")}</p>
        {zurAnmeldung}
      </div>,
    );
  }

  if (token) {
    return rahmen(
      <form onSubmit={setzen} className="login-card login-rise" style={{ animationDelay: "0.24s" }}>
        <h2 className="login-card-title">{t("public.reset.setTitle")}</h2>
        <p className="landing-pitch mb-4">{t("public.reset.setText")}</p>
        <label htmlFor="new-password">{t("public.reset.password")}</label>
        <input
          id="new-password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="new-password"
          minLength={MIN_PASSWORT}
          className="mb-4"
          required
        />
        {error && <p className="danger-text text-xs mb-3">{error}</p>}
        <button className="btn primary w-full justify-center" disabled={busy}>
          {busy ? t("public.reset.saving") : t("public.reset.save")}
        </button>
        {zurAnmeldung}
      </form>,
    );
  }

  if (sent) {
    return rahmen(
      <div className="login-card login-rise" style={{ animationDelay: "0.24s" }}>
        <h2 className="login-card-title">{t("public.reset.sentTitle")}</h2>
        <p className="landing-pitch">{t("public.reset.sentText")}</p>
        {zurAnmeldung}
      </div>,
    );
  }

  return rahmen(
    <form onSubmit={anfordern} className="login-card login-rise" style={{ animationDelay: "0.24s" }}>
      <h2 className="login-card-title">{t("public.reset.requestTitle")}</h2>
      <p className="landing-pitch mb-4">{t("public.reset.requestText")}</p>
      <label htmlFor="reset-email">{t("public.reset.email")}</label>
      <input
        id="reset-email"
        type="email"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        autoComplete="username"
        className="mb-4"
        required
      />
      {error && <p className="danger-text text-xs mb-3">{error}</p>}
      <button className="btn primary w-full justify-center" disabled={busy}>
        {busy ? t("public.reset.requesting") : t("public.reset.request")}
      </button>
      {zurAnmeldung}
    </form>,
  );
}

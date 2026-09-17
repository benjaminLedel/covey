import { useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { ApiError } from "../api";
import { BirdMark } from "./chrome";
import { usePublicLang } from "./lang";
import { pathOf } from "./routes";
import { signup, useSignupState } from "./signupState";

/* Sign up (/registrieren, /en/sign-up) — the way into the product for all
   who have no installation.

   The waitlist code is the first gate, so it stands at the top and not at
   the bottom: without it the form is pointless, and whoever has none should
   read that before thinking up a password (FR-002).

   An organisation is NOT chosen here. The account comes first, the
   membership after — joining or founding one yourself is decided after the
   e-mail is confirmed, in the signed-in state. */

const MIN_PASSWORT = 8;

export default function SignUp() {
  const { t } = useTranslation();
  const lang = usePublicLang();
  const { state, loading } = useSignupState();

  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  /* null = not submitted yet; otherwise: whether a confirmation is on its way. */
  const [fertig, setFertig] = useState<boolean | null>(null);

  const absenden = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await signup({
        code: code.trim(),
        email: email.trim(),
        display_name: name.trim(),
        password,
        lang,
      });
      setFertig(res.verification_sent);
    } catch (err) {
      /* The error message of the server is the more exact one: it tells the
         used-up code apart from the unknown one and the taken address from the
         invalid one. Only when none comes does this hold its own sentence. */
      setError(err instanceof ApiError && err.message ? err.message : t("public.signup.error"));
    } finally {
      setBusy(false);
    }
  };

  const rahmen = (inhalt: React.ReactNode) => (
    <div className="landing pub-signin">
      <div className="pub-signin-brand login-rise">
        <BirdMark size={52} />
        <h1 className="login-wordmark">covey</h1>
      </div>
      <p
        className="landing-tagline login-rise"
        style={{ animationDelay: "0.08s", textAlign: "center" }}
      >
        {t("public.signup.subtitle")}
      </p>
      {inhalt}
    </div>
  );

  /* While the answer is outstanding this holds nothing. A form that can
     vanish again right away is worse than one moment of quiet. */
  if (loading) return rahmen(null);

  if (state.mode === "off") {
    return rahmen(
      <div className="login-card login-rise" style={{ animationDelay: "0.24s" }}>
        <h2 className="login-card-title">{t("public.signup.closed.title")}</h2>
        <p className="landing-pitch">{t("public.signup.closed.text")}</p>
        <Link
          className="btn w-full justify-center mt-4"
          to={pathOf("anmelden", lang)}
        >
          {t("public.signup.signIn")}
        </Link>
      </div>,
    );
  }

  if (fertig !== null) {
    /* Two endings, because two different things happened. Whoever gets no
       mail must not be left waiting for one. */
    const zweig = fertig ? "done" : "created";
    return rahmen(
      <div className="login-card login-rise" style={{ animationDelay: "0.24s" }}>
        <h2 className="login-card-title">{t(`public.signup.${zweig}.title`)}</h2>
        <p className="landing-pitch">
          {t(`public.signup.${zweig}.text`, { email: email.trim() })}
        </p>
      </div>,
    );
  }

  const codeNoetig = state.mode === "waitlist";

  return rahmen(
    <form onSubmit={absenden} className="login-card login-rise" style={{ animationDelay: "0.24s" }}>
      <h2 className="login-card-title">{t("public.signup.title")}</h2>

      {codeNoetig && (
        <>
          <label htmlFor="code">{t("public.signup.code")}</label>
          <input
            id="code"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            autoComplete="off"
            autoCapitalize="characters"
            spellCheck={false}
            required
          />
          <p className="field-hint mb-3">{t("public.signup.codeHint")}</p>
        </>
      )}

      <label htmlFor="name">{t("public.signup.name")}</label>
      <input
        id="name"
        value={name}
        onChange={(e) => setName(e.target.value)}
        autoComplete="name"
        className="mb-3"
        required
      />

      <label htmlFor="email">{t("public.signup.email")}</label>
      <input
        id="email"
        type="email"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        autoComplete="username"
        className="mb-3"
        required
      />

      <label htmlFor="password">{t("public.signup.password")}</label>
      <input
        id="password"
        type="password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        autoComplete="new-password"
        minLength={MIN_PASSWORT}
        required
      />
      <p className="field-hint mb-4">{t("public.signup.passwordHint", { min: MIN_PASSWORT })}</p>

      {error && <p className="danger-text text-xs mb-3">{error}</p>}

      <button className="btn primary w-full justify-center" disabled={busy}>
        {busy ? t("public.signup.submitting") : t("public.signup.submit")}
      </button>

      <p className="login-alt">
        {t("public.signup.haveAccount")}{" "}
        <Link to={pathOf("anmelden", lang)}>{t("public.signup.signIn")}</Link>
      </p>
    </form>,
  );
}

import { useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { ApiError } from "../api";
import { BirdMark } from "./chrome";
import { usePublicLang } from "./lang";
import { pathOf } from "./routes";
import { signup, useSignupState } from "./signupState";

/* Registrieren (/registrieren, /en/sign-up) — der Weg ins Produkt für alle,
   die keine Installation haben.

   Der Wartelisten-Code ist das erste Tor und steht deshalb oben, nicht unten:
   ohne ihn ist das Formular sinnlos, und wer keinen hat, soll das lesen, bevor
   er ein Passwort ausdenkt (FR-002).

   Eine Organisation wird hier NICHT gewählt. Das Konto entsteht zuerst, die
   Zugehörigkeit danach — beitreten oder selbst gründen entscheidet sich nach
   der Bestätigung der E-Mail, im angemeldeten Zustand. */

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
  /* null = noch nicht abgeschickt; sonst: ob eine Bestätigung unterwegs ist. */
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
      /* Die Fehlermeldung des Servers ist die genauere: sie unterscheidet den
         verbrauchten Code vom unbekannten und die vergebene Adresse von der
         ungültigen. Nur wenn keine kommt, steht hier ein eigener Satz. */
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

  /* Solange die Antwort aussteht, steht hier nichts. Ein Formular, das gleich
     wieder verschwinden kann, ist schlimmer als ein Moment Ruhe. */
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
    /* Zwei Abschlüsse, weil zwei verschiedene Dinge passiert sind. Wer keine
       Mail bekommt, darf nicht auf eine warten. */
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

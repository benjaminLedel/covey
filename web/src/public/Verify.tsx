import { useEffect, useRef, useState } from "react";
import { Link, useLocation } from "react-router";
import { useTranslation } from "react-i18next";
import { BirdMark } from "./chrome";
import { usePublicLang } from "./lang";
import { pathOf } from "./routes";
import { resendVerification, verifyAddress } from "./signupState";

/* Die Adresse bestätigen (/verify?token=…) — das Ziel des Links aus der
   Registrierungsmail (#168).

   Der Pfad trägt keine Sprache, anders als /anmelden und /registrieren. Er
   steht in einer Mail, die Monate im Postfach liegen kann, und muss auch dann
   noch stimmen, wenn jemand die Sprache inzwischen gewechselt hat. Was hier
   übersetzt wird, ist der Text, nicht die Adresse.

   Bestätigt wird beim Laden, ohne Knopf: Wer den Link geöffnet hat, hat schon
   zugestimmt — ein „Jetzt bestätigen" wäre eine zweite Frage nach derselben
   Sache. */
export default function Verify({ onLogin }: { onLogin: () => void }) {
  const { t } = useTranslation();
  const lang = usePublicLang();
  const { search } = useLocation();
  const token = new URLSearchParams(search).get("token") ?? "";

  const [state, setState] = useState<"checking" | "ok" | "failed">("checking");
  const [email, setEmail] = useState("");
  const [resent, setResent] = useState(false);
  const [busy, setBusy] = useState(false);
  /* React 18 führt Effekte im Strict Mode zweimal aus. Der zweite Aufruf
     träfe auf einen verbrauchten Token und meldete „Link ungültig" — für
     einen Link, der gerade funktioniert hat. */
  const gestartet = useRef(false);

  useEffect(() => {
    if (gestartet.current) return;
    gestartet.current = true;
    if (!token) {
      setState("failed");
      return;
    }
    verifyAddress(token)
      .then(() => {
        setState("ok");
        /* Die Sitzung steht schon (der Server hat sie mit der Bestätigung
           gesetzt) — die Anwendung muss sie nur noch bemerken. */
        onLogin();
      })
      .catch(() => setState("failed"));
  }, [token, onLogin]);

  const rahmen = (inhalt: React.ReactNode) => (
    <div className="landing pub-signin">
      <div className="pub-signin-brand login-rise">
        <BirdMark size={52} />
        <h1 className="login-wordmark">covey</h1>
      </div>
      {inhalt}
    </div>
  );

  if (state === "checking") {
    return rahmen(
      <p className="landing-tagline login-rise" style={{ textAlign: "center" }}>
        {t("public.verify.checking")}
      </p>,
    );
  }

  if (state === "ok") {
    return rahmen(
      <div className="login-card login-rise" style={{ animationDelay: "0.24s" }}>
        <h2 className="login-card-title">{t("public.verify.okTitle")}</h2>
        <p className="landing-pitch">{t("public.verify.okText")}</p>
      </div>,
    );
  }

  const neuSchicken = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await resendVerification(email.trim(), lang);
    } catch {
      /* Auch der Fehlerfall führt hierher: die Antwort verrät ohnehin nicht,
         ob es diese Adresse gibt, und ein Unterschied in der Anzeige wäre
         genau die Auskunft, die der Endpunkt vermeidet. */
    } finally {
      setResent(true);
      setBusy(false);
    }
  };

  return rahmen(
    <div className="login-card login-rise" style={{ animationDelay: "0.24s" }}>
      <h2 className="login-card-title">{t("public.verify.failTitle")}</h2>
      <p className="landing-pitch mb-4">{t("public.verify.failText")}</p>
      {resent ? (
        <p className="login-note" role="status">{t("public.verify.resent")}</p>
      ) : (
        <form onSubmit={neuSchicken}>
          <label htmlFor="verify-email">{t("public.verify.resendEmail")}</label>
          <input
            id="verify-email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            autoComplete="username"
            className="mb-3"
            required
          />
          <button className="btn primary w-full justify-center" disabled={busy}>
            {busy ? t("public.verify.resending") : t("public.verify.resend")}
          </button>
        </form>
      )}
      <p className="login-alt">
        <Link to={pathOf("anmelden", lang)}>{t("public.signup.signIn")}</Link>
      </p>
    </div>,
  );
}

import { useEffect, useRef, useState } from "react";
import { Link, useLocation } from "react-router";
import { useTranslation } from "react-i18next";
import { BirdMark } from "./chrome";
import { usePublicLang } from "./lang";
import { pathOf } from "./routes";
import { resendVerification, verifyAddress } from "./signupState";

/* Confirm the address (/verify?token=…) — the target of the link from the
   sign-up mail (#168).

   The path carries no language, unlike /anmelden and /registrieren. It
   stands in a mail that can sit in the inbox for months, and must still be
   right when someone has switched the language in the meantime. What is
   translated here is the text, not the address.

   Confirmed on load, without a button: whoever opened the link has already
   agreed — a `confirm now` button would be a second question
   about the same thing. */
export default function Verify({ onLogin }: { onLogin: () => void }) {
  const { t } = useTranslation();
  const lang = usePublicLang();
  const { search } = useLocation();
  const token = new URLSearchParams(search).get("token") ?? "";

  const [state, setState] = useState<"checking" | "ok" | "failed">("checking");
  const [email, setEmail] = useState("");
  const [resent, setResent] = useState(false);
  const [busy, setBusy] = useState(false);
  /* React 18 runs effects twice in Strict Mode. The second call would hit
     a used-up token and report `Dieser Link gilt nicht mehr` —
     for a link that just worked. */
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
        /* The session already stands (the server set it with the
           confirmation) — the application only has to notice it. */
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
      /* The error case leads here too: the answer does not reveal whether this
         address exists in the first place, and a difference in the display
         would be exactly the report the endpoint avoids. */
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

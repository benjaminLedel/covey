import { useState } from "react";
import { useTranslation } from "react-i18next";
import { post } from "../api";

const DEMO_EMAIL = "admin@covey.local";
const DEMO_PASSWORD = "covey-admin";
const isLocal = ["localhost", "127.0.0.1", "[::1]"].includes(
  window.location.hostname,
);

/* Covey = ein Schwarm — drei Vögel in Flugformation als Wortmarke. */
function BirdMark({ size = 84 }: { size?: number }) {
  return (
    <span
      aria-hidden="true"
      style={{
        width: size,
        height: size,
        borderRadius: size * 0.32,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        background:
          "linear-gradient(140deg, #d98a6a 0%, var(--clay) 55%, #b25f41 100%)",
        boxShadow:
          "0 10px 28px rgba(178, 95, 65, 0.34), 0 1px 2px rgba(30, 28, 23, 0.2), inset 0 1px 0 rgba(255, 255, 255, 0.28)",
      }}
    >
      <svg
        viewBox="0 0 24 24"
        width={size * 0.62}
        height={size * 0.62}
        fill="none"
        stroke="#fff"
        strokeWidth={1.7}
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <path d="M7 15 Q9.75 11.8 12.5 15 Q15.25 11.8 18 15" />
        <path d="M3.5 10 Q5.5 7.7 7.5 10 Q9.5 7.7 11.5 10" />
        <path d="M13 8 Q14.5 6.3 16 8 Q17.5 6.3 19 8" />
      </svg>
    </span>
  );
}

/* Ein einzelner Vogel-Glyph für den Hintergrund-Schwarm. */
function Bird({
  top,
  left,
  size,
  delay,
}: {
  top: string;
  left: string;
  size: number;
  delay: string;
}) {
  return (
    <svg
      viewBox="0 0 18 6"
      width={size}
      height={size / 3}
      style={{ top, left, animationDelay: delay }}
    >
      <path d="M1 5 Q4.5 0.5 8 5 Q11.5 0.5 15 5" />
    </svg>
  );
}

export default function Login({ onLogin }: { onLogin: () => void }) {
  const { t } = useTranslation();
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
    <div className="login-bg">
      <div className="login-flock" aria-hidden="true">
        <Bird top="16%" left="12%" size={44} delay="0s" />
        <Bird top="26%" left="78%" size={62} delay="1.4s" />
        <Bird top="68%" left="18%" size={54} delay="0.7s" />
        <Bird top="74%" left="82%" size={38} delay="2.1s" />
        <Bird top="46%" left="90%" size={30} delay="1.1s" />
      </div>

      <div className="login-inner">
        <div className="login-head">
          <span className="login-mark">
            <BirdMark />
          </span>
          <h1 className="login-wordmark login-rise" style={{ animationDelay: "0.08s" }}>
            Covey
          </h1>
          <p className="login-subtitle login-rise" style={{ animationDelay: "0.16s" }}>
            {t("login.subtitle")}
          </p>
        </div>

        <form
          onSubmit={submit}
          className="login-card login-rise"
          style={{ animationDelay: "0.24s" }}
        >
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
        </form>
      </div>
    </div>
  );
}

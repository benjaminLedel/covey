import { useEffect, useState } from "react";
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
  drift,
}: {
  top: string;
  left: string;
  size: number;
  delay: string;
  drift?: boolean;
}) {
  return (
    <svg
      viewBox="0 0 18 6"
      width={size}
      height={size / 3}
      className={drift ? "drift" : undefined}
      style={{ top, left, animationDelay: delay }}
    >
      <path d="M1 5 Q4.5 0.5 8 5 Q11.5 0.5 15 5" />
    </svg>
  );
}

/* Icon-Pfade im Stil der Sidebar-Nav (App.tsx / Mockup). */
const featureIcons: Record<string, JSX.Element> = {
  sitemap: (
    <>
      <rect x="9" y="3" width="6" height="5" rx="1" />
      <rect x="3" y="16" width="6" height="5" rx="1" />
      <rect x="15" y="16" width="6" height="5" rx="1" />
      <path d="M12 8v4M6 16v-2h12v2M12 12v2" />
    </>
  ),
  box: (
    <>
      <path d="M12 3l8 4.5v9L12 21l-8-4.5v-9z" />
      <path d="M4 7.5l8 4.5l8-4.5M12 12v9" />
    </>
  ),
  key: (
    <>
      <circle cx="8" cy="8" r="3.5" />
      <path d="M10.5 10.5L20 20M17 17l2-2M14 14l2 2" />
    </>
  ),
  shield: <path d="M12 3l7 3v5c0 5-3 8-7 10c-4-2-7-5-7-10V6z" />,
  list: (
    <>
      <path d="M9 6h11M9 12h11M9 18h11" />
      <path d="M4 6h.01M4 12h.01M4 18h.01" />
    </>
  ),
  eye: (
    <>
      <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z" />
      <circle cx="12" cy="12" r="3" />
    </>
  ),
};

const FEATURES = ["sitemap", "box", "key", "shield", "list", "eye"] as const;

/* Scroll-Reveal: Elemente mit .reveal blenden ein, sobald sie sichtbar
   werden. Bei prefers-reduced-motion greift die CSS-Abschaltung. */
function useReveal() {
  useEffect(() => {
    const els = document.querySelectorAll<HTMLElement>(".reveal");
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) {
            (e.target as HTMLElement).classList.add("in");
            io.unobserve(e.target);
          }
        }
      },
      { threshold: 0.12 },
    );
    els.forEach((el) => io.observe(el));
    return () => io.disconnect();
  }, []);
}

function Imprint({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div
        className="modal sm"
        role="dialog"
        aria-modal="true"
        aria-label={t("landing.imprint")}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="modal-head">
          <h2>{t("landing.imprint")}</h2>
          <button className="icon-btn" onClick={onClose} aria-label={t("landing.close")}>
            <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
              <path d="M6 6l12 12M18 6L6 18" />
            </svg>
          </button>
        </div>
        <div className="modal-body imprint-body">
          <p className="imprint-law">{t("landing.imprintLaw")}</p>
          <p className="imprint-name">Benjamin Ledel</p>
          <p>{t("landing.imprintResp")}</p>
          <p className="imprint-note">{t("landing.imprintContact")}</p>
        </div>
      </div>
    </div>
  );
}

export default function Login({ onLogin }: { onLogin: () => void }) {
  const { t } = useTranslation();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [imprint, setImprint] = useState(false);
  useReveal();

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
        <Bird top="10%" left="12%" size={44} delay="0s" />
        <Bird top="18%" left="78%" size={62} delay="1.4s" />
        <Bird top="42%" left="6%" size={54} delay="0.7s" />
        <Bird top="52%" left="88%" size={38} delay="2.1s" />
        <Bird top="30%" left="92%" size={30} delay="1.1s" />
        <Bird top="8%" left="-6%" size={36} delay="0s" drift />
        <Bird top="22%" left="-6%" size={26} delay="9s" drift />
      </div>

      <div className="landing">
        <div className="landing-hero">
          <div className="landing-intro">
            <div className="landing-brand login-rise">
              <BirdMark size={64} />
              <h1 className="login-wordmark">Covey</h1>
            </div>
            <p className="landing-tagline login-rise" style={{ animationDelay: "0.08s" }}>
              {t("login.subtitle")}
            </p>
            <p className="landing-pitch login-rise" style={{ animationDelay: "0.16s" }}>
              {t("landing.pitch")}
            </p>
            <ul className="landing-points login-rise" style={{ animationDelay: "0.24s" }}>
              {[1, 2, 3].map((n) => (
                <li key={n}>
                  <svg viewBox="0 0 24 24" aria-hidden="true">
                    <path d="M5 12.5l4.5 4.5L19 7.5" />
                  </svg>
                  {t(`landing.point${n}`)}
                </li>
              ))}
            </ul>
          </div>

          <form
            onSubmit={submit}
            className="login-card login-rise"
            style={{ animationDelay: "0.24s" }}
          >
            <h2 className="login-card-title">{t("login.title")}</h2>
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

        {/* Schwarm-Bild als breites Band — die Leitmetapher wörtlich genommen. */}
        <figure className="landing-band reveal">
          <img src="/landing/murmuration.jpg" alt={t("landing.bandAlt")} loading="lazy" />
          <figcaption>{t("landing.bandCaption")}</figcaption>
        </figure>

        <section className="landing-features">
          <h2 className="reveal">{t("landing.featuresTitle")}</h2>
          <p className="landing-lead reveal">{t("landing.featuresLead")}</p>
          <div className="landing-grid">
            {FEATURES.map((icon, i) => (
              <div
                className="landing-feature reveal"
                style={{ transitionDelay: `${(i % 3) * 0.08}s` }}
                key={icon}
              >
                <span className="landing-feature-icon" aria-hidden="true">
                  <svg viewBox="0 0 24 24">{featureIcons[icon]}</svg>
                </span>
                <h3>{t(`landing.f${i + 1}t`)}</h3>
                <p>{t(`landing.f${i + 1}d`)}</p>
              </div>
            ))}
          </div>
        </section>

        {/* Ablauf in drei Schritten, daneben die Formation aus dem Schwarm. */}
        <section className="landing-how">
          <div className="landing-how-text">
            <h2 className="reveal">{t("landing.howTitle")}</h2>
            <ol>
              {[1, 2, 3].map((n) => (
                <li className="reveal" style={{ transitionDelay: `${(n - 1) * 0.1}s` }} key={n}>
                  <span className="landing-step-num" aria-hidden="true">{n}</span>
                  <div>
                    <h3>{t(`landing.s${n}t`)}</h3>
                    <p>{t(`landing.s${n}d`)}</p>
                  </div>
                </li>
              ))}
            </ol>
          </div>
          <div className="landing-how-img reveal">
            <img src="/landing/formation.jpg" alt={t("landing.howAlt")} loading="lazy" />
          </div>
        </section>

        <footer className="landing-foot">
          <span>{t("landing.foot")}</span>
          <nav>
            <button className="landing-foot-link" onClick={() => setImprint(true)}>
              {t("landing.imprint")}
            </button>
            <span className="landing-foot-sep" aria-hidden="true">·</span>
            <span className="landing-foot-credit">{t("landing.photoCredit")}</span>
          </nav>
        </footer>
      </div>

      {imprint && <Imprint onClose={() => setImprint(false)} />}
    </div>
  );
}

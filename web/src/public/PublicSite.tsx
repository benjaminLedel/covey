import { useEffect, useState } from "react";
import { Routes, Route, Navigate, useLocation, Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { PublicBackground, useReveal, BirdMark } from "./chrome";
import PublicNav from "./PublicNav";
import LoginCard from "./LoginCard";
import Home from "./Home";
import Funktion from "./Funktion";
import ProductCovey from "./ProductCovey";
import ProductCompanion from "./ProductCompanion";
import Docs from "./Docs";

/* Impressum & Datenschutz — Anschrift/E-Mail sind Platzhalter, die der
   Betreiber der jeweiligen Installation ausfüllen muss (§ 5 DDG). */
function LegalModal({
  kind,
  onClose,
}: {
  kind: "imprint" | "privacy";
  onClose: () => void;
}) {
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
        aria-label={t(`landing.${kind}`)}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="modal-head">
          <h2>{t(`landing.${kind}`)}</h2>
          <button className="icon-btn" onClick={onClose} aria-label={t("landing.close")}>
            <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
              <path d="M6 6l12 12M18 6L6 18" />
            </svg>
          </button>
        </div>
        {kind === "imprint" ? (
          <div className="modal-body imprint-body">
            <p className="imprint-law">{t("landing.imprintLaw")}</p>
            <p className="imprint-name">Benjamin Ledel</p>
            <p className="imprint-addr">
              {t("landing.imprintAddr1")}
              <br />
              {t("landing.imprintAddr2")}
            </p>
            <p>{t("landing.imprintMail")}</p>
            <p>{t("landing.imprintResp")}</p>
          </div>
        ) : (
          <div className="modal-body imprint-body">
            {[1, 2, 3, 4, 5].map((n) => (
              <p key={n}>{t(`landing.priv${n}`)}</p>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

/* Eigenständige Anmelde-Seite (/anmelden): zentrierte Karte. */
function AnmeldenPage({ onLogin }: { onLogin: () => void }) {
  const { t } = useTranslation();
  return (
    <div className="landing pub-signin">
      <div className="pub-signin-brand login-rise">
        <BirdMark size={52} />
        <h1 className="login-wordmark">Covey</h1>
      </div>
      <p className="landing-tagline login-rise" style={{ animationDelay: "0.08s", textAlign: "center" }}>
        {t("login.subtitle")}
      </p>
      <LoginCard onLogin={onLogin} />
    </div>
  );
}

/* PublicSite — die gesamte öffentliche Website im unangemeldeten Zustand:
   fixer Hintergrund, Kopfzeile, Seiten-Routing, Fußzeile. */
export default function PublicSite({ onLogin }: { onLogin: () => void }) {
  const { t } = useTranslation();
  const loc = useLocation();
  const [legal, setLegal] = useState<"imprint" | "privacy" | null>(null);
  useReveal(loc.pathname);

  // Bei jedem Seitenwechsel nach oben scrollen.
  useEffect(() => {
    window.scrollTo(0, 0);
  }, [loc.pathname]);

  return (
    <div className="login-bg pub-shell">
      <PublicBackground />
      <PublicNav />

      <main className="pub-main">
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/funktion" element={<Funktion />} />
          <Route path="/produkt/covey" element={<ProductCovey />} />
          <Route path="/produkt/companion" element={<ProductCompanion />} />
          <Route path="/docs" element={<Docs />} />
          <Route path="/docs/:slug" element={<Docs />} />
          <Route path="/anmelden" element={<AnmeldenPage onLogin={onLogin} />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>

      <footer className="landing-foot pub-foot">
        <span>{t("landing.foot")}</span>
        <nav>
          <Link className="landing-foot-link" to="/funktion">{t("public.nav.function")}</Link>
          <span className="landing-foot-sep" aria-hidden="true">·</span>
          <button className="landing-foot-link" onClick={() => setLegal("imprint")}>
            {t("landing.imprint")}
          </button>
          <span className="landing-foot-sep" aria-hidden="true">·</span>
          <button className="landing-foot-link" onClick={() => setLegal("privacy")}>
            {t("landing.privacy")}
          </button>
          <span className="landing-foot-sep" aria-hidden="true">·</span>
          <span className="landing-foot-credit">{t("landing.photoCredit")}</span>
        </nav>
      </footer>

      {legal && <LegalModal kind={legal} onClose={() => setLegal(null)} />}
    </div>
  );
}

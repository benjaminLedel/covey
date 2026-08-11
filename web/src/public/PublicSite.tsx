import { useEffect, useState, type ReactNode } from "react";
import { Routes, Route, useLocation, Link } from "react-router";
import { useTranslation } from "react-i18next";
import i18n from "../i18n";
import { PublicBackground, useReveal, BirdMark } from "./chrome";
import PublicNav from "./PublicNav";
import LoginCard from "./LoginCard";
import SignUp from "./SignUp";
import Home from "./Home";
import Funktion from "./Funktion";
import Integrationen from "./Integrationen";
import ProductCovey from "./ProductCovey";
import ProductCompanion from "./ProductCompanion";
import Docs from "./Docs";
import Head from "./Head";
import { usePublicLang, useSprachwahlFolgen } from "./lang";
import { LANGS, PAGE_ROUTES, pathOf } from "./seo";

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

/* Seite nicht gefunden. Früher leitete jeder unbekannte Pfad still auf die
   Startseite um — für Besucher unauffällig, für Suchmaschinen ein „Soft 404":
   beliebig viele Adressen, die mit Status 200 denselben Inhalt zeigen. Jetzt
   sagt die Seite, was los ist, und der Server schickt dazu die 404
   (internal/httpapi/spa.go). */
function NotFound() {
  const { t } = useTranslation();
  const lang = usePublicLang();
  return (
    <div className="landing pub-notfound">
      <h1>{t("public.notFound.title")}</h1>
      <p className="landing-pitch">{t("public.notFound.text")}</p>
      <Link className="btn primary" to={pathOf("home", lang)}>
        {t("public.notFound.home")}
      </Link>
    </div>
  );
}

/* PublicSite — die gesamte öffentliche Website im unangemeldeten Zustand:
   fixer Hintergrund, Kopfzeile, Seiten-Routing, Fußzeile. */
export default function PublicSite({ onLogin }: { onLogin: () => void }) {
  const { t } = useTranslation();
  const loc = useLocation();
  const lang = usePublicLang();
  const [legal, setLegal] = useState<"imprint" | "privacy" | null>(null);
  useReveal(loc.pathname);
  useSprachwahlFolgen();

  // Bei jedem Seitenwechsel nach oben scrollen.
  useEffect(() => {
    window.scrollTo(0, 0);
  }, [loc.pathname]);

  // Die Sprache folgt der URL (siehe lang.ts).
  useEffect(() => {
    if (i18n.language !== lang) void i18n.changeLanguage(lang);
  }, [lang]);

  /* Die Seiten sind je Sprache eigene Routen — die englischen Slugs sind
     übersetzt, nicht bloß präfigiert. Die Zuordnung Kennung → Element steht
     hier, die Pfade stehen in seo.ts. */
  const elements: Record<string, ReactNode> = {
    home: <Home />,
    funktion: <Funktion />,
    integrationen: <Integrationen />,
    "produkt-covey": <ProductCovey />,
    "produkt-companion": <ProductCompanion />,
    docs: <Docs />,
    anmelden: <AnmeldenPage onLogin={onLogin} />,
    registrieren: <SignUp />,
  };

  return (
    <div className="login-bg pub-shell">
      <Head />
      <PublicBackground />
      <PublicNav />

      <main className="pub-main">
        <Routes>
          {PAGE_ROUTES.flatMap((route) =>
            LANGS.map((l) => (
              <Route
                key={`${route.id}-${l}`}
                path={route.path[l]}
                element={elements[route.id]}
              />
            )),
          )}
          {/* Die einzelnen Docs-Seiten laufen über einen Parameter; ihre
              konkreten Pfade stehen trotzdem in seo.ts — für Sitemap,
              Vorrendern und den Kopf. */}
          {LANGS.map((l) => (
            <Route
              key={`docs-slug-${l}`}
              path={`${pathOf("docs", l)}/:slug`}
              element={<Docs />}
            />
          ))}
          <Route path="*" element={<NotFound />} />
        </Routes>
      </main>

      <footer className="landing-foot pub-foot">
        <span>{t("landing.foot")}</span>
        <nav>
          <Link className="landing-foot-link" to={pathOf("funktion", lang)}>
            {t("public.nav.function")}
          </Link>
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

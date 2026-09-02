/* Was ein Abgemeldeter von dieser Anwendung sieht: die Anmeldung und, wenn die
   Installation sie offen hat, die Registrierung. Sonst nichts.

   Bis #130 stand hier PublicSite — die ganze öffentliche Website mit Routing
   über acht Seiten, Kopfdaten, Vorrendern und einer Fußzeile. Sie ist mit #129
   in ein eigenes Repository und auf einen eigenen Host gezogen. Damit fällt
   auch die Sonderregel weg, die „/" für zwei Dinge zugleich hielt: Auf der
   Adresse der Anwendung ist „/" die Anmeldung, und sonst gar nichts. */

import { useEffect } from "react";
import { Navigate, Route, Routes, useLocation, useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import i18n, { initialLang, ladeSprache } from "../i18n";
import { PublicBackground, BirdMark } from "./chrome";
import GitHubLink from "../components/GitHubLink";
import LangPicker from "../components/LangPicker";
import LoginCard from "./LoginCard";
import Reset from "./Reset";
import SignUp from "./SignUp";
import Verify from "./Verify";
import { usePublicLang } from "./lang";
import { useSignupState } from "./signupState";
import { LANGS, PUBLIC_ROUTES, matchRoute, pathOf, type Lang } from "./routes";

/* Die Anmeldeseite: Wortmarke, ein Satz, die Karte. */
function AnmeldenPage({ onLogin }: { onLogin: () => void }) {
  const { t } = useTranslation();
  return (
    <div className="landing pub-signin">
      <div className="pub-signin-brand login-rise">
        <BirdMark size={52} />
        <h1 className="login-wordmark">covey</h1>
      </div>
      <p
        className="landing-tagline login-rise"
        style={{ animationDelay: "0.08s", textAlign: "center" }}
      >
        {t("login.subtitle")}
      </p>
      <LoginCard onLogin={onLogin} />
    </div>
  );
}

/* Der Titel im Reiter. Head.tsx hat ihn früher mitsamt Canonical, hreflang und
   strukturierten Daten gesetzt — davon bleibt hier nur der Titel: Was nicht
   indexiert wird (robots.txt sperrt diese Adresse), braucht keine Kopfdaten,
   aber ein offener Reiter soll trotzdem sagen, was er zeigt. */
function useTitel(pathname: string) {
  useEffect(() => {
    const treffer = matchRoute(pathname);
    if (treffer) document.title = treffer.route.title[treffer.lang];
  }, [pathname]);
}

export default function SignedOut({ onLogin }: { onLogin: () => void }) {
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const lang = usePublicLang();
  /* Dieselbe Abfrage, die die Anmelde-Karte ohnehin stellt (TanStack cacht
     sie nicht, aber der Hook hält sie pro Seite) — hier für die Adresse des
     Quelltexts. */
  const { state: installation } = useSignupState();
  useTitel(pathname);

  // Die Sprache folgt der Adresse (siehe lang.ts).
  useEffect(() => {
    if (i18n.language !== lang) void ladeSprache(lang);
  }, [lang]);

  const elemente: Record<string, React.ReactNode> = {
    anmelden: <AnmeldenPage onLogin={onLogin} />,
    registrieren: <SignUp />,
  };

  /* Alles andere gehört zur Anmeldung. Die Sprache dafür kommt aus der
     gespeicherten Wahl (oder der des Browsers), nicht aus dem Pfad — eine
     App-Adresse trägt keine. */
  const ziel = pathOf("anmelden", initialLang("/"));

  /* Die Sprachwahl wechselt hier die Adresse, nicht nur den Katalog: Vor der
     Anmeldung ist die Sprache Teil der URL (routes.ts), und wer /fr/connexion
     im Reiter stehen hat, soll sie teilen können. Der Effekt oben lädt den
     Katalog dann von selbst nach — eine Stelle, an der die Sprache gesetzt
     wird, nicht zwei. */
  const wechsleSprache = (neu: Lang) => {
    const treffer = matchRoute(pathname);
    navigate(pathOf(treffer?.route.id ?? "anmelden", neu));
  };

  return (
    <div className="login-bg pub-shell">
      <PublicBackground />
      <div className="pub-top">
        <GitHubLink url={installation.source} />
        <LangPicker onSelect={wechsleSprache} />
      </div>
      <main className="pub-main">
        <Routes>
          {PUBLIC_ROUTES.flatMap((route) =>
            LANGS.map((l: Lang) => (
              <Route key={`${route.id}-${l}`} path={route.path[l]} element={elemente[route.id]} />
            )),
          )}
          {/* Ohne Sprache im Pfad, weil sie aus einer Mail kommen (routes.ts). */}
          <Route path="/verify" element={<Verify onLogin={onLogin} />} />
          <Route path="/reset" element={<Reset />} />
          <Route path="*" element={<Navigate to={ziel} replace />} />
        </Routes>
      </main>
    </div>
  );
}

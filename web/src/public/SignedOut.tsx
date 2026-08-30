/* Was ein Abgemeldeter von dieser Anwendung sieht: die Anmeldung und, wenn die
   Installation sie offen hat, die Registrierung. Sonst nichts.

   Bis #130 stand hier PublicSite — die ganze öffentliche Website mit Routing
   über acht Seiten, Kopfdaten, Vorrendern und einer Fußzeile. Sie ist mit #129
   in ein eigenes Repository und auf einen eigenen Host gezogen. Damit fällt
   auch die Sonderregel weg, die „/" für zwei Dinge zugleich hielt: Auf der
   Adresse der Anwendung ist „/" die Anmeldung, und sonst gar nichts. */

import { useEffect } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router";
import { useTranslation } from "react-i18next";
import i18n, { gespeicherteSprache, ladeSprache } from "../i18n";
import { PublicBackground, BirdMark } from "./chrome";
import LoginCard from "./LoginCard";
import SignUp from "./SignUp";
import { usePublicLang } from "./lang";
import { LANGS, PUBLIC_ROUTES, matchRoute, pathOf, type Lang } from "./routes";

/* Die Anmeldeseite: Wortmarke, ein Satz, die Karte. */
function AnmeldenPage({ onLogin }: { onLogin: () => void }) {
  const { t } = useTranslation();
  return (
    <div className="landing pub-signin">
      <div className="pub-signin-brand login-rise">
        <BirdMark size={52} />
        <h1 className="login-wordmark">Covey</h1>
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
  const lang = usePublicLang();
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
     gespeicherten Wahl, nicht aus dem Pfad — App-Adressen tragen keine. */
  const ziel = pathOf("anmelden", gespeicherteSprache() === "de" ? "de" : "en");

  return (
    <div className="login-bg pub-shell">
      <PublicBackground />
      <main className="pub-main">
        <Routes>
          {PUBLIC_ROUTES.flatMap((route) =>
            LANGS.map((l: Lang) => (
              <Route key={`${route.id}-${l}`} path={route.path[l]} element={elemente[route.id]} />
            )),
          )}
          <Route path="*" element={<Navigate to={ziel} replace />} />
        </Routes>
      </main>
    </div>
  );
}

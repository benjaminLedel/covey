/* What a signed-out visitor sees of this application: the sign-in and, when the
   installation has it open, the sign-up. Nothing else.

   Up to #130 this was PublicSite — the whole public website with routing over
   eight pages, head data, prerendering and a footer. With #129 it moved into
   its own repository and onto its own host. That also drops the special rule
   that held `/` for two things at once: at the application address `/` is
   the sign-in, and otherwise nothing. */

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

/* The sign-in page: wordmark, one line, the card. */
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

/* The title in the tab. Head.tsx used to set it together with canonical, hreflang
   and structured data — of that only the title stays here: what is not
   indexed (robots.txt blocks this address) needs no head data, but an open
   tab should still say what it shows. */
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
  /* The same query the sign-in card makes anyway (TanStack does not cache
     it, but the hook keeps it per page) — here for the address of the
     source code. */
  const { state: installation } = useSignupState();
  useTitel(pathname);

  // The language follows the address (see lang.ts).
  useEffect(() => {
    if (i18n.language !== lang) void ladeSprache(lang);
  }, [lang]);

  const elemente: Record<string, React.ReactNode> = {
    anmelden: <AnmeldenPage onLogin={onLogin} />,
    registrieren: <SignUp />,
  };

  /* Everything else belongs to the sign-in. Its language comes from the
     stored choice (or the browser's), not from the path — an app address
     carries none. */
  const ziel = pathOf("anmelden", initialLang("/"));

  /* The language picker changes the address here, not just the catalogue: before
     the sign-in the language is part of the URL (routes.ts), and whoever has
     /fr/connexion in the tab should be able to share it. The effect above
     fetches the catalogue by itself — one place where the language is set,
     not two. */
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
          {/* No language in the path — they come from a mail (routes.ts). */}
          <Route path="/verify" element={<Verify onLogin={onLogin} />} />
          <Route path="/reset" element={<Reset />} />
          <Route path="*" element={<Navigate to={ziel} replace />} />
        </Routes>
      </main>
    </div>
  );
}

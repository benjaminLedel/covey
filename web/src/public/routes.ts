/* The two addresses this application serves without a sign-in, and the
   prefixes of the signed-in UI.

   Until #130 this place held the map of a whole website: titles, description,
   canonical, hreflang, the prerender list and the template for sitemap.xml.
   The website has lived in its own repository, on its own host, since #129;
   the binary carries only what still belongs to the application.

   The English slugs stay translated rather than prefixed (/en/sign-in, not
   /en/anmelden). They stand like that in the documentation, in bookmarks and
   in the redirects of the proxy — unifying them would break old addresses
   without gaining anything. The eight languages that came later follow the
   same rule: prefix plus a slug in their own language. Where the script is
   not Latin (ja, zh) a Latin slug stands — an address in kana would be a
   chain of percent signs in the browser, with that neither shareable
   nor readable.

   German carries no prefix: /anmelden is the oldest address of this
   application and the one that everything from before the languages points at.

   Titles stand here and not in the locales: they hang on the route, and the
   sign-in area sets them before i18n is loaded. */

import { LANGS, type Lang } from "../langs";

export type { Lang };
export { LANGS };

export type Localized = Record<Lang, string>;

export type PublicRoute = {
  /** Stable identifier; the sign-in area hangs its element mapping off it. */
  id: "anmelden" | "registrieren";
  /** Path per language. */
  path: Localized;
  /** What stands in the browser tab. */
  title: Localized;
};

export const PUBLIC_ROUTES: PublicRoute[] = [
  {
    id: "anmelden",
    path: {
      de: "/anmelden",
      en: "/en/sign-in",
      es: "/es/iniciar-sesion",
      fr: "/fr/connexion",
      it: "/it/accedi",
      nl: "/nl/inloggen",
      pl: "/pl/logowanie",
      pt: "/pt/entrar",
      ja: "/ja/login",
      zh: "/zh/login",
    },
    title: {
      de: "Anmelden — covey",
      en: "Sign in — covey",
      es: "Iniciar sesión — covey",
      fr: "Connexion — covey",
      it: "Accedi — covey",
      nl: "Inloggen — covey",
      pl: "Logowanie — covey",
      pt: "Entrar — covey",
      ja: "ログイン — covey",
      zh: "登录 — covey",
    },
  },
  {
    id: "registrieren",
    path: {
      de: "/registrieren",
      en: "/en/sign-up",
      es: "/es/crear-cuenta",
      fr: "/fr/inscription",
      it: "/it/registrati",
      nl: "/nl/registreren",
      pl: "/pl/rejestracja",
      pt: "/pt/criar-conta",
      ja: "/ja/sign-up",
      zh: "/zh/sign-up",
    },
    title: {
      de: "Registrieren — covey",
      en: "Sign up — covey",
      es: "Crear cuenta — covey",
      fr: "Inscription — covey",
      it: "Registrati — covey",
      nl: "Registreren — covey",
      pl: "Rejestracja — covey",
      pt: "Criar conta — covey",
      ja: "新規登録 — covey",
      zh: "注册 — covey",
    },
  },
];

/* The two addresses that are clicked out of a mail (#168).

   They deliberately carry NO language: they sit in a mail that can lie in the
   mailbox for months, and a link has to be right even once someone has
   switched the language in the meantime. The text on the page is translated,
   not their address.

   The Go handler needs them in the same list as the other public paths
   (vite.config.ts writes app-routes.json) — otherwise a call of the
   confirmation link answers with 404 instead of with the UI. */
export const MAIL_LINK_PATHS = ["/verify", "/reset"];

/* Addresses the covey app claims on app.covey.work (#333,
   internal/httpapi/applinks.go). /pair is the pairing link in the QR code: on
   a phone with the app it never reaches the browser; everywhere else it lands
   on a page that hands it on with covey://, signed in or not. */
export const APP_LINK_PATHS = ["/pair"];

/** Path of a route in the wanted language; unknown → the sign-in page. */
export function pathOf(id: string, lang: Lang): string {
  const route = PUBLIC_ROUTES.find((r) => r.id === id);
  return route ? route.path[lang] : PUBLIC_ROUTES[0].path[lang];
}

/** Which route an address is — for the title in the browser tab. */
export function matchRoute(pathname: string): { route: PublicRoute; lang: Lang } | null {
  const clean = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
  for (const route of PUBLIC_ROUTES) {
    for (const lang of LANGS) {
      if (route.path[lang] === clean) return { route, lang };
    }
  }
  return null;
}

/* The path prefixes of the signed-in UI. The Go handler
   (internal/httpapi/spa.go) needs them to tell apart two things that look the
   same: an app route that has to fall through to the SPA, and a typo that
   deserves an honest 404. Matching the routes in
   App.tsx — App.test.tsx holds the two together. */
export const APP_ROUTE_PREFIXES = [
  "/administration",
  "/agents",
  "/approvals",
  "/audit",
  "/team",
  "/costs",
  "/egress",
  "/guardrails",
  "/improvements",
  "/inbox",
  "/org",
  "/orgs",
  "/people",
  "/platform",
  "/profile",
  "/requests",
  "/diagnostics",
  "/runners",
  "/runtimes",
  "/secrets",
  "/setup",
  "/skills",
  "/targets",
  "/templates",
  "/users",
  "/voices",
  "/workplaces",
  "/infrastructure",
];

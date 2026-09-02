/* Die zwei Adressen, die diese Anwendung ohne Anmeldung kennt, und die
   Präfixe der angemeldeten Oberfläche.

   Bis #130 stand an dieser Stelle die Landkarte einer ganzen Website: Titel,
   Description, Canonical, hreflang, die Liste fürs Vorrendern und die Vorlage
   für sitemap.xml. Die Website liegt seit #129 in einem eigenen Repository und
   auf einem eigenen Host; das Binary trägt nur noch, was zur Anwendung gehört.

   Die englischen Slugs bleiben übersetzt statt präfigiert (/en/sign-in, nicht
   /en/anmelden). Sie stehen so in der Dokumentation, in Lesezeichen und in den
   Weiterleitungen des Proxys — eine Vereinheitlichung würde alte Adressen
   brechen, ohne etwas zu gewinnen. Die acht Sprachen, die später dazukamen,
   folgen derselben Regel: Präfix plus ein Slug in ihrer eigenen Sprache. Wo
   die Schrift nicht lateinisch ist (ja, zh), steht ein lateinischer Slug —
   eine Adresse in Kana wäre im Browser eine Kette von Prozentzeichen, und
   damit weder teilbar noch lesbar.

   Deutsch trägt kein Präfix: /anmelden ist die älteste Adresse dieser
   Anwendung und die, auf die alles zeigt, was vor den Sprachen da war.

   Titel stehen hier und nicht in den locales: Sie hängen an der Route, und der
   Anmeldebereich setzt sie, bevor i18n geladen ist. */

import { LANGS, type Lang } from "../langs";

export type { Lang };
export { LANGS };

export type Localized = Record<Lang, string>;

export type PublicRoute = {
  /** Stabile Kennung; der Anmeldebereich hängt daran seine Element-Zuordnung. */
  id: "anmelden" | "registrieren";
  /** Pfad je Sprache. */
  path: Localized;
  /** Was im Reiter steht. */
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

/* Die zwei Adressen, die aus einer Mail heraus angesteuert werden (#168).

   Sie tragen bewusst KEINE Sprache: sie stehen in einer Mail, die Monate im
   Postfach liegen kann, und ein Link muss auch dann noch stimmen, wenn jemand
   die Sprache inzwischen gewechselt hat. Übersetzt wird der Text auf der
   Seite, nicht ihre Adresse.

   Der Go-Handler braucht sie in derselben Liste wie die übrigen offenen Pfade
   (vite.config.ts schreibt app-routes.json) — sonst antwortet ein Aufruf des
   Bestätigungslinks mit 404 statt mit der Oberfläche. */
export const MAIL_LINK_PATHS = ["/verify", "/reset"];

/** Der Pfad einer Route in der gewünschten Sprache; unbekannt → die Anmeldung. */
export function pathOf(id: string, lang: Lang): string {
  const route = PUBLIC_ROUTES.find((r) => r.id === id);
  return route ? route.path[lang] : PUBLIC_ROUTES[0].path[lang];
}

/** Welche Route eine Adresse ist — für den Titel im Reiter. */
export function matchRoute(pathname: string): { route: PublicRoute; lang: Lang } | null {
  const clean = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
  for (const route of PUBLIC_ROUTES) {
    for (const lang of LANGS) {
      if (route.path[lang] === clean) return { route, lang };
    }
  }
  return null;
}

/* Die Pfad-Präfixe der angemeldeten Oberfläche. Der Go-Handler
   (internal/httpapi/spa.go) braucht sie, um zwei Dinge auseinanderzuhalten,
   die gleich aussehen: eine App-Route, die auf die SPA fallen muss, und ein
   Tippfehler, der eine ehrliche 404 verdient. Deckungsgleich mit den Routen in
   App.tsx — App.test.tsx hält beides zusammen. */
export const APP_ROUTE_PREFIXES = [
  "/administration",
  "/agents",
  "/approvals",
  "/audit",
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
  "/workplaces",
  "/infrastructure",
];

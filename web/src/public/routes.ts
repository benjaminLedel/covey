/* Die zwei Adressen, die diese Anwendung ohne Anmeldung kennt, und die
   Präfixe der angemeldeten Oberfläche.

   Bis #130 stand an dieser Stelle die Landkarte einer ganzen Website: Titel,
   Description, Canonical, hreflang, die Liste fürs Vorrendern und die Vorlage
   für sitemap.xml. Die Website liegt seit #129 in einem eigenen Repository und
   auf einem eigenen Host; das Binary trägt nur noch, was zur Anwendung gehört.

   Die englischen Slugs bleiben übersetzt statt präfigiert (/en/sign-in, nicht
   /en/anmelden). Sie stehen so in der Dokumentation, in Lesezeichen und in den
   Weiterleitungen des Proxys — eine Vereinheitlichung würde alte Adressen
   brechen, ohne etwas zu gewinnen.

   Titel stehen hier und nicht in den locales: Sie hängen an der Route, und der
   Anmeldebereich setzt sie, bevor i18n geladen ist. */

export type Lang = "de" | "en";
export type Localized = Record<Lang, string>;

export const LANGS: Lang[] = ["de", "en"];

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
    path: { de: "/anmelden", en: "/en/sign-in" },
    title: { de: "Anmelden — Covey", en: "Sign in — Covey" },
  },
  {
    id: "registrieren",
    path: { de: "/registrieren", en: "/en/sign-up" },
    title: { de: "Registrieren — Covey", en: "Sign up — Covey" },
  },
];

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

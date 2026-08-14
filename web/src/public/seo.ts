/* Die Landkarte der öffentlichen Website — eine Quelle für vier Verbraucher:

   1. PublicSite.tsx baut daraus sein Routing (beide Sprachen),
   2. Head.tsx setzt daraus Titel, Description, Canonical und hreflang,
   3. prerender.mjs rendert daraus jede Seite als echte HTML-Datei,
   4. der Go-Server (internal/httpapi/seo.go) macht daraus sitemap.xml.

   Warum hier und nicht in den locales: Titel und Description sind kein
   Oberflächen-Text, sondern das, was in der Suche steht. Sie gehören zur
   Route, nicht zur Übersetzung, und der Build muss sie ohne i18n lesen können.

   Die englische Fassung liegt unter eigenen URLs (/en/…). Ohne das gäbe es sie
   für Suchmaschinen nicht: eine Sprache, die nur im localStorage umschaltet,
   hat keine Adresse, unter der sie indexiert werden könnte. */

import { DOC_SECTIONS, type Faq } from "./docs/docsContent";

export type Lang = "de" | "en";
export type Localized = Record<Lang, string>;

export const LANGS: Lang[] = ["de", "en"];

export type PublicRoute = {
  /** Stabile Kennung; PublicSite hängt daran seine Element-Zuordnung. */
  id: string;
  /** Pfad je Sprache. Die englischen Slugs sind übersetzt, nicht präfigiert. */
  path: Localized;
  title: Localized;
  description: Localized;
  /** false = aus Sitemap und Index heraushalten (Anmeldeseite). */
  indexable: boolean;
  /** Gewicht in der Sitemap. */
  priority: number;
  /* Kanonische Adresse, wenn sie nicht die eigene ist.
     Genau ein Fall bisher: /docs zeigt denselben Inhalt wie die erste
     Docs-Seite (Docs.tsx fällt ohne Slug auf FIRST_DOC zurück). Zwei
     Adressen mit demselben Text, beide mit Canonical auf sich selbst, sind
     für eine Suchmaschine zwei konkurrierende Seiten — und keine von beiden
     gewinnt. */
  canonical?: Localized;
  /** Die Fragen der Seite, für die FAQ-Auszeichnung (Head.tsx). */
  faq?: Record<Lang, Faq[]>;
};

/* Die festen Seiten. Reihenfolge = Reihenfolge in der Sitemap. */
const PAGES: PublicRoute[] = [
  {
    id: "home",
    path: { de: "/", en: "/en" },
    title: {
      de: "Covey — die IT- und HR-Abteilung für KI-Agenten",
      en: "Covey — the IT and HR department for AI agents",
    },
    description: {
      de: "Covey führt KI-Agenten wie Mitarbeiter: eigene Identität, isolierte Sandbox, gebrokerte Zugänge, Backlog und ein Platz im Org-Chart — zentral governt.",
      en: "Covey runs AI agents like employees: their own identity, an isolated sandbox, brokered access, a backlog and a place in the org chart — centrally governed.",
    },
    indexable: true,
    priority: 1.0,
  },
  {
    id: "funktion",
    path: { de: "/funktion", en: "/en/how-it-works" },
    title: {
      de: "So funktioniert Covey — Sandbox, Backlog, Guard-Rails",
      en: "How Covey works — sandbox, backlog, guard-rails",
    },
    description: {
      de: "Vom Einstellen bis zur Abnahme: wie Covey Agenten mit Identität ausstattet, isoliert arbeiten lässt, Zugänge zur Laufzeit brokert und jede Sitzung nachvollziehbar hält.",
      en: "From hiring to sign-off: how Covey gives agents an identity, an isolated workspace, access brokered at runtime and a recorded, auditable trail for every session.",
    },
    indexable: true,
    priority: 0.9,
  },
  {
    id: "integrationen",
    path: { de: "/integrationen", en: "/en/integrations" },
    title: {
      de: "Integrationen — Engines und Zielsysteme für Ihre KI-Agenten",
      en: "Integrations — engines and target systems for your AI agents",
    },
    description: {
      de: "Läuft mit Claude, Codex/ChatGPT als zweite Engine — und dockt über Zielsystem-Plugins an Zammad, GitHub, GitLab, E-Mail, Microsoft Teams und MCP an.",
      en: "Works with Claude, with Codex/ChatGPT as the second engine — and connects to Zammad, GitHub, GitLab, email, Microsoft Teams and MCP through target-system plugins.",
    },
    indexable: true,
    priority: 0.8,
  },
  {
    id: "produkt-covey",
    path: { de: "/produkt/covey", en: "/en/product/covey" },
    title: {
      de: "Covey — die Plattform für Ihre KI-Belegschaft",
      en: "Covey — the platform for your AI workforce",
    },
    description: {
      de: "Die Control Plane: Agenten-Registry, Org-Chart, Backlog, Secrets-Broker, Guard-Rails und Kostenübersicht — ein Binary, selbst gehostet.",
      en: "The control plane: agent registry, org chart, backlog, secrets broker, guard-rails and cost tracking — a single binary you host yourself.",
    },
    indexable: true,
    priority: 0.8,
  },
  {
    id: "produkt-companion",
    path: { de: "/produkt/companion", en: "/en/product/companion" },
    title: {
      de: "Companion — Wissen, das im Team bleibt",
      en: "Companion — knowledge that stays with the team",
    },
    description: {
      de: "Der Companion nimmt auf, was im Kopf liegt, und stellt es kuratiert als Kontext für Agenten und Kollegen bereit — für Vertretung, Urlaub und Onboarding.",
      en: "Companion captures what is in your head and turns it into curated context for agents and colleagues — for cover, vacation and onboarding.",
    },
    indexable: true,
    priority: 0.7,
  },
  {
    id: "docs",
    path: { de: "/docs", en: "/en/docs" },
    title: {
      de: "Dokumentation — Covey",
      en: "Documentation — Covey",
    },
    description: {
      de: "Covey selbst hosten und betreiben: Installation mit Docker, den ersten KI-Agenten anlegen, Zielsysteme anbinden, Guard-Rails setzen, Sandboxen betreiben.",
      en: "Self-host and operate Covey: install with Docker, create your first AI agent, connect target systems, set guard-rails and run sandboxes.",
    },
    canonical: { de: "/docs/was-ist-covey", en: "/en/docs/what-is-covey" },
    indexable: true,
    priority: 0.7,
  },
  {
    id: "anmelden",
    path: { de: "/anmelden", en: "/en/sign-in" },
    title: { de: "Anmelden — Covey", en: "Sign in — Covey" },
    description: {
      de: "Anmeldung an dieser Covey-Installation — der Control Plane für Ihre KI-Belegschaft.",
      en: "Sign in to this Covey installation — the control plane for your AI workforce.",
    },
    // Eine Anmeldemaske gehört nicht in den Index: kein Inhalt, und in der
    // Suche wäre sie die falsche Landung.
    indexable: false,
    priority: 0.1,
  },
  {
    id: "registrieren",
    path: { de: "/registrieren", en: "/en/sign-up" },
    title: {
      de: "Registrieren — Covey",
      en: "Sign up — Covey",
    },
    description: {
      de: "Mit einem Wartelisten-Code ein Konto anlegen, einer Organisation beitreten oder eine eigene gründen.",
      en: "Create an account with a waitlist code, then join an organisation or start your own.",
    },
    /* Anders als die Anmeldemaske gehört diese Seite sehr wohl in den Index:
       Sie ist der Einstieg für Leute, die Covey noch nicht haben — also genau
       die Landung, nach der jemand sucht. Ob die Registrierung offen ist,
       entscheidet der Server zur Laufzeit (signup-state), nicht der Index. */
    indexable: true,
    priority: 0.6,
  },
];

/* Die 404-Seite steht bewusst nicht in PUBLIC_ROUTES: Sie hat keine eigene
   Adresse, sondern erscheint unter jeder falschen. Sie wird trotzdem
   vorgerendert (dist/404.html) und trägt einen eigenen Kopf. */
export const NOT_FOUND_ROUTE: PublicRoute = {
  id: "404",
  path: { de: "/404", en: "/en/404" },
  title: { de: "Seite nicht gefunden — Covey", en: "Page not found — Covey" },
  description: {
    de: "Diese Adresse führt ins Leere.",
    en: "This address leads nowhere.",
  },
  indexable: false,
  priority: 0,
};

/* Markdown-Auszeichnung aus einem Text nehmen — für alles, was im Kopf einer
   Seite landet (Description, FAQ-Antworten): dort steht Text, kein Markdown,
   und Backticks und Sternchen erschienen sonst genau so im Suchergebnis.

   Sternchen und Unterstriche fallen nur dort weg, wo sie ein Wort einrahmen.
   Pauschal entfernt wurde aus `COVEY_PUBLIC_URL` ein „COVEYPUBLICURL" — ein
   Name, den es nicht gibt, und ausgerechnet in dem Text, den jemand aus dem
   Suchergebnis abschreibt. */
export function plainText(md: string): string {
  return md
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1") // Links → Linktext
    .replace(/`/g, "")
    .replace(/\*{1,2}([^*]+)\*{1,2}/g, "$1")
    .replace(/(^|[\s(])_([^_]+)_(?=[\s.,;:!?)]|$)/g, "$1$2")
    .replace(/\s+/g, " ")
    .trim();
}

/* Aus einem Markdown-Body die erste Fließtext-Zeile als Description ziehen —
   ohne Überschrift, ohne Auszeichnung, auf Snippet-Länge gekürzt. */
export function descriptionFromMarkdown(md: string, max = 160): string {
  const line = md
    .split("\n")
    .map((l) => l.trim())
    .find((l) => l.length > 0 && !l.startsWith("#") && !l.startsWith(">"));
  if (!line) return "";
  const plain = plainText(line);
  if (plain.length <= max) return plain;
  const cut = plain.slice(0, max);
  return cut.slice(0, cut.lastIndexOf(" ")).replace(/[,;:—-]$/, "") + " …";
}

/* Die Docs-Seiten kommen aus dem Inhalt selbst — eine neue Seite in
   docsContent.ts steht damit ohne weiteres Zutun in der Sitemap.

   Der Slug kommt je Sprache aus dem Inhalt: Die englische Fassung lief vorher
   unter dem deutschen (/en/docs/gedaechtnis), und eine Adresse ist das Erste,
   was von einer Seite gelesen wird.

   Die Description ebenso — geschrieben, nicht geschnitten. Der Automatismus
   bleibt als Rückfallweg stehen, damit eine neue Seite ohne Description keine
   leere im Suchergebnis bekommt, sondern eine mittelgute. */
const DOCS: PublicRoute[] = DOC_SECTIONS.flatMap((sec) =>
  sec.pages.map((p) => ({
    id: `docs-${p.slug.de}`,
    path: { de: `/docs/${p.slug.de}`, en: `/en/docs/${p.slug.en}` },
    title: {
      de: `${p.title.de} — Covey Docs`,
      en: `${p.title.en} — Covey Docs`,
    },
    description: {
      de: p.description.de || descriptionFromMarkdown(p.body.de),
      en: p.description.en || descriptionFromMarkdown(p.body.en),
    },
    indexable: true,
    priority: 0.6,
    faq: p.faq,
  })),
);

/** Die festen Seiten — sie tragen das Routing; die Docs-Einträge nicht. */
export const PAGE_ROUTES: PublicRoute[] = PAGES;

export const PUBLIC_ROUTES: PublicRoute[] = [...PAGES, ...DOCS];

/** Pfad einer Seite in der gewünschten Sprache. */
export function pathOf(id: string, lang: Lang): string {
  const route = PUBLIC_ROUTES.find((r) => r.id === id);
  if (route) return route.path[lang];
  return lang === "en" ? "/en" : "/";
}

/* Alle indexierbaren URLs, flach — die Vorlage für sitemap.xml. Jede trägt
   ihre Gegenstücke mit: Eine Sitemap ohne Alternates lässt Google die zwei
   Sprachfassungen als konkurrierende Seiten sehen statt als Übersetzungen. */
export type SeoUrl = {
  path: string;
  lang: Lang;
  priority: number;
  alt: Localized;
};

export const SEO_URLS: SeoUrl[] = PUBLIC_ROUTES.filter((r) => r.indexable).flatMap(
  (r) => LANGS.map((l) => ({ path: r.path[l], lang: l, priority: r.priority, alt: r.path })),
);

/* Auch die nicht indexierbaren Seiten werden vorgerendert — sie sollen ohne
   JavaScript nicht leer sein, nur eben nicht in die Sitemap. */
export const PRERENDER_URLS: Array<{ path: string; lang: Lang }> =
  PUBLIC_ROUTES.flatMap((r) => LANGS.map((l) => ({ path: r.path[l], lang: l })));

/** Route + Sprache zu einem Pfad. null = keine öffentliche Seite. */
export function matchRoute(
  pathname: string,
): { route: PublicRoute; lang: Lang } | null {
  const clean = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
  for (const route of PUBLIC_ROUTES) {
    for (const lang of LANGS) {
      if (route.path[lang] === clean) return { route, lang };
    }
  }
  return null;
}

/** Der gleiche Inhalt in der anderen Sprache — für hreflang und Umschalter. */
export function altPath(pathname: string, lang: Lang): string {
  const hit = matchRoute(pathname);
  if (hit) return hit.route.path[lang];
  // Unbekannter Pfad (z. B. eine neue Docs-Seite ohne Eintrag): auf die
  // Startseite der Zielsprache, statt in eine 404 zu schicken.
  return lang === "en" ? "/en" : "/";
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
];

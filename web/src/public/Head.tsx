/* Der Kopf jeder öffentlichen Seite — einmal beschrieben, zweimal verwendet:
   beim Vorrendern als HTML-Text (prerender.mjs) und im Browser als DOM-Update
   bei jedem Seitenwechsel. Beides aus derselben Funktion, damit eine
   clientseitig navigierte Seite denselben Kopf trägt wie ihre gerenderte
   Fassung — sonst driften die zwei Wahrheiten auseinander und niemand merkt es.

   Die Origin ist beim Vorrendern nicht bekannt: Covey wird selbst gehostet,
   jede Installation hat ihre eigene Adresse. Deshalb steht dort ein Platzhalter,
   den der Go-Server beim Ausliefern durch die tatsächliche Origin ersetzt
   (internal/httpapi/spa.go). */

import { useEffect } from "react";
import { useLocation } from "react-router";
import { LANGS, NOT_FOUND_ROUTE, matchRoute, type Lang, type PublicRoute } from "./seo";
import { langFromPath } from "../i18n";

/** Platzhalter im vorgerenderten HTML; der Server setzt die echte Origin ein. */
export const ORIGIN_PLACEHOLDER = "__COVEY_ORIGIN__";

const OG_IMAGE = "/landing/murmuration.jpg";
const OG_IMAGE_SIZE = { width: "1400", height: "927" };

export type Tag =
  | { tag: "title"; text: string }
  | { tag: "meta"; attrs: Record<string, string> }
  | { tag: "link"; attrs: Record<string, string> }
  | { tag: "script"; attrs: Record<string, string>; text: string };

const OG_LOCALE: Record<Lang, string> = { de: "de_DE", en: "en_US" };

/* Strukturierte Daten. Nur Belegbares: wer betreibt die Software, wie heißt
   sie, was ist sie. Bewertungen oder Preise stehen hier bewusst nicht — was
   nicht stimmt, fällt in der Search Console als „strukturierte Daten
   ungültig" zurück und schadet mehr, als es nützt. */
function jsonLd(route: PublicRoute, lang: Lang, origin: string): object | null {
  const home = `${origin}/`;
  const org = {
    "@type": "Organization",
    "@id": `${origin}/#organization`,
    name: "Covey",
    url: home,
    logo: `${origin}/icon-512.png`,
  };

  if (route.id === "home") {
    return {
      "@context": "https://schema.org",
      "@graph": [
        org,
        {
          "@type": "WebSite",
          "@id": `${origin}/#website`,
          name: "Covey",
          url: home,
          inLanguage: lang,
          publisher: { "@id": `${origin}/#organization` },
        },
        {
          "@type": "SoftwareApplication",
          name: "Covey",
          applicationCategory: "BusinessApplication",
          operatingSystem: "Linux, Docker",
          url: home,
          description: route.description[lang],
          publisher: { "@id": `${origin}/#organization` },
        },
      ],
    };
  }

  if (route.id.startsWith("docs-")) {
    const docsPath = lang === "de" ? "/docs" : "/en/docs";
    const graph: object[] = [
      {
        "@type": "TechArticle",
        headline: route.title[lang].replace(/ — Covey Docs$/, ""),
        description: route.description[lang],
        inLanguage: lang,
        url: `${origin}${route.path[lang]}`,
        publisher: { "@id": `${origin}/#organization` },
      },
      // Brotkrumen: Docs → Abschnitt → Seite. Der Abschnitt hat keine eigene
      // Adresse — die Docs-Navigation ist eine Gliederung, keine Seitenfolge —,
      // deshalb trägt seine Stufe nur einen Namen. Das ist erlaubt und
      // ehrlicher, als eine URL zu erfinden, die auf nichts zeigt.
      {
        "@type": "BreadcrumbList",
        itemListElement: [
          { "@type": "ListItem", position: 1, name: "Docs", item: `${origin}${docsPath}` },
          ...(route.section ? [{ "@type": "ListItem", position: 2, name: route.section[lang] }] : []),
          {
            "@type": "ListItem",
            position: route.section ? 3 : 2,
            name: route.title[lang].replace(/ — Covey Docs$/, ""),
            item: `${origin}${route.path[lang]}`,
          },
        ],
      },
    ];

    // FAQ nur, wenn die Fragen auch auf der Seite stehen — Docs.tsx rendert
    // dieselbe Quelle. Dass Google FAQ-Rich-Results inzwischen nur noch für
    // wenige Seitenarten anzeigt, ändert daran nichts: Die Auszeichnung bleibt
    // gültig, sie wird von anderen Diensten gelesen, und sie kostet nichts.
    const faq = route.faq?.[lang];
    if (faq && faq.length > 0) {
      graph.push({
        "@type": "FAQPage",
        mainEntity: faq.map((f) => ({
          "@type": "Question",
          name: f.q,
          acceptedAnswer: { "@type": "Answer", text: plainText(f.a) },
        })),
      });
    }

    return { "@context": "https://schema.org", "@graph": graph };
  }

  return null;
}

/* Markdown-Auszeichnung aus einer Antwort nehmen. In den strukturierten Daten
   steht Text, kein Markdown: Backticks und Sternchen würden im Suchergebnis
   genau so erscheinen, wie sie hier stehen. */
function plainText(md: string): string {
  return md
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/[*_`]/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

/** Alle Kopf-Elemente einer Seite. origin ohne abschließenden Schrägstrich. */
export function seoTags(
  route: PublicRoute,
  lang: Lang,
  origin: string = ORIGIN_PLACEHOLDER,
): Tag[] {
  const url = `${origin}${route.path[lang]}`;
  const title = route.title[lang];
  const description = route.description[lang];

  const tags: Tag[] = [
    { tag: "title", text: title },
    { tag: "meta", attrs: { name: "description", content: description } },
    {
      tag: "meta",
      attrs: {
        name: "robots",
        content: route.indexable
          ? "index, follow, max-image-preview:large, max-snippet:-1"
          : "noindex, follow",
      },
    },
  ];

  // Die 404-Seite erscheint unter jeder falschen Adresse — ein Canonical wäre
  // eine Behauptung über eine Seite, die es nicht gibt.
  if (route.id !== "404") {
    tags.push({ tag: "link", attrs: { rel: "canonical", href: url } });
  }

  // hreflang: beide Fassungen zeigen aufeinander, x-default auf die deutsche.
  // Fehlt die Gegenrichtung, wertet Google die Angabe nicht.
  if (route.indexable) {
    for (const l of LANGS) {
      tags.push({
        tag: "link",
        attrs: { rel: "alternate", hreflang: l, href: `${origin}${route.path[l]}` },
      });
    }
    tags.push({
      tag: "link",
      attrs: { rel: "alternate", hreflang: "x-default", href: `${origin}${route.path.de}` },
    });
  }

  tags.push(
    { tag: "meta", attrs: { property: "og:type", content: "website" } },
    { tag: "meta", attrs: { property: "og:site_name", content: "Covey" } },
    { tag: "meta", attrs: { property: "og:title", content: title } },
    { tag: "meta", attrs: { property: "og:description", content: description } },
    { tag: "meta", attrs: { property: "og:url", content: url } },
    { tag: "meta", attrs: { property: "og:locale", content: OG_LOCALE[lang] } },
    { tag: "meta", attrs: { property: "og:image", content: `${origin}${OG_IMAGE}` } },
    { tag: "meta", attrs: { property: "og:image:width", content: OG_IMAGE_SIZE.width } },
    { tag: "meta", attrs: { property: "og:image:height", content: OG_IMAGE_SIZE.height } },
    {
      tag: "meta",
      attrs: {
        property: "og:image:alt",
        content: lang === "de" ? "Ein Schwarm Vögel" : "A murmuration of birds",
      },
    },
    { tag: "meta", attrs: { name: "twitter:card", content: "summary_large_image" } },
    { tag: "meta", attrs: { name: "twitter:title", content: title } },
    { tag: "meta", attrs: { name: "twitter:description", content: description } },
    { tag: "meta", attrs: { name: "twitter:image", content: `${origin}${OG_IMAGE}` } },
  );

  const ld = jsonLd(route, lang, origin);
  if (ld) {
    tags.push({
      tag: "script",
      attrs: { type: "application/ld+json" },
      text: JSON.stringify(ld),
    });
  }

  return tags;
}

const esc = (s: string) =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

/* Kopf-Elemente als HTML — für das Vorrendern. Das data-seo trägt dieselbe
   Marke wie die clientseitig gesetzten Elemente: Beim ersten Seitenwechsel
   räumt applyTags die vorgerenderten weg, statt sie zu verdoppeln. */
export function renderHeadTags(tags: Tag[]): string {
  return tags
    .map((t) => {
      if (t.tag === "title") return `<title>${esc(t.text)}</title>`;
      const attrs = Object.entries({ ...t.attrs, "data-seo": "" })
        .map(([k, v]) => `${k}="${esc(v)}"`)
        .join(" ");
      if (t.tag === "script") {
        // Kein esc(): JSON-LD steht in einem Skript-Element, dort wäre &quot;
        // falsch. Nur der Ausstieg aus dem Element muss verhindert werden.
        return `<script ${attrs}>${t.text.replace(/</g, "\\u003c")}</script>`;
      }
      return `<${t.tag} ${attrs} />`;
    })
    .join("\n    ");
}

/* Im Browser: dieselben Elemente in den bestehenden Kopf schreiben. Alles,
   was von hier stammt, trägt data-seo — beim Seitenwechsel wird genau das
   entfernt und neu gesetzt, statt zu raten, was noch gilt. */
function applyTags(tags: Tag[]) {
  const head = document.head;
  head.querySelectorAll("[data-seo]").forEach((el) => el.remove());

  for (const t of tags) {
    if (t.tag === "title") {
      document.title = t.text;
      continue;
    }
    const el = document.createElement(t.tag);
    for (const [k, v] of Object.entries(t.attrs)) el.setAttribute(k, v);
    if (t.tag === "script") el.textContent = t.text;
    el.setAttribute("data-seo", "");
    head.appendChild(el);
  }
}

/** Hält Titel, Meta-Angaben und die Sprache des Dokuments am aktuellen Pfad. */
export default function Head() {
  const { pathname } = useLocation();

  useEffect(() => {
    const hit = matchRoute(pathname) ?? {
      route: NOT_FOUND_ROUTE,
      lang: langFromPath(pathname) ?? ("de" as Lang),
    };
    document.documentElement.lang = hit.lang;
    applyTags(seoTags(hit.route, hit.lang, window.location.origin));
  }, [pathname]);

  return null;
}

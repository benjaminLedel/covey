/* Der Einstieg fürs Vorrendern. Er läuft in Node, nicht im Browser: einmal je
   öffentlicher Seite beim Build, damit in dist/ echte HTML-Dateien mit Inhalt
   und Kopf liegen.

   Warum überhaupt: Google führt JavaScript aus und käme auch an eine leere
   Hülle heran — die übrigen nicht. Bing rendert unzuverlässig, und die Crawler
   der Sprachmodelle (GPTBot, ClaudeBot, PerplexityBot) führen gar keins aus.
   Für ein Produkt, das in solchen Antworten vorkommen will, ist das der
   Unterschied zwischen lesbar und unsichtbar.

   Das Ergebnis wird im Browser hydriert (main.tsx), nicht ersetzt — deshalb
   muss hier genau das herauskommen, was der erste Rendervorgang im Browser
   erzeugt. Alles, was von window, localStorage oder dem Hostnamen abhängt,
   gehört darum in einen Effekt und nicht in den Render. */

import { renderToString } from "react-dom/server";
import { StaticRouter } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import PublicSite from "./public/PublicSite";
import { renderHeadTags, seoTags } from "./public/Head";
import { NOT_FOUND_ROUTE, matchRoute, type Lang } from "./public/seo";
import i18n from "./i18n";

export { PRERENDER_URLS, SEO_URLS, APP_ROUTE_PREFIXES, NOT_FOUND_ROUTE, LANGS } from "./public/seo";

export type RenderResult = { html: string; head: string; lang: Lang };

export async function renderPage(url: string, lang: Lang): Promise<RenderResult> {
  await i18n.changeLanguage(lang);

  const html = renderToString(
    <QueryClientProvider client={new QueryClient()}>
      <StaticRouter location={url}>
        <PublicSite onLogin={() => {}} />
      </StaticRouter>
    </QueryClientProvider>,
  );

  const hit = matchRoute(url);
  const route = hit ? hit.route : NOT_FOUND_ROUTE;
  return { html, head: renderHeadTags(seoTags(route, lang)), lang };
}

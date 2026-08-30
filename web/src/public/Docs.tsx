import { Link, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { Markdown } from "../components/Markdown";
import { DOC_SECTIONS, DOC_PAGES, FIRST_DOC } from "./docs/content.generated";
import { usePublicLang } from "./lang";
import { pathOf } from "./seo";

/* Docs-Bereich: linke Kategorie-/Seiten-Navigation + Markdown-Inhalt.
   Zweisprachig — Titel und Body je Sprache in den Markdown-Dateien unter docs/. Die
   Sprache kommt aus der URL, nicht aus i18n: Sie steht damit schon beim ersten
   Rendern fest und ist beim Vorrendern dieselbe wie im Browser. */
export default function Docs() {
  const { t } = useTranslation();
  const lang = usePublicLang();
  const base = pathOf("docs", lang);
  const { slug } = useParams<{ slug: string }>();
  // Der Slug kommt in der Sprache der URL — /docs/gedaechtnis und
  // /en/docs/memory sind dieselbe Seite unter ihrem jeweils eigenen Namen.
  // Die andere Sprache wird mitgesucht, damit ein von Hand umgeschriebener
  // Pfad (/en/docs/gedaechtnis) auf der Seite landet statt auf der ersten.
  const page =
    DOC_PAGES.find((p) => p.slug[lang] === slug) ??
    DOC_PAGES.find((p) => p.slug.de === slug || p.slug.en === slug) ??
    FIRST_DOC;

  const idx = DOC_PAGES.findIndex((p) => p.slug.de === page.slug.de);
  const prev = idx > 0 ? DOC_PAGES[idx - 1] : null;
  const next = idx < DOC_PAGES.length - 1 ? DOC_PAGES[idx + 1] : null;

  return (
    <div className="docs">
      <aside className="docs-nav" aria-label={t("public.docs.aria")}>
        <div className="docs-nav-title">{t("public.docs.title")}</div>
        {DOC_SECTIONS.map((sec) => (
          <div className="docs-nav-sec" key={sec.id}>
            <div className="docs-nav-cat">{sec.title[lang]}</div>
            <ul>
              {sec.pages.map((p) => (
                <li key={p.slug.de}>
                  <Link
                    to={`${base}/${p.slug[lang]}`}
                    className={`docs-nav-link ${p.slug.de === page.slug.de ? "active" : ""}`}
                  >
                    {p.title[lang]}
                  </Link>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </aside>

      <article className="docs-content">
        <div className="docs-body">
          <Markdown text={page.body[lang]} baseLevel={1} />
        </div>

        {page.faq && page.faq[lang].length > 0 && (
          // Die Fragen stehen als sichtbarer Teil der Seite, nicht nur in der
          // Auszeichnung. Andersherum — Fragen im JSON-LD, die auf der Seite
          // niemand findet — wäre es das, was Suchmaschinen zu Recht
          // abstrafen; und für den Leser ist es der nützlichere Teil.
          <section className="docs-faq" aria-labelledby="docs-faq-title">
            <h2 id="docs-faq-title">{t("public.docs.faq")}</h2>
            <dl>
              {page.faq[lang].map((item) => (
                <div key={item.q}>
                  <dt>{item.q}</dt>
                  <dd>
                    <Markdown text={item.a} baseLevel={3} />
                  </dd>
                </div>
              ))}
            </dl>
          </section>
        )}

        <nav className="docs-pager">
          {prev ? (
            <Link to={`${base}/${prev.slug[lang]}`} className="docs-pager-link prev">
              <span className="docs-pager-dir">← {t("public.docs.prev")}</span>
              <span className="docs-pager-title">{prev.title[lang]}</span>
            </Link>
          ) : <span />}
          {next ? (
            <Link to={`${base}/${next.slug[lang]}`} className="docs-pager-link next">
              <span className="docs-pager-dir">{t("public.docs.next")} →</span>
              <span className="docs-pager-title">{next.title[lang]}</span>
            </Link>
          ) : <span />}
        </nav>
      </article>
    </div>
  );
}

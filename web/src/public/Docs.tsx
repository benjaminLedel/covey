import { Link, useParams } from "react-router";
import { useTranslation } from "react-i18next";
import { Markdown } from "../components/Markdown";
import { DOC_SECTIONS, DOC_PAGES, FIRST_DOC, docLang } from "./docs/docsContent";

/* Docs-Bereich: linke Kategorie-/Seiten-Navigation + Markdown-Inhalt.
   Zweisprachig — Titel und Body je Sprache in docs/docsContent.ts. */
export default function Docs() {
  const { t, i18n } = useTranslation();
  const lang = docLang(i18n.language);
  const { slug } = useParams<{ slug: string }>();
  const page = DOC_PAGES.find((p) => p.slug === slug) ?? FIRST_DOC;

  const idx = DOC_PAGES.findIndex((p) => p.slug === page.slug);
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
                <li key={p.slug}>
                  <Link
                    to={`/docs/${p.slug}`}
                    className={`docs-nav-link ${p.slug === page.slug ? "active" : ""}`}
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
          <Markdown text={page.body[lang]} />
        </div>

        <nav className="docs-pager">
          {prev ? (
            <Link to={`/docs/${prev.slug}`} className="docs-pager-link prev">
              <span className="docs-pager-dir">← {t("public.docs.prev")}</span>
              <span className="docs-pager-title">{prev.title[lang]}</span>
            </Link>
          ) : <span />}
          {next ? (
            <Link to={`/docs/${next.slug}`} className="docs-pager-link next">
              <span className="docs-pager-dir">{t("public.docs.next")} →</span>
              <span className="docs-pager-title">{next.title[lang]}</span>
            </Link>
          ) : <span />}
        </nav>
      </article>
    </div>
  );
}

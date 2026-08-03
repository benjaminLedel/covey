import { NavLink, Link, useLocation, useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import { merkeSprache } from "../i18n";
import { BirdMark } from "./chrome";
import { usePublicLang } from "./lang";
import { LANGS, altPath, pathOf } from "./seo";

/* Sticky Kopfzeile der öffentlichen Website: Wortmarke, Seiten-Navigation,
   Sprachumschalter und Anmelde-Knopf. */
export default function PublicNav() {
  const { t } = useTranslation();
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const lang = usePublicLang();

  /* Der Umschalter wechselt die Adresse, nicht nur die Anzeige — sonst gäbe
     es die englische Fassung nur im Browser des Umschaltenden. Die Wahl wird
     zusätzlich gemerkt, weil die angemeldete Oberfläche sie von dort liest. */
  const setLang = (l: "de" | "en") => {
    merkeSprache(l);
    navigate(altPath(pathname, l));
  };

  const link = ({ isActive }: { isActive: boolean }) =>
    `pubnav-link ${isActive ? "active" : ""}`;

  const nav: Array<[string, string]> = [
    ["funktion", "public.nav.function"],
    ["integrationen", "public.nav.integrations"],
    ["produkt-covey", "public.nav.covey"],
    ["produkt-companion", "public.nav.companion"],
    ["docs", "public.nav.docs"],
  ];

  return (
    <header className="pubnav">
      <div className="pubnav-inner">
        <Link to={pathOf("home", lang)} className="pubnav-brand" aria-label="Covey">
          <BirdMark size={30} />
          <span className="pubnav-word">Covey</span>
        </Link>

        <nav className="pubnav-links" aria-label={t("public.nav.aria")}>
          {nav.map(([id, key]) => (
            <NavLink key={id} to={pathOf(id, lang)} className={link}>
              {t(key)}
            </NavLink>
          ))}
        </nav>

        <div className="pubnav-right">
          <div className="lang-switch inline" role="group" aria-label="Sprache / Language">
            {LANGS.map((l) => (
              <button
                key={l}
                className={lang === l ? "on" : ""}
                aria-pressed={lang === l}
                onClick={() => setLang(l)}
              >
                {l.toUpperCase()}
              </button>
            ))}
          </div>
          <Link to={pathOf("anmelden", lang)} className="btn primary pubnav-cta">
            {t("public.nav.login")}
          </Link>
        </div>
      </div>
    </header>
  );
}

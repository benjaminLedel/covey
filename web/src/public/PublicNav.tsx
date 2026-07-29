import { NavLink, Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import i18n from "../i18n";
import { BirdMark } from "./chrome";

/* Sticky Kopfzeile der öffentlichen Website: Wortmarke, Seiten-Navigation,
   Sprachumschalter und Anmelde-Knopf. */
export default function PublicNav() {
  const { t } = useTranslation();

  const setLang = (l: "de" | "en") => {
    i18n.changeLanguage(l);
    localStorage.setItem("covey.lang", l);
  };

  const link = ({ isActive }: { isActive: boolean }) =>
    `pubnav-link ${isActive ? "active" : ""}`;

  return (
    <header className="pubnav">
      <div className="pubnav-inner">
        <Link to="/" className="pubnav-brand" aria-label="Covey">
          <BirdMark size={30} />
          <span className="pubnav-word">Covey</span>
        </Link>

        <nav className="pubnav-links" aria-label={t("public.nav.aria")}>
          <NavLink to="/funktion" className={link}>
            {t("public.nav.function")}
          </NavLink>
          <NavLink to="/integrationen" className={link}>
            {t("public.nav.integrations")}
          </NavLink>
          <NavLink to="/produkt/covey" className={link}>
            {t("public.nav.covey")}
          </NavLink>
          <NavLink to="/produkt/companion" className={link}>
            {t("public.nav.companion")}
          </NavLink>
          <NavLink to="/docs" className={link}>
            {t("public.nav.docs")}
          </NavLink>
        </nav>

        <div className="pubnav-right">
          <div className="lang-switch inline" role="group" aria-label="Sprache / Language">
            {(["de", "en"] as const).map((l) => (
              <button
                key={l}
                className={i18n.language === l ? "on" : ""}
                aria-pressed={i18n.language === l}
                onClick={() => setLang(l)}
              >
                {l.toUpperCase()}
              </button>
            ))}
          </div>
          <Link to="/anmelden" className="btn primary pubnav-cta">
            {t("public.nav.login")}
          </Link>
        </div>
      </div>
    </header>
  );
}

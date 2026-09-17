import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import i18n, { LANG_BY_CODE, LANG_LIST, istLang, ladeSprache, merkeSprache } from "../i18n";
import type { Lang } from "../langs";

/* The language picker — the same control in two places: at the top of the
   sign-in page (`pill`) and in the footer menu of the signed-in UI (`menu`).
   As with the appearance (ThemeSwitch) the choice stands in both places in
   the same component, so it does not drift apart.

   Up to ten languages it was a button that switched between German and
   English. A toggle has two states; from the third one must be able to
   see what exists, and find one's own among them — even when
   the UI stands in a language one does not read. Hence
   the flag and the native name side by side, not an abbreviation. */

/* The same globe as in the navigation (NavIcon "globe") — the entry should
   look like the rows below it, not like a foreign body. */
function NavGlobe() {
  return (
    <svg className="ic" viewBox="0 0 24 24" aria-hidden="true">
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18M12 3c2.5 2.6 2.5 15.4 0 18M12 3c-2.5 2.6-2.5 15.4 0 18" />
    </svg>
  );
}

function Chevron() {
  return (
    <svg className="ic" viewBox="0 0 24 24" aria-hidden="true">
      <path d="M7 10l5 5 5-5" />
    </svg>
  );
}

export default function LangPicker({
  variant = "pill",
  onSelect,
}: {
  variant?: "pill" | "menu";
  /* Before sign-in the language hangs on the address (/fr/connexion), so there
     the page changes with it — the caller says where to. Without a given
     callback it is enough to swap the catalogue and remember the choice. */
  onSelect?: (lang: Lang) => void;
}) {
  const { t } = useTranslation();
  const [offen, setOffen] = useState(false);

  const aktuell: Lang = istLang(i18n.language) ? i18n.language : "en";
  const info = LANG_BY_CODE[aktuell];

  // Escape closes the list — the same expectation as for every other
  // dropdown menu, and the only way out for someone without a mouse.
  useEffect(() => {
    if (!offen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOffen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [offen]);

  const waehle = (lang: Lang) => {
    setOffen(false);
    merkeSprache(lang);
    if (onSelect) onSelect(lang);
    else void ladeSprache(lang);
  };

  /* Ten languages are ten rows, and in the footer menu they would stand beside four
     other entries — the language picker would then be the loudest part of a
     menu in which it is the rarest concern. It therefore stands there as
     a row that says what currently applies, and expands the list only on
     request. At the top of the sign-in page it is the same consideration: there
     nothing is to cover the card before someone asks. */
  const liste = (
    <div className="lang-pick-list" role="listbox" aria-label={t("lang.label")}>
      {LANG_LIST.map((l) => (
        <button
          key={l.code}
          role="option"
          aria-selected={l.code === aktuell}
          className={l.code === aktuell ? "on" : ""}
          lang={l.bcp47}
          onClick={() => waehle(l.code)}
        >
          <span className="flag" aria-hidden="true">
            {l.flag}
          </span>
          <span className="nm">{l.name}</span>
        </button>
      ))}
    </div>
  );

  if (variant === "menu") {
    return (
      <div className="lang-pick menu">
        <button className="lang-pick-row" onClick={() => setOffen((v) => !v)} aria-expanded={offen}>
          <NavGlobe />
          <span className="lb">{t("lang.label")}</span>
          <span className="cur">
            <span className="flag" aria-hidden="true">
              {info.flag}
            </span>
            {info.name}
          </span>
          <Chevron />
        </button>
        {offen && liste}
      </div>
    );
  }

  return (
    <div className={`lang-pick ${variant}`}>
      <button
        className="lang-pick-btn"
        onClick={() => setOffen((v) => !v)}
        aria-haspopup="listbox"
        aria-expanded={offen}
        aria-label={t("lang.label")}
        title={t("lang.label")}
      >
        <span className="flag" aria-hidden="true">
          {info.flag}
        </span>
        <span className="nm">{info.name}</span>
        <Chevron />
      </button>
      {offen && (
        <>
          <div className="lang-pick-backdrop" onClick={() => setOffen(false)} />
          {liste}
        </>
      )}
    </div>
  );
}

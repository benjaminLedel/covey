import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import i18n, { LANG_BY_CODE, LANG_LIST, istLang, ladeSprache, merkeSprache } from "../i18n";
import type { Lang } from "../langs";

/* Die Sprachwahl — dieselbe Steuerung an zwei Orten: oben auf der
   Anmeldeseite (`pill`) und im Fußmenü der angemeldeten Oberfläche (`menu`).
   Wie beim Erscheinungsbild (ThemeSwitch) steht die Wahl an beiden Stellen in
   derselben Komponente, damit sie nicht auseinanderläuft.

   Bis zehn Sprachen war es ein Knopf, der zwischen Deutsch und Englisch
   umschaltete. Ein Umschalter kann zwei Zustände; ab dem dritten muss man
   sehen können, was es gibt, und darunter das Eigene finden — auch dann, wenn
   die Oberfläche gerade in einer Sprache steht, die man nicht liest. Deshalb
   Flagge und Eigenname nebeneinander, nicht ein Kürzel. */

/* Derselbe Globus wie in der Navigation (NavIcon "globe") — der Eintrag soll
   aussehen wie die Zeilen darunter, nicht wie ein Fremdkörper. */
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
  /* Vor der Anmeldung hängt die Sprache an der Adresse (/fr/connexion), also
     wechselt dort die Seite mit — der Aufrufer sagt, wohin. Ohne Angabe
     genügt es, den Katalog zu tauschen und die Wahl zu merken. */
  onSelect?: (lang: Lang) => void;
}) {
  const { t } = useTranslation();
  const [offen, setOffen] = useState(false);

  const aktuell: Lang = istLang(i18n.language) ? i18n.language : "en";
  const info = LANG_BY_CODE[aktuell];

  // Escape schließt die Liste — dieselbe Erwartung wie bei jedem anderen
  // Aufklappmenü, und der einzige Weg heraus für jemanden ohne Maus.
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

  /* Zehn Sprachen sind zehn Zeilen, und im Fußmenü stünden sie neben vier
     anderen Einträgen — die Sprachwahl wäre dann der lauteste Teil eines
     Menüs, in dem sie das seltenste Anliegen ist. Sie steht dort deshalb als
     eine Zeile, die sagt, was gerade gilt, und die Liste erst auf Wunsch
     ausklappt. Oben auf der Anmeldeseite ist es dieselbe Überlegung: dort
     soll nichts die Karte verdecken, bevor jemand fragt. */
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

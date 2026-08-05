import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { THEMES, gespeichertesTheme, merkeTheme, wendeThemeAn, type Theme } from "../theme";

/* Umschalter fürs Erscheinungsbild — dieselbe Steuerung an zwei Orten: im
   Fußmenü der angemeldeten Oberfläche (`seg`) und in der Kopfzeile der
   öffentlichen Website (`pill`, neben der Sprachwahl).

   Drei Möglichkeiten nebeneinander statt eines Knopfes, der durchschaltet: Man
   soll sehen können, was gerade gilt, ohne es auszuprobieren — und „System"
   ist eine eigene Wahl, kein dritter Zustand zwischen hell und dunkel. */

const icons: Record<Theme, ReactNode> = {
  // Bildschirm: die Einstellung des Betriebssystems.
  system: (
    <>
      <rect x="3" y="4" width="18" height="12" rx="2" />
      <path d="M9 20h6M12 16v4" />
    </>
  ),
  // Sonne: hell, unabhängig vom System.
  light: (
    <>
      <circle cx="12" cy="12" r="4.2" />
      <path d="M12 2.5v2.2M12 19.3v2.2M4.2 4.2l1.6 1.6M18.2 18.2l1.6 1.6M2.5 12h2.2M19.3 12h2.2M4.2 19.8l1.6-1.6M18.2 5.8l1.6-1.6" />
    </>
  ),
  dark: <path d="M20.5 14.6A8.6 8.6 0 0 1 9.4 3.5a8.6 8.6 0 1 0 11.1 11.1z" />,
};

function ThemeIcon({ name }: { name: Theme }) {
  return (
    <svg className="ic" viewBox="0 0 24 24" aria-hidden="true">
      {icons[name]}
    </svg>
  );
}

export default function ThemeSwitch({ variant = "seg" }: { variant?: "seg" | "pill" }) {
  const { t } = useTranslation();

  /* Die gespeicherte Wahl kommt erst nach dem ersten Rendervorgang zum Zug:
     Auf den vorgerenderten Seiten muss dieser das ausgelieferte Markup treffen,
     und localStorage kennt der Server nicht (siehe entry-server.tsx). Die
     Farben selbst hängen nicht daran — sie stehen im Stylesheet und folgen bis
     dahin dem Betriebssystem. */
  const [theme, setTheme] = useState<Theme>("system");
  useEffect(() => setTheme(gespeichertesTheme()), []);

  const waehle = (next: Theme) => {
    setTheme(next);
    merkeTheme(next);
    wendeThemeAn(next);
  };

  const pill = variant === "pill";
  return (
    <div
      className={pill ? "lang-switch inline theme-pill" : "theme-seg"}
      role="group"
      aria-label={t("theme.label")}
    >
      {THEMES.map((m) => (
        <button
          key={m}
          className={theme === m ? "on" : ""}
          aria-pressed={theme === m}
          title={t(`theme.${m}`)}
          aria-label={pill ? t(`theme.${m}`) : undefined}
          onClick={() => waehle(m)}
        >
          <ThemeIcon name={m} />
          {!pill && <span>{t(`theme.${m}`)}</span>}
        </button>
      ))}
    </div>
  );
}

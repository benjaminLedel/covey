import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { THEMES, gespeichertesTheme, merkeTheme, wendeThemeAn, type Theme } from "../theme";

/* Switch for the appearance — the same control in two places: in the
   footer menu of the signed-in UI (`seg`, a row) and in the header of the
   public website (`pill`, next to the language picker).

   Three options side by side instead of one button that cycles: you should be
   able to see which one applies without trying it out — and `System`
   is a choice of its own, not a third state between light and dark. */

const icons: Record<Theme, ReactNode> = {
  // Screen: the setting of the operating system.
  system: (
    <>
      <rect x="3" y="4" width="18" height="12" rx="2" />
      <path d="M9 20h6M12 16v4" />
    </>
  ),
  // Sun: light, independent of the system.
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

  /* The stored choice only takes effect after the first render: on the
     prerendered pages it has to hit the markup that was served, and the
     server does not know localStorage (see entry-server.tsx). The
     colours themselves do not depend on it — they stand in the stylesheet,
     and until then follow the operating system. */
  const [theme, setTheme] = useState<Theme>("system");
  useEffect(() => setTheme(gespeichertesTheme()), []);

  const waehle = (next: Theme) => {
    setTheme(next);
    merkeTheme(next);
    wendeThemeAn(next);
  };

  if (variant === "pill") {
    return (
      <div className="lang-switch inline theme-pill" role="group" aria-label={t("theme.label")}>
        {THEMES.map((m) => (
          <button
            key={m}
            className={theme === m ? "on" : ""}
            aria-pressed={theme === m}
            title={t(`theme.${m}`)}
            aria-label={t(`theme.${m}`)}
            onClick={() => waehle(m)}
          >
            <ThemeIcon name={m} />
          </button>
        ))}
      </div>
    );
  }

  /* In the footer menu a row like the language beside it (#432): what it
     is on the left, the three choices as marks on the right — each named by
     its tooltip and for a screen reader, the chosen one raised. */
  return (
    <div className="theme-row">
      <ThemeIcon name={theme} />
      <span className="lb">{t("theme.label")}</span>
      <div className="theme-marks" role="group" aria-label={t("theme.label")}>
        {THEMES.map((m) => (
          <button
            key={m}
            className={theme === m ? "on" : ""}
            aria-pressed={theme === m}
            title={t(`theme.${m}`)}
            aria-label={t(`theme.${m}`)}
            onClick={() => waehle(m)}
          >
            <ThemeIcon name={m} />
          </button>
        ))}
      </div>
    </div>
  );
}

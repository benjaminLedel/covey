import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { THEMES, gespeichertesTheme, merkeTheme, wendeThemeAn, type Theme } from "../theme";

/* The appearance, as a setting of the account beside the language (#432):
   light, dark, or what the operating system says — and that last one is the
   default, so most people never need to come here. Kept in this browser. */
export default function ThemeSwitch({ id }: { id: string }) {
  const { t } = useTranslation();

  /* The stored choice only takes effect after the first render: on the
     prerendered pages it has to hit the markup that was served, and the
     server does not know localStorage (see entry-server.tsx). The
     colours themselves do not depend on it — they stand in the stylesheet,
     and until then follow the operating system. */
  const [theme, setTheme] = useState<Theme>("system");
  useEffect(() => setTheme(gespeichertesTheme()), []);

  return (
    <select
      id={id}
      value={theme}
      onChange={(e) => {
        const next = e.target.value as Theme;
        setTheme(next);
        merkeTheme(next);
        wendeThemeAn(next);
      }}
    >
      {THEMES.map((m) => (
        <option key={m} value={m}>
          {t(`theme.${m}`)}
        </option>
      ))}
    </select>
  );
}

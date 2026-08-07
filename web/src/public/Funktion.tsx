import PubLink from "./PubLink";
import { useTranslation } from "react-i18next";
import { icons } from "./chrome";

/* Funktion — der ausführliche Fähigkeiten-Katalog. Spiegelt den heutigen
   Umfang von Covey (deutlich über den MVP hinaus): Identität & Struktur,
   Arbeit & Zielsysteme, Sicherheit & Kontrolle, Gedächtnis. */

const GROUPS: { g: string; items: { icon: keyof typeof icons; k: string }[] }[] = [
  {
    g: "structure",
    items: [
      { icon: "sitemap", k: "orgchart" },
      { icon: "users", k: "profiles" },
      { icon: "key", k: "identity" },
    ],
  },
  {
    g: "work",
    items: [
      { icon: "list", k: "backlog" },
      { icon: "plug", k: "targets" },
      { icon: "git", k: "gitlab" },
      { icon: "mail", k: "email" },
      { icon: "spark", k: "runtimes" },
      { icon: "box", k: "sandbox" },
    ],
  },
  {
    g: "control",
    items: [
      { icon: "lock", k: "secrets" },
      { icon: "shield", k: "guardrails" },
      { icon: "net", k: "egress" },
      { icon: "eye", k: "observability" },
      { icon: "coins", k: "costs" },
      { icon: "chart", k: "indicators" },
    ],
  },
  {
    g: "memory",
    items: [
      { icon: "brain", k: "wiki" },
      { icon: "mic", k: "companion" },
    ],
  },
];

export default function Funktion() {
  const { t } = useTranslation();
  return (
    <div className="landing pub-page">
      <header className="pub-hero login-rise">
        <p className="pub-eyebrow">{t("public.function.eyebrow")}</p>
        <h1>{t("public.function.title")}</h1>
        <p className="pub-hero-lead">{t("public.function.lead")}</p>
      </header>

      {GROUPS.map((group) => (
        <section className="pub-group" key={group.g}>
          <h2 className="reveal">{t(`public.function.groups.${group.g}`)}</h2>
          <div className="landing-grid">
            {group.items.map((it, i) => (
              <div
                className="landing-feature reveal"
                style={{ transitionDelay: `${(i % 3) * 0.06}s` }}
                key={it.k}
              >
                <span className="landing-feature-icon" aria-hidden="true">
                  <svg viewBox="0 0 24 24">{icons[it.icon]}</svg>
                </span>
                <h3>{t(`public.function.cap.${it.k}.t`)}</h3>
                <p>{t(`public.function.cap.${it.k}.d`)}</p>
              </div>
            ))}
          </div>
        </section>
      ))}

      <section className="pub-cta reveal">
        <h2>{t("public.function.ctaTitle")}</h2>
        <p>{t("public.function.ctaLead")}</p>
        <div className="pub-cta-row">
          <PubLink id="produkt-covey" className="btn primary">{t("public.function.ctaCovey")}</PubLink>
          <PubLink id="anmelden" className="btn">{t("public.nav.login")}</PubLink>
        </div>
      </section>
    </div>
  );
}

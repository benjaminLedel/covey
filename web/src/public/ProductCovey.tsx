import PubLink from "./PubLink";
import { useTranslation } from "react-i18next";
import { BirdMark, icons } from "./chrome";
import Screenshots from "./Screenshots";

/* Produktseite Covey — die Plattform: KI-Agenten wie Mitarbeiter,
   Control Plane als Produkt. */

const PILLARS: { icon: keyof typeof icons; k: string }[] = [
  { icon: "sitemap", k: "identity" },
  { icon: "box", k: "sandbox" },
  { icon: "lock", k: "broker" },
  { icon: "shield", k: "control" },
  { icon: "chart", k: "delivery" },
];

export default function ProductCovey() {
  const { t } = useTranslation();
  return (
    <div className="landing pub-page">
      <header className="pub-hero pub-hero-product login-rise">
        <div className="pub-hero-brand">
          <BirdMark size={54} />
          <div>
            <p className="pub-eyebrow">{t("public.covey.eyebrow")}</p>
            <h1 className="pub-product-title">Covey</h1>
          </div>
        </div>
        <p className="pub-hero-lead">{t("public.covey.lead")}</p>
        <div className="pub-cta-row">
          <PubLink id="anmelden" className="btn primary">{t("public.nav.login")}</PubLink>
          <PubLink id="funktion" className="btn">{t("public.covey.seeFunctions")}</PubLink>
        </div>
      </header>

      <section className="pub-group">
        <h2 className="reveal">{t("public.covey.pillarsTitle")}</h2>
        <div className="landing-grid">
          {PILLARS.map((p, i) => (
            <div
              className="landing-feature reveal"
              style={{ transitionDelay: `${(i % 3) * 0.06}s` }}
              key={p.k}
            >
              <span className="landing-feature-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24">{icons[p.icon]}</svg>
              </span>
              <h3>{t(`public.covey.pillars.${p.k}.t`)}</h3>
              <p>{t(`public.covey.pillars.${p.k}.d`)}</p>
            </div>
          ))}
        </div>
      </section>

      <Screenshots />

      <section className="pub-split reveal">
        <div className="pub-split-text">
          <h2>{t("public.covey.whoTitle")}</h2>
          <p>{t("public.covey.whoLead")}</p>
          <ul className="landing-points">
            {[1, 2, 3, 4].map((n) => (
              <li key={n}>
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path d="M5 12.5l4.5 4.5L19 7.5" />
                </svg>
                {t(`public.covey.who${n}`)}
              </li>
            ))}
          </ul>
        </div>
        <figure className="pub-split-img">
          <img src="/landing/formation.jpg" alt={t("landing.howAlt")} loading="lazy" />
        </figure>
      </section>

      <section className="landing-how">
        <div className="landing-how-text">
          <h2 className="reveal">{t("landing.howTitle")}</h2>
          <ol>
            {[1, 2, 3].map((n) => (
              <li className="reveal" style={{ transitionDelay: `${(n - 1) * 0.1}s` }} key={n}>
                <span className="landing-step-num" aria-hidden="true">{n}</span>
                <div>
                  <h3>{t(`landing.s${n}t`)}</h3>
                  <p>{t(`landing.s${n}d`)}</p>
                </div>
              </li>
            ))}
          </ol>
        </div>
        <div className="landing-how-img reveal">
          <img src="/landing/murmuration.jpg" alt={t("landing.bandAlt")} loading="lazy" />
        </div>
      </section>

      <section className="pub-cta reveal">
        <h2>{t("public.covey.ctaTitle")}</h2>
        <p>{t("public.covey.ctaLead")}</p>
        <div className="pub-cta-row">
          <PubLink id="anmelden" className="btn primary">{t("public.nav.login")}</PubLink>
          <PubLink id="produkt-companion" className="btn">{t("public.covey.ctaCompanion")}</PubLink>
        </div>
      </section>
    </div>
  );
}

import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import { icons } from "./chrome";

/* Produktseite Companion — die optionale App, die den Brain-Load eines
   Menschen abfängt und (kuratiert) als Kontext für seine Agenten bereitstellt.
   Als in Entwicklung markiert: das Konzept steht (spec/14), der Bau nicht. */

const STEPS: { icon: keyof typeof icons; k: string }[] = [
  { icon: "mic", k: "capture" },
  { icon: "brain", k: "curate" },
  { icon: "spark", k: "feed" },
];

const PILLARS: { icon: keyof typeof icons; k: string }[] = [
  { icon: "mic", k: "voice" },
  { icon: "brain", k: "curator" },
  { icon: "lock", k: "privacy" },
  { icon: "users", k: "continuity" },
];

export default function ProductCompanion() {
  const { t } = useTranslation();
  return (
    <div className="landing pub-page">
      <header className="pub-hero pub-hero-product login-rise">
        <p className="pub-eyebrow">
          {t("public.companion.eyebrow")}
          <span className="pub-badge">{t("public.products.soon")}</span>
        </p>
        <h1 className="pub-product-title">Companion</h1>
        <p className="pub-hero-lead">{t("public.companion.lead")}</p>
        <div className="pub-cta-row">
          <Link to="/produkt/covey" className="btn primary">{t("public.companion.aboutCovey")}</Link>
        </div>
      </header>

      {/* Drei Schritte: Abladen → Kuratieren → Füttern. */}
      <section className="pub-group">
        <h2 className="reveal">{t("public.companion.howTitle")}</h2>
        <div className="pub-steps">
          {STEPS.map((s, i) => (
            <div className="pub-step reveal" style={{ transitionDelay: `${i * 0.08}s` }} key={s.k}>
              <span className="landing-feature-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24">{icons[s.icon]}</svg>
              </span>
              <span className="pub-step-n">{i + 1}</span>
              <h3>{t(`public.companion.steps.${s.k}.t`)}</h3>
              <p>{t(`public.companion.steps.${s.k}.d`)}</p>
            </div>
          ))}
        </div>
      </section>

      <section className="pub-group">
        <h2 className="reveal">{t("public.companion.pillarsTitle")}</h2>
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
              <h3>{t(`public.companion.pillars.${p.k}.t`)}</h3>
              <p>{t(`public.companion.pillars.${p.k}.d`)}</p>
            </div>
          ))}
        </div>
      </section>

      {/* Datenschutz-Zusage als eigener, ruhiger Block. */}
      <section className="pub-note reveal">
        <span className="landing-feature-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24">{icons.lock}</svg>
        </span>
        <div>
          <h3>{t("public.companion.privacyTitle")}</h3>
          <p>{t("public.companion.privacyBody")}</p>
        </div>
      </section>

      <section className="pub-cta reveal">
        <h2>{t("public.companion.ctaTitle")}</h2>
        <p>{t("public.companion.ctaLead")}</p>
        <div className="pub-cta-row">
          <Link to="/produkt/covey" className="btn primary">{t("public.companion.aboutCovey")}</Link>
          <Link to="/funktion" className="btn">{t("public.nav.function")}</Link>
        </div>
      </section>
    </div>
  );
}

import PubLink from "./PubLink";
import { useTranslation } from "react-i18next";
import { BirdMark, RotatingWord, HeroFlock, icons } from "./chrome";
import Screenshots from "./Screenshots";

const FEATURES = ["sitemap", "box", "key", "shield", "list", "eye"] as const;

/* Home — das Dach der öffentlichen Website: Hero mit Schwarm-Animation, das
   Schwarm-Band, die zwei Produkte, die Fähigkeiten und der Ablauf. */
export default function Home() {
  const { t } = useTranslation();

  return (
    <div className="landing">
      <div className="landing-hero">
        <div className="landing-intro">
          <div className="landing-brand login-rise">
            <BirdMark size={64} />
            <h1 className="login-wordmark">Covey</h1>
          </div>
          <p className="landing-tagline login-rise" style={{ animationDelay: "0.08s" }}>
            {t("login.subtitle")}
          </p>
          <p className="landing-pitch login-rise" style={{ animationDelay: "0.16s" }}>
            {t("landing.pitch")}
          </p>
          <p className="landing-rot login-rise" style={{ animationDelay: "0.2s" }}>
            {t("landing.rotPrefix")} <RotatingWord />
          </p>
          <ul className="landing-points login-rise" style={{ animationDelay: "0.24s" }}>
            {[1, 2, 3].map((n) => (
              <li key={n}>
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path d="M5 12.5l4.5 4.5L19 7.5" />
                </svg>
                {t(`landing.point${n}`)}
              </li>
            ))}
          </ul>
          <div className="landing-cta login-rise" style={{ animationDelay: "0.32s" }}>
            <PubLink id="anmelden" className="btn primary">{t("public.nav.login")}</PubLink>
            <PubLink id="funktion" className="btn">{t("public.covey.seeFunctions")}</PubLink>
          </div>
        </div>

        <div className="hero-visual login-rise" style={{ animationDelay: "0.24s" }} aria-hidden="true">
          <HeroFlock />
        </div>
      </div>

      {/* Schwarm-Bild als breites Band — die Leitmetapher wörtlich genommen. */}
      <figure className="landing-band reveal">
        <img src="/landing/murmuration.jpg" alt={t("landing.bandAlt")} loading="lazy" />
        <figcaption>{t("landing.bandCaption")}</figcaption>
      </figure>

      {/* Echte Einblicke in die Software. */}
      <Screenshots />

      {/* Zwei Produkte: die Plattform und die optionale Companion-App. */}
      <section className="landing-features">
        <h2 className="reveal">{t("public.products.title")}</h2>
        <p className="landing-lead reveal">{t("public.products.lead")}</p>
        <div className="pub-products">
          {(["covey", "companion"] as const).map((p, i) => (
            <PubLink
              id={`produkt-${p}`}
              className="pub-product reveal"
              style={{ transitionDelay: `${i * 0.08}s` }}
              key={p}
            >
              <span className="landing-feature-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24">{icons[p === "covey" ? "sitemap" : "mic"]}</svg>
              </span>
              <h3>
                {t(`public.products.${p}.name`)}
                {p === "companion" && (
                  <span className="pub-badge">{t("public.products.soon")}</span>
                )}
              </h3>
              <p>{t(`public.products.${p}.tagline`)}</p>
              <span className="pub-more">
                {t("public.products.more")}
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path d="M5 12h14M13 6l6 6-6 6" />
                </svg>
              </span>
            </PubLink>
          ))}
        </div>
      </section>

      {/* Kern-Fähigkeiten (Kurzfassung; Details auf der Funktion-Seite). */}
      <section className="landing-features">
        <h2 className="reveal">{t("landing.featuresTitle")}</h2>
        <p className="landing-lead reveal">{t("landing.featuresLead")}</p>
        <div className="landing-grid">
          {FEATURES.map((icon, i) => (
            <div
              className="landing-feature reveal"
              style={{ transitionDelay: `${(i % 3) * 0.08}s` }}
              key={icon}
            >
              <span className="landing-feature-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24">{icons[icon]}</svg>
              </span>
              <h3>{t(`landing.f${i + 1}t`)}</h3>
              <p>{t(`landing.f${i + 1}d`)}</p>
            </div>
          ))}
        </div>
        <PubLink id="funktion" className="pub-textlink reveal">
          {t("public.products.allFunctions")}
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M5 12h14M13 6l6 6-6 6" />
          </svg>
        </PubLink>
      </section>

      {/* Ablauf in drei Schritten, daneben die Formation aus dem Schwarm. */}
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
          <img src="/landing/formation.jpg" alt={t("landing.howAlt")} loading="lazy" />
        </div>
      </section>
    </div>
  );
}

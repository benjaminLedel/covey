import PubLink from "./PubLink";
import { useTranslation } from "react-i18next";
import { TargetIcon } from "../components/TargetIcon";

/* Integrationen — die Zielsysteme, an die ein Agent andocken kann. Die Liste
   ist bewusst statisch: die öffentliche Seite ist unangemeldet und fragt
   keine API. Sie spiegelt die Built-ins aus internal/target/ und muss beim
   Anlegen eines neuen Plugins mitgepflegt werden. */

type Item = { name: string; category: string; tag: "webhook" | "poll" | "nosecret" };

const GROUPS: { g: string; items: Item[] }[] = [
  {
    g: "work",
    items: [
      { name: "zammad", category: "ticketing", tag: "webhook" },
      { name: "github", category: "code", tag: "webhook" },
      { name: "gitlab", category: "code", tag: "poll" },
      { name: "email", category: "communication", tag: "poll" },
      { name: "teams", category: "communication", tag: "webhook" },
    ],
  },
  {
    g: "files",
    items: [
      { name: "nextcloud", category: "files", tag: "poll" },
      { name: "sharepoint", category: "files", tag: "poll" },
    ],
  },
  {
    g: "sandbox",
    items: [
      { name: "browser", category: "web", tag: "nosecret" },
      { name: "dev", category: "dev", tag: "nosecret" },
    ],
  },
];

const EXTEND: { k: string; kind: string }[] = [
  { k: "manifest", kind: "custom" },
  { k: "mcp", kind: "mcp" },
];

export default function Integrationen() {
  const { t } = useTranslation();
  return (
    <div className="landing pub-page">
      <header className="pub-hero login-rise">
        <p className="pub-eyebrow">{t("public.integrations.eyebrow")}</p>
        <h1>{t("public.integrations.title")}</h1>
        <p className="pub-hero-lead">{t("public.integrations.lead")}</p>
      </header>

      {GROUPS.map((group) => (
        <section className="pub-group" key={group.g}>
          <h2 className="reveal">{t(`public.integrations.groups.${group.g}`)}</h2>
          <div className="landing-grid">
            {group.items.map((it, i) => (
              <div
                className="landing-feature reveal"
                style={{ transitionDelay: `${(i % 3) * 0.06}s` }}
                key={it.name}
              >
                <div className="int-head">
                  <span className="int-logo" aria-hidden="true">
                    <TargetIcon name={it.name} category={it.category} size={21} />
                  </span>
                  <span className="int-tag">{t(`public.integrations.tag.${it.tag}`)}</span>
                </div>
                <h3>{t(`public.integrations.sys.${it.name}.t`)}</h3>
                <p>{t(`public.integrations.sys.${it.name}.d`)}</p>
              </div>
            ))}
          </div>
        </section>
      ))}

      <section className="pub-group">
        <h2 className="reveal">{t("public.integrations.groups.extend")}</h2>
        <p className="int-note reveal">{t("public.integrations.extendLead")}</p>
        <div className="landing-grid">
          {EXTEND.map((it, i) => (
            <div
              className="landing-feature reveal"
              style={{ transitionDelay: `${i * 0.06}s` }}
              key={it.k}
            >
              <div className="int-head">
                <span className="int-logo" aria-hidden="true">
                  <TargetIcon name={it.k} kind={it.kind} size={21} />
                </span>
              </div>
              <h3>{t(`public.integrations.ext.${it.k}.t`)}</h3>
              <p>{t(`public.integrations.ext.${it.k}.d`)}</p>
            </div>
          ))}
        </div>
      </section>

      <section className="pub-group">
        <div className="int-trust reveal">
          <h2>{t("public.integrations.brokerTitle")}</h2>
          <p>{t("public.integrations.brokerLead")}</p>
        </div>
      </section>

      <section className="pub-cta reveal">
        <h2>{t("public.integrations.ctaTitle")}</h2>
        <p>{t("public.integrations.ctaLead")}</p>
        <div className="pub-cta-row">
          <PubLink id="funktion" className="btn primary">{t("public.nav.function")}</PubLink>
          <PubLink id="anmelden" className="btn">{t("public.nav.login")}</PubLink>
        </div>
      </section>
    </div>
  );
}

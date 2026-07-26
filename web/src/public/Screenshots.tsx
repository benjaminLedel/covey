import { useState } from "react";
import { useTranslation } from "react-i18next";

/* Interaktive Screenshot-Galerie der echten Software: großes Vorschaubild in
   einem Browser-Rahmen + Thumbnails zum Umschalten. Bilder liegen unter
   web/public/shots/ (aus der laufenden Instanz aufgenommen). */

const SHOTS = [
  { img: "agents.jpg", k: "agents" },
  { img: "org.jpg", k: "org" },
  { img: "backlog.jpg", k: "backlog" },
  { img: "memory.jpg", k: "memory" },
  { img: "costs.jpg", k: "costs" },
] as const;

export default function Screenshots() {
  const { t } = useTranslation();
  const [sel, setSel] = useState(0);
  const cur = SHOTS[sel];

  return (
    <section className="landing-features pub-shots">
      <h2 className="reveal">{t("public.shots.title")}</h2>
      <p className="landing-lead reveal">{t("public.shots.lead")}</p>

      <div className="pub-shot-stage reveal">
        <figure className="pub-shot-frame">
          <div className="pub-shot-bar" aria-hidden="true"><span /><span /><span /></div>
          <img
            src={`/shots/${cur.img}`}
            alt={t(`public.shots.${cur.k}.t`)}
            loading="lazy"
          />
        </figure>
        <p className="pub-shot-cap">
          <strong>{t(`public.shots.${cur.k}.t`)}</strong> — {t(`public.shots.${cur.k}.d`)}
        </p>
      </div>

      <div className="pub-shot-thumbs" role="tablist">
        {SHOTS.map((s, i) => (
          <button
            key={s.k}
            role="tab"
            aria-selected={sel === i}
            className={`pub-shot-thumb ${sel === i ? "active" : ""}`}
            onClick={() => setSel(i)}
          >
            <img src={`/shots/${s.img}`} alt="" loading="lazy" />
            <span>{t(`public.shots.${s.k}.t`)}</span>
          </button>
        ))}
      </div>
    </section>
  );
}

import { useState } from "react";
import { useTranslation } from "react-i18next";
import Bild from "./Bild";

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
          <Bild
            src={`/shots/${cur.img}`}
            breiten={[320, 1280]}
            sizes="(max-width: 1000px) 100vw, 900px"
            alt={t(`public.shots.${cur.k}.t`)}
            breite={1280}
            hoehe={800}
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
            {/* Die Vorschau ist gut hundert Pixel breit — sie hat den
                Screenshot in voller Größe nie gebraucht. */}
            <Bild
              src={`/shots/${s.img}`}
              breiten={[320, 1280]}
              sizes="130px"
              alt=""
              breite={1280}
              hoehe={800}
            />
            <span>{t(`public.shots.${s.k}.t`)}</span>
          </button>
        ))}
      </div>
    </section>
  );
}

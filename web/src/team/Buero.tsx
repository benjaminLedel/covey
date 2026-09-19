import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import type { Agent, Department, Laufend } from "../api";
import Gesicht from "../components/Gesicht";

/* Das Büro: die Belegschaft als Grundriss.
 *
 * Eine Liste sagt, dass etwas läuft. Ein Raum zeigt es. Und covey hat für
 * diesen Raum bereits das Vokabular — ein Agent HAT einen Arbeitsplatz, er
 * gehört zu einer Abteilung, er schläft, wenn nichts anliegt, und er wartet
 * auf einen Menschen, wenn er nicht weiterkann. Das ist keine Verzierung,
 * sondern dieselbe Auskunft in einer Form, die man mit einem Blick liest
 * statt in vier Zeilen.
 *
 * Warum kein <canvas>: Die Gesichter gibt es schon als SVG-Komponente, mit
 * Zuständen, Animationen und Erscheinungsbild. Auf eine Leinwand gemalt wären
 * sie ein zweites Mal gebaut — und ein Klick auf einen Kollegen wäre
 * Mathematik statt eines Links. Mit Transformationen bewegt sich hier
 * genauso viel, und die Tastatur kommt überall hin.
 *
 * Wer wo steht:
 *
 *   am eigenen Platz     — schlafend (mit zzz) oder arbeitend
 *   am Tresen vorn       — wartet auf eine Entscheidung von Ihnen
 *   zwischen den Plätzen — wach, aber ohne Vorgang: sie laufen herum
 */

/** Ein Platz im Raster eines Abteilungsraums. */
type Platz = { x: number; y: number };

const PLATZ_B = 78;
const PLATZ_H = 84;
const RAUM_PAD = 16;
const SPALTEN = 4;

/* Eine kleine, stabile Streuung aus dem Kürzel: Wer herumläuft, soll nicht
   im Gleichschritt mit den anderen laufen. */
function streu(text: string, n: number) {
  let h = 2166136261;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return Math.abs(h) % n;
}

export default function Buero({
  agents,
  departments,
  laufend,
  wartetBei,
}: {
  agents: Agent[];
  departments: Department[];
  laufend: Laufend[];
  wartetBei: Set<string>;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();

  /* Die Räume: eine Abteilung, ein Raster. Die Reihenfolge ist die der
     Abteilungen, damit der Grundriss von Besuch zu Besuch derselbe bleibt —
     ein Büro, in dem die Zimmer wandern, ist kein Büro. */
  const raeume = useMemo(() => {
    const ohne = agents.filter((a) => !departments.some((d) => d.id === a.department_id));
    const gruppen = [
      ...departments
        .map((d) => ({ id: d.id, name: d.name, color: d.color, leute: agents.filter((a) => a.department_id === d.id) }))
        .filter((g) => g.leute.length > 0),
      ...(ohne.length > 0 ? [{ id: "", name: t("team.ohneAbteilung"), color: "", leute: ohne }] : []),
    ];
    return gruppen.map((g) => {
      const spalten = Math.min(SPALTEN, Math.max(1, g.leute.length));
      const zeilen = Math.ceil(g.leute.length / spalten);
      return {
        ...g,
        spalten,
        breite: spalten * PLATZ_B + RAUM_PAD * 2,
        hoehe: zeilen * PLATZ_H + RAUM_PAD * 2 + 22,
        plaetze: new Map<string, Platz>(
          g.leute.map((a, i) => [
            a.id,
            { x: RAUM_PAD + (i % spalten) * PLATZ_B, y: RAUM_PAD + 22 + Math.floor(i / spalten) * PLATZ_H },
          ]),
        ),
      };
    });
  }, [agents, departments, t]);

  /* Das Herumlaufen. Wer wach ist und keinen Vorgang hat, tritt alle paar
     Sekunden einen Schritt zur Seite — nicht mehr: Ein Büro, in dem alle
     ständig rennen, ist ein Bildschirmschoner. */
  const [takt, setTakt] = useState(0);
  const ruhig = useRef(false);
  useEffect(() => {
    ruhig.current = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
    if (ruhig.current) return;
    const id = setInterval(() => setTakt((n) => n + 1), 5200);
    return () => clearInterval(id);
  }, []);

  const laufendVon = new Map(laufend.map((l) => [l.agent_id, l]));

  return (
    <div className="bu">
      {/* Der Tresen: hier stehen die, die auf eine Entscheidung warten. Er
          liegt vorn, weil das die einzige Gruppe ist, die etwas von Ihnen
          will. */}
      <BuTresen
        leute={agents.filter((a) => wartetBei.has(a.id))}
        onOeffnen={(id) => navigate(`/team/${id}`)}
      />

      <div className="bu-flaeche">
        {raeume.map((raum) => (
          <section
            key={raum.id || "ohne"}
            className="bu-raum"
            style={{ width: raum.breite, height: raum.hoehe }}
            aria-label={raum.name}
          >
            <h3 className="bu-raum-name">
              {raum.color && <span className="bu-punkt" style={{ background: raum.color }} aria-hidden="true" />}
              {raum.name}
            </h3>

            {/* Die Plätze stehen fest und bleiben stehen, auch wenn niemand
                an ihnen sitzt. Ein leerer Schreibtisch ist eine Auskunft:
                der Kollege ist vorn am Tresen oder läuft herum. */}
            {raum.leute.map((a) => {
              const platz = raum.plaetze.get(a.id)!;
              const laeuft = laufendVon.get(a.id);
              return (
                <span
                  key={`p-${a.id}`}
                  className={`bu-platz${laeuft ? " belegt" : ""}`}
                  style={{ transform: `translate(${platz.x}px, ${platz.y}px)` }}
                  aria-hidden="true"
                />
              );
            })}

            {raum.leute.map((a) => {
              const platz = raum.plaetze.get(a.id)!;
              if (wartetBei.has(a.id)) return null; // steht vorn am Tresen
              const laeuft = laufendVon.get(a.id);
              const schlaeft = a.status === "sleeping";
              const tot = a.killed;
              /* Wer arbeitet oder schläft, sitzt still. Wer wach ist und
                 nichts zu tun hat, wandert um seinen Platz herum. */
              const wandert = !laeuft && !schlaeft && !tot && !ruhig.current;
              const dx = wandert ? [0, 15, -13, 8][(streu(a.slug, 4) + takt) % 4] : 0;
              const dy = wandert ? [0, -9, 11, -4][(streu(a.slug + "y", 4) + takt) % 4] : 0;
              return (
                <button
                  key={a.id}
                  className={`bu-wer${laeuft ? " arbeitet" : ""}${tot ? " gestoppt" : ""}${wandert ? " geht" : ""}`}
                  style={{ transform: `translate(${platz.x + dx}px, ${platz.y + dy}px)` }}
                  onClick={() => navigate(`/team/${a.id}`)}
                  title={laeuft ? `${a.display_name}: ${laeuft.title}` : a.display_name}
                >
                  <Gesicht
                    schluessel={a.slug}
                    zustand={tot ? "killed" : schlaeft ? "sleeping" : "working"}
                    groesse={30}
                  />
                  <span className="bu-name">{a.display_name.split(/\s+/)[0]}</span>
                </button>
              );
            })}
          </section>
        ))}
      </div>

      <p className="bu-legende">{t("team.bueroLegende")}</p>
    </div>
  );
}

/* Der Tresen. Eine eigene Reihe, weil „wartet auf Sie" die einzige Gruppe
   ist, die nicht nur Zustand, sondern Aufforderung ist. */
function BuTresen({ leute, onOeffnen }: { leute: Agent[]; onOeffnen: (id: string) => void }) {
  const { t } = useTranslation();
  if (leute.length === 0) return null;
  return (
    <div className="bu-tresen">
      <span className="bu-tresen-schild">{t("team.wartet")}</span>
      <div className="bu-tresen-leute">
        {leute.map((a) => (
          <button key={a.id} className="bu-wer am-tresen" onClick={() => onOeffnen(a.id)} title={a.display_name}>
            <Gesicht schluessel={a.slug} zustand="working" groesse={30} />
            <span className="bu-name">{a.display_name.split(/\s+/)[0]}</span>
          </button>
        ))}
      </div>
    </div>
  );
}

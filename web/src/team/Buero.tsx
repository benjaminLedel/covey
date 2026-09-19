import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type JSX } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { post, type Agent, type Department, type Laufend, type Principal } from "../api";
import { canManage } from "../pages/agent/roles";
import Gesicht from "../components/Gesicht";
import Dauer from "../components/Dauer";
import Riss, { MASS, type Art } from "./buero/risse";
import {
  AUSSEN,
  INNEN,
  PAD,
  PAD_OBEN,
  PX_JE_METER,
  SCHILD_H,
  SCHWUNG,
  SITZ_B,
  SITZ_H,
  TUER_B,
  ausstattungFuer,
  bauplan,
  podBreite,
  streu,
  type Gruppe,
  type Plan,
  type Punkt,
  type Raum,
} from "./buero/plan";
import { erschaffeLeben, type Leben, type Zustand } from "./buero/leben";

/* Das Büro: die Belegschaft als Grundriss.
 *
 * Eine Liste sagt, dass etwas läuft. Ein Raum zeigt es. Und covey hat für
 * diesen Raum bereits das Vokabular — ein Agent HAT einen Arbeitsplatz, er
 * gehört zu einer Abteilung, er schläft, wenn nichts anliegt, und er wartet
 * auf einen Menschen, wenn er nicht weiterkann. Das ist keine Verzierung,
 * sondern dieselbe Auskunft in einer Form, die man mit einem Blick liest
 * statt in vier Zeilen.
 *
 * DER PLAN WIRD GERECHNET (buero/plan.ts). Es gibt keine Vorlage: Wie viele
 * Plätze ein Zimmer nebeneinander hat, wie tief es wird, wie viele Zimmer in
 * eine Zeile passen und ob es einen zweiten Flur braucht, folgt aus der
 * Kopfzahl. Acht Kollegen ergeben ein Haus mit einem Flur, hundertvierzig
 * eines mit dreien und einem Quergang, der sie verbindet.
 *
 * DIE WAND IST DIE MASSE. Die ganze Fläche ist Mauerwerk, und was Raum ist,
 * wird ausgespart. Umgekehrt — helle Kästen mit einem Rahmen — treffen zwei
 * Rahmen nebeneinander als doppelte Linie aufeinander, die es in keinem Haus
 * gibt, und aus dem Grundriss wird ein Diagramm.
 *
 * DAS LICHT IST DER ZUSTAND. Der Bildschirm am Platz ist dunkel, solange
 * nichts läuft, und hell, sobald etwas läuft; sein Schein fällt auf Tisch und
 * Boden, und ein Zimmer mit einem hellen Tisch darin ist ein helles Zimmer.
 * Damit beantwortet ein Blick über die Seite die Frage, für die man vorher
 * vierzig Tische einzeln lesen musste.
 *
 * WAS AUSKUNFT TRÄGT und was nur Atmosphäre ist, steht in buero/leben.ts —
 * und die Trennung ist nicht Geschmack, sondern Bedingung. Eine Bewegung, aus
 * der sich etwas ablesen ließe, das in keiner Aufzeichnung steht, wäre eine
 * Lüge mit Charme.
 *
 * Warum kein <canvas>: Die Gesichter gibt es schon als SVG-Komponente, mit
 * Zuständen, Animationen und Erscheinungsbild. Auf eine Leinwand gemalt wären
 * sie ein zweites Mal gebaut, ein Klick auf einen Kollegen wäre Mathematik
 * statt eines Knopfes, und die Tastatur käme nirgends hin. Bewegt wird
 * ausschließlich mit `transform`, ein Schreibzugriff je Figur und Bild.
 */

/** Wie weit die Augen ausschlagen, im Raster des Gesichts (24 breit). */
const BLICK_WEITE = 1.25;

export default function Buero({
  agents,
  departments,
  laufend,
  wartetBei,
  me,
}: {
  agents: Agent[];
  departments: Department[];
  laufend: Laufend[];
  wartetBei: Set<string>;
  me: Principal;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();

  const ruhig = useMemo(() => window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false, []);

  /* Der Bau hängt an dem, was ihn WIRKLICH verändert: wer hier arbeitet, in
     welcher Abteilung, und wie breit das Fenster ist. Nicht daran, ob die
     Schale gerade ein neues Array gebaut hat.
     
     Das ist kein Feinschliff, sondern der Unterschied zwischen einem Büro und
     einem Flackern: Die Startseite fragt alle zehn Sekunden nach laufenden
     Vorgängen und rendert dabei neu, und die Liste der Kollegen ist bei jedem
     Rendern ein anderes Array. An dessen Identität gehängt, wurde der
     Grundriss alle zehn Sekunden neu gebaut — mit ihm das Leben darin. Jede
     Figur sprang an ihren Platz zurück, und eine Pflanze, die auf Wasser
     wartete, hatte ihren Durst vergessen, bevor jemand bei ihr ankam. */
  const kern = agents.map((a) => `${a.id}:${a.department_id ?? ""}:${a.slug}`).join("|");
  const abtKern = departments.map((d) => `${d.id}:${d.name}:${d.color}`).join("|");
  const ohneName = t("team.ohneAbteilung");
  const gruppen = useMemo<Gruppe[]>(() => {
    const ohne = agents.filter((a) => !departments.some((d) => d.id === a.department_id));
    return [
      ...departments
        .map((d) => ({ id: d.id, name: d.name, farbe: d.color, leute: agents.filter((a) => a.department_id === d.id) }))
        .filter((g) => g.leute.length > 0),
      ...(ohne.length > 0 ? [{ id: "ohne", name: ohneName, farbe: "", leute: ohne }] : []),
    ];
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kern, abtKern, ohneName]);

  /* Die Breite gibt das Fenster vor, und der Plan richtet sich danach. Ohne
     das Messen stünde hier eine geratene Zahl, und bei jedem zweiten
     Bildschirm ragte der Bau heraus oder ließe eine Spalte leer. */
  const huelle = useRef<HTMLDivElement>(null);
  const [breite, setBreite] = useState(1024);
  useLayoutEffect(() => {
    const messen = () => {
      const w = huelle.current?.clientWidth;
      if (w && Math.abs(w - breite) > 8) setBreite(w);
    };
    messen();
    const beobachter = new ResizeObserver(messen);
    if (huelle.current) beobachter.observe(huelle.current);
    return () => beobachter.disconnect();
  }, [breite]);

  const besprechungName = t("team.raumBesprechung");
  const teekuecheName = t("team.raumTeekueche");
  const plan = useMemo(
    () => bauplan(gruppen, breite, { besprechung: besprechungName, teekueche: teekuecheName }),
    [gruppen, breite, besprechungName, teekuecheName],
  );

  /* Zustand aus den Daten. „arbeitet" heißt: hat einen laufenden Vorgang —
     der Status allein sagte nur, dass der Agent wach ist, und ein wacher
     Agent ohne Aufgabe ist keine Auskunft, sondern ein Zustand. */
  const laufendVon = useMemo(() => new Map(laufend.map((l) => [l.agent_id, l])), [laufend]);
  const zustandVon = useCallback(
    (a: Agent): Zustand =>
      a.killed
        ? "gestoppt"
        : wartetBei.has(a.id)
          ? "wartet"
          : laufendVon.has(a.id)
            ? "arbeitet"
            : a.status === "sleeping"
              ? "schlaeft"
              : "frei",
    [laufendVon, wartetBei],
  );

  const [gewaehlt, setGewaehlt] = useState<string | null>(null);
  const [karteAn, setKarteAn] = useState<Punkt | null>(null);
  const [tassen, setTassen] = useState<ReadonlySet<string>>(new Set());
  const [gegossen, setGegossen] = useState<ReadonlySet<string>>(new Set());
  const [gaesteTakt, setGaesteTakt] = useState(0);
  const [aufmerksam, setAufmerksam] = useState(false);

  /* ── Das Leben ──────────────────────────────────────────────────────────
     Die Simulation lebt in einem Ref, nicht im Zustand: Siebzig Figuren je
     Bild durch React zu schicken hieße siebzig Abgleiche je Bild. React
     zeichnet den Bau und die Knöpfe; bewegt wird über `transform`. */
  const leben = useRef<Leben | null>(null);
  const knoepfe = useRef(new Map<string, HTMLButtonElement>());
  const blicke = useRef(new Map<string, SVGGElement>());
  const gastRefs = useRef(new Map<number, HTMLElement>());

  const haken = useMemo(
    () => ({
      tasse: (id: string, da: boolean) =>
        setTassen((alt) => {
          const neu = new Set(alt);
          if (da) neu.add(id);
          else neu.delete(id);
          return neu;
        }),
      gegossen: (p: string) => setGegossen((alt) => new Set(alt).add(p)),
      gaeste: () => setGaesteTakt((n) => n + 1),
      durst: () => setGaesteTakt((n) => n + 1),
      aufmerksam: () => {
        leben.current?.hinsehen();
        setAufmerksam(true);
        window.setTimeout(() => setAufmerksam(false), 2600);
      },
    }),
    [],
  );

  /* Der Plan kann sich ändern (neue Kollegen, andere Breite). Dann wird das
     Leben neu aufgesetzt — die Geometrie darunter ist eine andere. */
  const planRef = useRef<Plan | null>(null);
  if (planRef.current !== plan) {
    planRef.current = plan;
    leben.current = erschaffeLeben(plan, haken);
  }
  useEffect(() => setGaesteTakt((n) => n + 1), [plan]);

  /* Zustände übernehmen, Positionen behalten. */
  useEffect(() => {
    const stand = plan.raeume.flatMap((r, ri) =>
      r.leute.map((a, i) => ({ id: a.id, slug: a.slug, ri, i, sitz: r.sitze[i], zustand: zustandVon(a) })),
    );
    leben.current?.uebernehmen(stand);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [plan, zustandVon, kern]);

  /* Der Taktgeber. Bei „weniger Bewegung" läuft er gar nicht: Dann sitzt
     jeder an seinem Platz, das Licht bleibt an — die Auskunft bleibt, nur
     die Bewegung geht. */
  useEffect(() => {
    if (ruhig) return;
    let laeuft = true;
    let letzte = 0;
    const bild = (t0: number) => {
      if (!laeuft) return;
      const dt = Math.min(64, t0 - letzte) / 1000;
      letzte = t0;
      const l = leben.current;
      if (l) {
        l.tick(dt);
        for (const f of l.figuren.values()) {
          const el = knoepfe.current.get(f.id);
          if (!el) continue;
          const bob = f.geht ? Math.sin(f.phase * 0.16) * 1.6 : 0;
          el.style.transform = `translate3d(${f.pos.x}px, ${f.pos.y + bob}px, 0)`;
          el.classList.toggle("geht", f.geht);
          const b = blicke.current.get(f.id);
          if (b) b.style.transform = `translate(${f.blick.x * BLICK_WEITE}px, ${f.blick.y * BLICK_WEITE}px)`;
        }
        for (const g of l.gast()) {
          if (g.art !== "katze") continue;
          const el = gastRefs.current.get(g.id);
          if (el) el.style.transform = `translate3d(${g.pos.x}px, ${g.pos.y}px, 0)`;
        }
      }
      requestAnimationFrame(bild);
    };
    const id = requestAnimationFrame((t0) => {
      letzte = t0;
      requestAnimationFrame(bild);
    });
    return () => {
      laeuft = false;
      cancelAnimationFrame(id);
    };
  }, [ruhig, plan]);

  /* Der Zeiger, entkoppelt vom Ereignis: Mausbewegungen kommen häufiger als
     Bilder, und jede davon zu verarbeiten hieße, mehrfach je Bild zu rechnen. */
  const gemeldet = useRef(false);
  const aufZeiger = (e: React.PointerEvent<HTMLDivElement>) => {
    if (ruhig || gemeldet.current) return;
    const r = e.currentTarget.getBoundingClientRect();
    const p = { x: e.clientX - r.left, y: e.clientY - r.top };
    gemeldet.current = true;
    requestAnimationFrame(() => {
      gemeldet.current = false;
      leben.current?.zeiger(p);
    });
  };

  useEffect(() => {
    if (!gewaehlt) return;
    const zu = (e: KeyboardEvent) => {
      if (e.key === "Escape") setGewaehlt(null);
    };
    window.addEventListener("keydown", zu);
    return () => window.removeEventListener("keydown", zu);
  }, [gewaehlt]);

  useEffect(() => {
    leben.current?.waehlen(gewaehlt);
  }, [gewaehlt]);

  /* Wer „covey" tippt, bekommt die ganze Belegschaft zu Gesicht. Die Augen
     können das ohnehin — sie folgen sonst dem Zeiger; hier bekommen alle für
     einen Moment dieselbe Richtung. */
  useEffect(() => {
    if (ruhig) return;
    let getippt = "";
    const horchen = (e: KeyboardEvent) => {
      const z = e.target as HTMLElement | null;
      if (e.key.length !== 1 || (z && /^(INPUT|TEXTAREA)$/.test(z.tagName)) || z?.isContentEditable) return;
      getippt = (getippt + e.key.toLowerCase()).slice(-5);
      if (getippt !== "covey") return;
      getippt = "";
      leben.current?.ausloesen("aufmerksam");
    };
    window.addEventListener("keydown", horchen);
    return () => window.removeEventListener("keydown", horchen);
  }, [ruhig]);

  const gewaehlterAgent = agents.find((a) => a.id === gewaehlt);
  const anwaehlen = (a: Agent) => {
    if (gewaehlt === a.id) {
      setGewaehlt(null);
      return;
    }
    const f = leben.current?.figuren.get(a.id);
    setKarteAn(f ? { ...f.pos } : null);
    setGewaehlt(a.id);
  };

  void gaesteTakt; // die Gästeliste ist ein Ref; der Takt löst das Neuzeichnen aus
  const gaeste = leben.current?.gast() ?? [];

  if (!plan.raeume.some((r) => r.leute.length)) return null;

  return (
    <div className="bu" ref={huelle}>
      <div className="bu-rahmen">
        <div
          className={`bu-bau${ruhig ? " ruht" : ""}${aufmerksam ? " aufmerksam" : ""}`}
          style={{ width: plan.breite, height: plan.hoehe }}
          onPointerMove={aufZeiger}
          onPointerLeave={() => leben.current?.zeiger(null)}
          /* Ein Klick auf den Boden schließt die Karte. Die Karte selbst hält
             ihn auf — sonst schlösse sie sich, während man in ihr Feld tippt. */
          onPointerDown={() => setGewaehlt(null)}
        >
          <Boeden plan={plan} laufendVon={laufendVon} />
          <Zeichnung plan={plan} />
          <Ausstattungen
            plan={plan}
            gegossen={gegossen}
            durstig={(id) => !!leben.current?.istDurstig(id)}
            onGiessen={(id, p) => leben.current?.giessen(id, p)}
          />
          <Arbeitsplaetze plan={plan} laufendVon={laufendVon} tassen={tassen} />

          {plan.raeume.map((r) =>
            r.leute.map((a, i) => {
              const laeuft = laufendVon.get(a.id);
              const zustand = zustandVon(a);
              return (
                <button
                  key={a.id}
                  ref={(el) => {
                    if (el) knoepfe.current.set(a.id, el);
                    else knoepfe.current.delete(a.id);
                  }}
                  data-wer={a.id}
                  className={`bu-wer${laeuft ? " arbeitet" : ""}${zustand === "gestoppt" ? " gestoppt" : ""}${gewaehlt === a.id ? " gewaehlt" : ""}`}
                  style={{ transform: `translate3d(${r.sitze[i].x}px, ${r.sitze[i].y}px, 0)` }}
                  onClick={() => anwaehlen(a)}
                  onPointerDown={(e) => e.stopPropagation()}
                  aria-expanded={gewaehlt === a.id}
                  title={laeuft ? `${a.display_name}: ${laeuft.title}` : a.display_name}
                >
                  {/* Die Gedankenblase: der zuletzt aufgezeichnete Schritt,
                      über dem Kopf dessen, der ihn gerade tut. Der Schlüssel
                      ist der Schritt selbst — so läuft das Aufziehen erneut,
                      wenn er sich ändert, und der Wechsel ist zu sehen statt
                      nur zu lesen. */}
                  {laeuft?.step && (
                    <span key={laeuft.step} className="bu-denkt">
                      {t(`team.schritt.${laeuft.step}`, laeuft.step)}
                    </span>
                  )}
                  {zustand === "wartet" && !laeuft?.step && <span className="bu-denkt warten">{t("team.wartet")}</span>}
                  <span className="bu-kopf">
                    <Gesicht
                      schluessel={a.slug}
                      zustand={zustand === "gestoppt" ? "killed" : zustand === "schlaeft" ? "sleeping" : "working"}
                      groesse={30}
                      blickRef={(g) => {
                        if (g) blicke.current.set(a.id, g);
                        else blicke.current.delete(a.id);
                      }}
                    />
                  </span>
                  <span className="bu-name">{a.display_name.split(/\s+/)[0]}</span>
                </button>
              );
            }),
          )}

          {/* Was selten vorbeikommt. Nichts davon trägt Auskunft. */}
          {gaeste.map((g) => {
            if (g.art === "katze")
              return (
                <span
                  key={g.id}
                  className="bu-katze"
                  ref={(el) => {
                    if (el) gastRefs.current.set(g.id, el);
                    else gastRefs.current.delete(g.id);
                  }}
                  style={{ transform: `translate3d(${g.pos.x}px, ${g.pos.y}px, 0)` }}
                >
                  <Riss art="katze" x={0} y={0} w={22} h={26} />
                </span>
              );
            if (g.art === "flieger")
              return (
                <span key={g.id} className="bu-flieger" style={{ left: g.pos.x, top: g.pos.y, ["--weit" as string]: `${g.weit}px` }}>
                  <Riss art="flieger" x={0} y={0} w={22} h={12} />
                </span>
              );
            const masse = g.art === "blatt" ? [9, 12] : g.art === "kuchen" ? [22, 20] : [13, 12];
            return <Riss key={g.id} art={g.art} x={g.pos.x} y={g.pos.y} w={masse[0]} h={masse[1]} className={`bu-gast g-${g.art}`} />;
          })}

          {gewaehlterAgent && karteAn && (
            <BuKarte
              agent={gewaehlterAgent}
              laeuft={laufendVon.get(gewaehlterAgent.id)}
              wartet={wartetBei.has(gewaehlterAgent.id)}
              am={karteAn}
              breite={plan.breite}
              me={me}
              onOeffnen={() => navigate(`/team/${gewaehlterAgent.id}`)}
              onSchliessen={() => setGewaehlt(null)}
            />
          )}
        </div>
      </div>
      <p className="bu-legende">{t("team.bueroLegende")}</p>
    </div>
  );
}

/* ── Die Böden ─────────────────────────────────────────────────────────────
   Die Fläche darunter ist Mauerwerk; hier wird ausgespart. Der Flur ist eine
   Spur dunkler als die Zimmer — er wird begangen. */
function Boeden({ plan, laufendVon }: { plan: Plan; laufendVon: Map<string, Laufend> }) {
  return (
    <>
      <div className="bu-boden flur" style={{ left: plan.quer.x, top: AUSSEN, width: plan.quer.w, height: plan.hoehe - AUSSEN * 2 }} />
      {plan.flure.map((f, i) => (
        <div key={i} className="bu-boden flur" style={{ left: plan.quer.x, top: f.y, width: plan.breite - plan.quer.x - AUSSEN, height: f.h }} />
      ))}
      {plan.raeume.map((r) => {
        const arbeitet = r.leute.some((a) => laufendVon.has(a.id));
        const leer = !arbeitet && r.leute.length > 0 && r.leute.every((a) => a.killed || a.status === "sleeping");
        return (
          <div
            key={r.id}
            className={`bu-boden${r.gem ? " gemein" : ""}${arbeitet ? " hell" : ""}${leer ? " leer" : ""}`}
            style={{ left: r.x, top: r.y, width: r.w, height: r.h }}
            aria-label={r.name}
          />
        );
      })}
      {/* Die Schwellen: an dieser Stelle ist die Wand nicht da. */}
      {plan.raeume.map((r) => (
        <div
          key={`s-${r.id}`}
          className="bu-schwelle"
          style={{ left: r.tuerX - TUER_B / 2, top: r.obenDrueber ? r.wand : r.wand - INNEN, width: TUER_B, height: INNEN }}
        />
      ))}
      <div className="bu-schwelle" style={{ left: 0, top: plan.eingang.y, width: AUSSEN, height: plan.eingang.hoehe }} />
      {plan.fenster.map((f, i) => (
        <span key={i}>
          <div className="bu-schwelle" style={{ left: f.x, top: 0, width: f.w, height: AUSSEN }} />
          <div className="bu-schwelle" style={{ left: f.x + 24, top: plan.hoehe - AUSSEN, width: f.w * 0.72, height: AUSSEN }} />
        </span>
      ))}
    </>
  );
}

/* ── Der Riss ──────────────────────────────────────────────────────────────
   Türschwünge, Brüstungen, Möbelkanten — die gezeichneten Linien über dem
   Bau. Der Türschwung ist das Zeichen, an dem ein Grundriss als Grundriss
   gelesen wird, und er sagt nebenbei, wohin die Tür aufgeht. */
function Zeichnung({ plan }: { plan: Plan }) {
  const { t } = useTranslation();
  const ey = plan.eingang.y;
  const eb = plan.eingang.hoehe - 4;
  return (
    <>
      <svg className="bu-riss" viewBox={`0 0 ${plan.breite} ${plan.hoehe}`} aria-hidden="true">
        {plan.raeume.map((r) => {
          const x = r.tuerX - TUER_B / 2;
          const s = SCHWUNG * r.ri;
          return (
            <g key={`t-${r.id}`}>
              <path className="blatt" d={`M${x} ${r.wand} v${s}`} />
              <path d={`M${x} ${r.wand + s} A${SCHWUNG} ${SCHWUNG} 0 0 ${r.obenDrueber ? 0 : 1} ${x + SCHWUNG} ${r.wand}`} />
            </g>
          );
        })}
        {/* Der Eingang, mit eigenem Schwung nach innen. */}
        <path className="blatt" d={`M${AUSSEN} ${ey} v${eb}`} />
        <path d={`M${AUSSEN} ${ey + eb} A${eb} ${eb} 0 0 0 ${AUSSEN + eb} ${ey}`} />
        {/* Fenster: eine Brüstungslinie im Mauerwerk, keine aufgemalte Scheibe. */}
        {plan.fenster.map((f, i) => (
          <g key={i}>
            <path className="fenster" d={`M${f.x} ${AUSSEN / 2} h${f.w}`} />
            <path className="fenster" d={`M${f.x + 24} ${plan.hoehe - AUSSEN / 2} h${f.w * 0.72}`} />
          </g>
        ))}
        {/* Der Tresen, und die Bänke, die sich zwei Plätze teilen. */}
        <path className="moebel" d={`M${plan.quer.x + 14} ${plan.tresen.y} h${plan.quer.w - 28}`} />
        {/* Die Schreibtische. Eine gefüllte Platte allein war auf dem hellen
            Boden kaum zu erkennen — in einem Plan wird Mobiliar umrissen.
            Dazu der Bildschirm am hinteren Rand und die Tastatur davor: die
            zwei Striche, an denen ein Rechteck zu einem Arbeitsplatz wird.
            Und die gemeinsame Rückkante, wo zwei Plätze eine Bank bilden. */}
        {plan.raeume.map((r) =>
          r.sitze.map((p, i) => {
            const x = p.x - SITZ_B / 2 + 7;
            const y = p.y + 18;
            const w = SITZ_B - 14;
            return (
              <g key={`${r.id}-${i}`} className="moebel">
                <path d={`M${x + 0.5} ${y + 0.5}h${w - 1}v20h${-(w - 1)}Z`} />
                <path className="tastatur" d={`M${x + 12} ${y + 15}h${w - 24}`} />
                {i % 2 === 1 && <path d={`M${x} ${y} v21`} />}
              </g>
            );
          }),
        )}
        {plan.raeume
          .filter((r) => r.gem === "besprechung")
          .map((r) => <path key="wb" className="moebel" d={`M${r.x + 24} ${r.y + SCHILD_H + 6} h${Math.min(90, r.w - 48)}`} />)}
      </svg>

      <span className="bu-schild eingang" style={{ left: AUSSEN + 12, top: ey + plan.eingang.hoehe + 8 }}>
        {t("team.raumEingang")}
      </span>
      <span className="bu-schild mitte" style={{ left: plan.quer.mitte, top: plan.tresen.y - 17 }}>
        {t("team.raumTresen")}
      </span>
      {plan.raeume.map((r) => (
        <span key={`n-${r.id}`}>
          {/* Der Name bekommt genau die Breite, die ihm neben der Nummer
              bleibt — sonst schreibt eine lange Abteilung sie zu, und der
              Plan verliert die eine Angabe, die ihn als Plan ausweist. */}
          <h3
            className={`bu-schild${r.gem ? " mitte" : ""}`}
            style={
              r.gem
                ? { left: r.x + r.w / 2, top: r.y + 8, maxWidth: r.w - 24 }
                : { left: r.x + PAD, top: r.y + 6, maxWidth: r.w - PAD * 2 }
            }
          >
            {r.farbe && <span className="bu-punkt" style={{ background: r.farbe }} aria-hidden="true" />}
            {r.name}
          </h3>
          {!r.gem && (
            /* Nummer und Fläche UNTER dem Namen, wie auf einem gezeichneten
               Plan. Der Maßstab steht in plan.ts: 38 Pixel je Meter. */
            <span className="bu-nummer" style={{ left: r.x + PAD + 11, top: r.y + 19 }}>
              {r.flur + 1}.{String(r.nr).padStart(2, "0")} · {Math.round((r.w * r.h) / (PX_JE_METER * PX_JE_METER))} m²
            </span>
          )}
        </span>
      ))}
    </>
  );
}

type TopfBauer = (id: string, x: number, y: number, art: Art) => JSX.Element;

/* ── Die Einrichtung ───────────────────────────────────────────────────────
   Was in einem Zimmer steht, außer Tischen. Aus dem Namen der Abteilung
   gerechnet — dasselbe Zimmer, dieselben Möbel, jedes Mal (plan.ts,
   `ausstattungFuer`). Nichts davon trägt Auskunft. */
function Ausstattungen({
  plan,
  gegossen,
  durstig,
  onGiessen,
}: {
  plan: Plan;
  gegossen: ReadonlySet<string>;
  durstig: (id: string) => boolean;
  onGiessen: (id: string, p: Punkt) => void;
}) {
  const { t } = useTranslation();
  const topf: TopfBauer = (id, x, y, art) => {
    const [w, h] = MASS[art]!;
    return (
      <span key={id} className={`bu-topf${gegossen.has(id) ? " blueht" : ""}${durstig(id) ? " durstig" : ""}`}>
        <Riss art={art} x={x} y={y} w={w} h={h} onClick={() => onGiessen(id, { x: x + w / 2, y: y + h })} titel={t("team.giessen")} />
        {gegossen.has(id) && <span className="bu-bluete" style={{ left: x + w / 2 - 3, top: y + h * 0.2 }} aria-hidden="true" />}
      </span>
    );
  };

  return (
    <>
      {/* Empfang: Garderobe, Tresen, Bank, eine große Pflanze. Ein Vorraum, in
          dem nichts steht, ist kein Vorraum, sondern eine Lücke im Plan. */}
      <Riss art="garderobe" x={plan.quer.x + 16} y={plan.eingang.y - 22} w={plan.quer.w - 32} h={7} />
      <div className="bu-tisch" style={{ left: plan.quer.x + 14, top: plan.tresen.y, width: plan.quer.w - 28, height: 12 }} />
      <div className="bu-tisch" style={{ left: plan.quer.x + 22, top: plan.tresen.y + 206, width: plan.quer.w - 44, height: 11 }} />
      {topf("empfang", plan.quer.mitte - 13, plan.hoehe - AUSSEN - 44, "monstera")}

      {/* Im Flur: Drucker und Wasserspender, an der Wand, wo man vorbeigeht. */}
      {plan.flure.map((f, i) => (
        <span key={i}>
          <Riss art="drucker" x={plan.quer.x + plan.quer.w + 70} y={f.y + f.h - 22} w={22} h={17} />
          <Riss art="spender" x={plan.breite - AUSSEN - 46} y={f.y + 5} w={15} h={18} />
          <Riss art="feuerloescher" x={plan.quer.x + plan.quer.w + 24} y={f.y + 4} />
          {i % 2 === 0 && topf(`flur-${i}`, plan.breite - AUSSEN - 96, f.y + f.h - 32, "monstera")}
        </span>
      ))}

      {plan.raeume.map((r) => (r.gem ? <Gemeinschaft key={r.id} raum={r} topf={topf} /> : <Arbeitszimmer key={r.id} raum={r} topf={topf} />))}
    </>
  );
}

/* Die Einrichtung eines Zimmers trägt den Ton seiner Abteilung — denselben,
   der als Punkt neben dem Namen steht. Das ist der eine Ort, an dem Farbe im
   Grundriss etwas bindet, statt etwas zu behaupten: Möbel gehören zu einem
   Zimmer, und das Zimmer gehört zu einer Abteilung. Gemischt wird zum Ton der
   Zeichnung hin, damit es ein Plan bleibt und kein Bilderbuch; ohne gesetzte
   Abteilungsfarbe bleibt alles grau. */
function Arbeitszimmer({ raum: r, topf }: { raum: Raum; topf: TopfBauer }) {
  const h = streu(r.name);
  const e = ausstattungFuer(r.name);
  const stuecke: JSX.Element[] = [];

  /* An der Wand gegenüber der Tür — die einzige, an der etwas hängen kann,
     ohne im Weg zu stehen. Und nur, was FLACH ist: In der Draufsicht hängt
     ein Bild sechs Pixel tief an der Wand, ein Serverschrank steht dreißig
     tief auf dem Boden. Der stand vorher mitten in der ersten Reihe. */
  const wandY = r.obenDrueber ? r.y + SCHILD_H + 2 : r.y + r.h - 3;
  const wandPlatz = PAD_OBEN - 6;
  const stehend: typeof e.wand = [];
  const haengend = e.wand.filter((art) => (MASS[art]![1] <= wandPlatz ? true : (stehend.push(art), false)));
  haengend.forEach((art, i) => {
    const [w, hh] = MASS[art]!;
    const x = r.x + 24 + (i * (r.w - 66)) / Math.max(1, haengend.length);
    if (x + w > r.x + r.w - 12) return;
    stuecke.push(<Riss key={`w${i}`} art={art} x={x} y={r.obenDrueber ? wandY : wandY - hh} w={w} h={hh} />);
  });

  /* In der Lücke, die die letzte Reihe ohnehin lässt: die Sitzecke. Das ist
     der Platz, den der Grundriss schon hat — Ausstattung, die keinen neuen
     Raum kostet. */
  const rest = r.leute.length % r.spalten;
  if (rest !== 0 && r.sitze.length) {
    const p = r.sitze[r.sitze.length - 1];
    const frei = (r.spalten - rest) * SITZ_B;
    const mx = p.x + SITZ_B / 2 + frei / 2;
    if (frei >= 130) {
      const [sw, sh] = MASS[e.ecke]!;
      stuecke.push(<Riss key="tep" art="teppich" x={mx - 35} y={p.y - 8} w={70} h={46} />);
      stuecke.push(<Riss key="eck" art={e.ecke} x={mx - sw / 2} y={p.y + 2} w={sw} h={sh} />);
      stuecke.push(topf(`${r.id}-ecke`, mx + 26, p.y - 4, "monstera"));
      stuecke.push(<Riss key="neb" art={e.neben} x={mx - 42} y={p.y - 2} />);
    } else {
      stuecke.push(topf(`${r.id}-luecke`, mx - 11, p.y - 6, "pflanze"));
    }
  }
  /* Und wenn das Zimmer tiefer ist, als seine Reihen brauchen: Alle Zimmer
     eines Bandes sind gleich tief, also hat die Abteilung mit einem Kollegen
     so viel Boden wie die mit dreizehn. Dieser Rest bleibt nicht leer — er
     wird die Sitzecke. Ein Zimmer, in dem nichts steht, sieht aus, als fehle
     etwas, und genau das soll es nicht. */
  const unten = r.y + SCHILD_H + PAD_OBEN + Math.max(0, r.zeilen - 1) * SITZ_H + 32 + 46;
  const rest2 = r.y + r.h - unten;
  if (rest2 > 76 && r.w > 150) {
    const [sw, sh] = MASS[e.ecke]!;
    const mx = r.x + r.w / 2;
    const my = unten + 10;
    stuecke.push(<Riss key="tep2" art="teppich" x={mx - 35} y={my} w={70} h={46} />);
    stuecke.push(<Riss key="eck2" art={e.ecke} x={mx - sw / 2} y={my + 10} w={sw} h={sh} />);
    stuecke.push(topf(`${r.id}-lounge`, mx + 44, my + 6, "monstera"));
    stuecke.push(<Riss key="neb2" art={e.neben} x={mx - 58} y={my + 8} />);
  } else if ((h >> 3) % 3 !== 0 && rest2 > 40) {
    stuecke.push(topf(`${r.id}-ecke2`, h % 2 ? r.x + r.w - 34 : r.x + 12, r.y + r.h - 36, "monstera"));
  }
  /* Der Papierkorb in die freie Ecke, nicht in die erste Reihe. */
  stuecke.push(<Riss key="korb" art="papierkorb" x={r.x + 14} y={r.y + r.h - 22} w={10} h={10} />);
  /* Was auf dem Boden steht statt an der Wand zu hängen, kommt in den
     Seitenstreifen, den das mittig stehende Raster ohnehin frei lässt. */
  const seite = (r.w - podBreite(r.spalten)) / 2;
  stehend.forEach((art, i) => {
    const [w, hh] = MASS[art]!;
    if (seite < w + 10) return;
    stuecke.push(
      <Riss
        key={`st${i}`}
        art={art}
        x={i % 2 ? r.x + r.w - seite / 2 - w / 2 : r.x + seite / 2 - w / 2}
        y={r.y + SCHILD_H + PAD_OBEN + 6 + i * (hh + 8)}
        w={w}
        h={hh}
      />,
    );
  });

  /* Auf den Tischen: Mappen überall, dazu das, was zur Abteilung gehört. */
  r.leute.forEach((a, i) => {
    const hs = streu(a.slug + "#");
    const p = r.sitze[i];
    if (hs % 3 === 0) stuecke.push(<Riss key={`m${i}`} art="mappe" x={p.x - SITZ_B / 2 + 12} y={p.y + 22} w={13} h={10} />);
    if (e.tisch && (hs >> 2) % 5 < 3) {
      const [w, hh] = MASS[e.tisch]!;
      stuecke.push(<Riss key={`d${i}`} art={e.tisch} x={p.x + SITZ_B / 2 - w - 12} y={p.y + (e.tisch === "lampe" ? 12 : 24)} w={w} h={hh} />);
    }
  });
  return (
    <span className="bu-einrichtung" style={r.farbe ? ({ ["--bu-ton" as string]: r.farbe } as React.CSSProperties) : undefined}>
      {stuecke}
    </span>
  );
}

function Gemeinschaft({ raum: r, topf }: { raum: Raum; topf: TopfBauer }) {
  const cx = r.x + r.w / 2;
  const cy = r.y + SCHILD_H + (r.h - SCHILD_H) / 2;
  if (r.gem === "besprechung") {
    const tw = Math.min(r.w - PAD * 4, 118);
    const th = 44;
    return (
      <>
        <div className="bu-tisch rund" style={{ left: cx - tw / 2, top: cy - th / 2, width: tw, height: th }} />
        {[0, 1, 2].map((i) =>
          [-1, 1].map((s) => (
            <div
              key={`${i}${s}`}
              className="bu-stuhl"
              style={{ left: cx - tw / 2 + 22 + (i * (tw - 44)) / 2 - 7, top: cy + s * (th / 2 + 7) - 2 }}
            />
          )),
        )}
        <Riss art="flipchart" x={r.x + 16} y={r.y + r.h - 40} />
        {topf(`${r.id}-p`, r.x + r.w - 34, r.y + r.h - 36, "monstera")}
      </>
    );
  }
  return (
    <>
      <div className="bu-tisch" style={{ left: r.x + PAD, top: r.y + SCHILD_H + 8, width: r.w - PAD * 2, height: 16 }} />
      <Riss art="kaffeemaschine" x={r.x + PAD + 8} y={r.y + SCHILD_H + 10} />
      <div className="bu-tisch rund" style={{ left: cx - 21, top: cy + 8, width: 42, height: 28 }} />
      {[-1, 1].map((s) => (
        <div key={s} className="bu-stuhl" style={{ left: cx - 7 + s * 29, top: cy + 19 }} />
      ))}
      {topf(`${r.id}-p`, r.x + r.w - 34, r.y + r.h - 36, "monstera")}
    </>
  );
}

/* ── Die Arbeitsplätze ─────────────────────────────────────────────────────
   Der Schreibtisch mit dem Bildschirm darauf. Der Bildschirm ist der Träger
   des Zustands: dunkel, solange nichts läuft; hell, sobald etwas läuft, und
   dann fällt sein Schein auf Tisch und Boden. Der leere Stuhl daneben ist
   die zweite Auskunft — der Kollege ist nicht am Platz. */
function Arbeitsplaetze({
  plan,
  laufendVon,
  tassen,
}: {
  plan: Plan;
  laufendVon: Map<string, Laufend>;
  tassen: ReadonlySet<string>;
}) {
  return (
    <>
      {plan.raeume.map((r) =>
        r.leute.map((a, i) => {
          const p = r.sitze[i];
          return (
            <span key={a.id}>
              <span
                className={`bu-platz${laufendVon.has(a.id) ? " belegt" : ""}`}
                style={{ left: p.x - SITZ_B / 2 + 7, top: p.y + 18, width: SITZ_B - 14, height: 21 }}
                aria-hidden="true"
              >
                {tassen.has(a.id) && <Riss art="tasse" x={0} y={0} w={12} h={10} className="bu-tasse" />}
              </span>
              <span className="bu-stuhl" style={{ left: p.x - 7, top: p.y - 14 }} aria-hidden="true" />
            </span>
          );
        }),
      )}
    </>
  );
}

/* Die Karte am Platz.
 *
 * Sie ersetzt den Sprung in den Verlauf nicht, sie kommt ihm zuvor: In neun
 * von zehn Fällen will man wissen, woran jemand sitzt, und in dem zehnten
 * will man ihm einen Satz sagen. Beides hier zu haben heißt, dass das Büro
 * eine Arbeitsfläche ist und nicht ein Bild davon. Wer angesprochen wird,
 * bleibt übrigens stehen — das tut ein Mensch auch.
 *
 * Das Eingabefeld ist dasselbe Tor wie im Verlauf — dieselbe Adresse,
 * dieselbe Triage. Es antwortet nur nicht: Was der Kollege sagt, steht im
 * Gespräch, und dorthin führt der Verweis darunter.
 */
function BuKarte({
  agent,
  laeuft,
  wartet,
  am,
  breite,
  me,
  onOeffnen,
  onSchliessen,
}: {
  agent: Agent;
  laeuft?: Laufend;
  wartet: boolean;
  am: Punkt;
  breite: number;
  me: Principal;
  onOeffnen: () => void;
  onSchliessen: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [text, setText] = useState("");
  const [ab, setAb] = useState(false);
  const karte = useRef<HTMLDivElement>(null);

  useEffect(() => {
    karte.current?.focus();
  }, []);

  const schicken = useMutation({
    mutationFn: (nachricht: string) => post(`/agents/${agent.id}/messages`, { text: nachricht }),
    onSuccess: () => {
      setText("");
      setAb(true);
      qc.invalidateQueries({ queryKey: ["thread", agent.id] });
      qc.invalidateQueries({ queryKey: ["org-running"] });
    },
  });

  /* Am Rand nicht über den Rand: Die Karte ist breit, die Fläche endet, und
     eine halb abgeschnittene Karte ist schlimmer als eine verschobene. */
  const HALB = 132;
  const x = breite > 0 ? Math.min(Math.max(am.x, HALB + 8), breite - HALB - 8) : am.x;
  const darfSchreiben = canManage(me.Role);

  return (
    <div
      className="bu-karte"
      style={{ left: x, top: am.y + 36 }}
      role="dialog"
      aria-label={agent.display_name}
      ref={karte}
      tabIndex={-1}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <header className="bu-karte-kopf">
        <Gesicht
          schluessel={agent.slug}
          zustand={agent.killed ? "killed" : agent.status === "sleeping" ? "sleeping" : "working"}
          groesse={28}
        />
        <span className="bu-karte-wer">
          <strong>{agent.display_name}</strong>
          <span className="bu-karte-rolle">{agent.job_title || agent.slug}</span>
        </span>
        <button className="bu-karte-zu" onClick={onSchliessen} aria-label={t("team.abbrechen")}>
          ×
        </button>
      </header>

      {laeuft ? (
        <p className="bu-karte-lage">
          <span className="bu-karte-titel">{laeuft.title}</span>
          <span className="bu-karte-unten">
            {laeuft.step && <>{t(`team.schritt.${laeuft.step}`, laeuft.step)} · </>}
            <Dauer seit={laeuft.since} />
          </span>
        </p>
      ) : (
        <p className="bu-karte-lage">
          <span className="bu-karte-unten">
            {wartet ? t("team.wartet") : agent.status === "sleeping" ? t("team.schlaeft") : t("team.bereit")}
          </span>
        </p>
      )}

      {darfSchreiben && (
        <form
          className="bu-karte-sagen"
          onSubmit={(e) => {
            e.preventDefault();
            const n = text.trim();
            if (n && !schicken.isPending) schicken.mutate(n);
          }}
        >
          <input
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setAb(false);
            }}
            placeholder={t("chat.placeholder")}
            aria-label={t("chat.placeholder")}
          />
          <button type="submit" disabled={!text.trim() || schicken.isPending} aria-label={t("team.senden")}>
            ↑
          </button>
        </form>
      )}
      {/* Angenommen, nicht beantwortet: Was der Kollege daraus macht, steht im
          Gespräch, und der Verweis darunter führt hin. Hier zu behaupten, es
          sei erledigt, wäre genau die Unwahrheit, die #304 beseitigt hat. */}
      {ab && <p className="bu-karte-ab">{t("team.angenommen")}</p>}

      <button className="bu-karte-hin" onClick={onOeffnen}>
        {t("team.gespraech")}
      </button>
    </div>
  );
}

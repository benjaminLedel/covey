import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { post, type Agent, type Department, type Laufend, type Principal } from "../api";
import { canManage } from "../pages/agent/roles";
import Gesicht from "../components/Gesicht";
import Dauer from "../components/Dauer";

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
 *
 * DAS LICHT IST DER ZUSTAND. Ein Büro sagt einem von der Tür aus, wo
 * gearbeitet wird, weil dort die Lampen an sind. Hier war dieselbe Auskunft
 * vierzig einzelne Lesungen: jeder Tisch trug einen blassen Rahmen, und die
 * Räume sahen alle gleich aus. Jetzt trägt der Bildschirm am Platz das Licht,
 * es fällt auf den Boden davor, und ein Raum mit einem hellen Tisch darin ist
 * ein heller Raum — quer über die Seite lesbar (#312).
 *
 * Und was man damit TUN kann, ist der eigentliche Punkt: Die Wachen sehen dem
 * Zeiger nach, wer arbeitet, hat eine Gedankenblase mit seinem letzten
 * Schritt, und ein Klick öffnet die Karte am Platz — mit einem Eingabefeld
 * darin. Man geht zu jemandem hin und sagt etwas, ohne das Büro zu verlassen.
 */

/** Ein Platz im Raster eines Abteilungsraums. */
type Platz = { x: number; y: number };

/* Breiter als zuvor (78), und der Grund steht in der Liste darunter: Bei
   siebzig Pixeln endete jeder zweite Name in drei Punkten — „Infra-Wäch…",
   „Postmorte…". Ein Grundriss, auf dem man die Kollegen nicht lesen kann,
   spart Platz an der einzigen Stelle, die ihn braucht. */
const PLATZ_B = 88;
const PLATZ_H = 92;
/* Was ein einzelner Platz wirklich hoch ist: Kopf, Tisch, Name. Die 92 oben
   sind der Abstand ZWISCHEN zwei Reihen; die letzte Reihe braucht ihn nicht,
   und ohne diese Unterscheidung stand unter jedem Zimmer eine Handbreit
   nichts. */
const PLATZ_INHALT = 70;
const RASTER_OBEN = 42;
const RAUM_UNTEN = 12;
const SPALTEN = 3;

/** Wie weit die Augen ausschlagen, im Raster des Gesichts (24 breit). */
const BLICK_WEITE = 1.25;

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

type Raum = {
  id: string;
  name: string;
  color: string;
  leute: Agent[];
  rasterB: number;
  rasterH: number;
  plaetze: Map<string, Platz>;
  /** Der erste freie Platz der letzten Reihe — dort steht die Pflanze. */
  luecke: Platz | null;
};

/* Wohin die Wachen treten.
 *
 * Der Regelfall ist ein Schritt zur Seite, alle paar Sekunden, um den eigenen
 * Platz herum — ein Büro, in dem alle ständig rennen, ist ein
 * Bildschirmschoner.
 *
 * Dazu kommt das Zusammenstehen: Alle drei Takte gehen zwei Wache, deren
 * Plätze nebeneinander liegen, aufeinander zu und stehen eine Weile
 * beieinander. Das ist AUSDRÜCKLICH kein Ereignis, das covey kennt — es steht
 * in keiner Aufzeichnung und behauptet kein Gespräch. Es ist der Unterschied
 * zwischen einem Raum und einem Wartezimmer, in dem alle einzeln an ihrem
 * Stuhl kleben, und es bewegt nur die, die ohnehin nichts zu tun haben.
 */
function wanderung(raum: Raum, frei: Agent[], takt: number): Map<string, Platz> {
  const versatz = new Map<string, Platz>();
  for (const a of frei) {
    versatz.set(a.id, {
      x: [0, 15, -13, 8][(streu(a.slug, 4) + takt) % 4],
      y: [0, -9, 11, -4][(streu(a.slug + "y", 4) + takt) % 4],
    });
  }
  if (takt % 3 !== 1 || frei.length < 2) return versatz;

  const i = streu(raum.id + "|" + takt, frei.length);
  const j = (i + 1 + streu(raum.id + "~" + takt, frei.length - 1)) % frei.length;
  if (i === j) return versatz;
  const a = frei[i];
  const b = frei[j];
  const pa = raum.plaetze.get(a.id)!;
  const pb = raum.plaetze.get(b.id)!;
  /* Nur, wer nebeneinander sitzt: Zwei, die quer durch den Raum
     aufeinander zulaufen, sehen aus, als sei der Grundriss kaputt. */
  if (Math.hypot(pb.x - pa.x, pb.y - pa.y) > PLATZ_B * 1.6) return versatz;
  const mx = (pa.x + pb.x) / 2;
  const my = (pa.y + pb.y) / 2;
  versatz.set(a.id, { x: mx - pa.x - 14, y: my - pa.y });
  versatz.set(b.id, { x: mx - pb.x + 14, y: my - pb.y });
  return versatz;
}

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

  /* Die Räume: eine Abteilung, ein Raster. Die Reihenfolge ist die der
     Abteilungen, damit der Grundriss von Besuch zu Besuch derselbe bleibt —
     ein Büro, in dem die Zimmer wandern, ist kein Büro. */
  const raeume = useMemo<Raum[]>(() => {
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
      const rest = g.leute.length % spalten;
      return {
        ...g,
        rasterB: spalten * PLATZ_B,
        rasterH: (zeilen - 1) * PLATZ_H + PLATZ_INHALT,
        plaetze: new Map<string, Platz>(
          g.leute.map((a, i) => [a.id, { x: (i % spalten) * PLATZ_B, y: Math.floor(i / spalten) * PLATZ_H }]),
        ),
        /* Geht die letzte Reihe nicht auf, bleibt eine Lücke. Sie stand
           vorher leer; jetzt steht die Pflanze darin. Das ist der Platz, den
           der Grundriss ohnehin hat — Beiwerk, das keinen neuen Raum
           kostet. */
        luecke: rest === 0 ? null : { x: rest * PLATZ_B, y: (zeilen - 1) * PLATZ_H },
      };
    });
  }, [agents, departments, t]);

  /* Das Herumlaufen. Wer wach ist und keinen Vorgang hat, tritt alle paar
     Sekunden einen Schritt zur Seite. */
  const [takt, setTakt] = useState(0);
  const ruhig = useRef(false);
  useEffect(() => {
    ruhig.current = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
    if (ruhig.current) return;
    const id = setInterval(() => setTakt((n) => n + 1), 5200);
    return () => clearInterval(id);
  }, []);

  /* Wo jeder steht, in Koordinaten der Fläche.
   *
   * Einmal nach dem Zeichnen gemessen und dann gemerkt: Beim Zeigen wäre ein
   * getBoundingClientRect() je Kollege und je Mausbewegung ein Layout pro
   * Bild — bei siebzig Kollegen ist das der Unterschied zwischen einer
   * Bewegung und einem Ruckeln. Das Wandern verschiebt jemanden um höchstens
   * fünfzehn Pixel; für eine Blickrichtung ist das nichts. */
  const flaeche = useRef<HTMLDivElement>(null);
  const [zentren, setZentren] = useState<Map<string, Platz>>(new Map());
  useLayoutEffect(() => {
    const messen = () => {
      const f = flaeche.current;
      if (!f) return;
      const fr = f.getBoundingClientRect();
      const m = new Map<string, Platz>();
      f.querySelectorAll<HTMLElement>("[data-wer]").forEach((el) => {
        const r = el.getBoundingClientRect();
        m.set(el.dataset.wer!, { x: r.left - fr.left + r.width / 2, y: r.top - fr.top + 16 });
      });
      setZentren(m);
    };
    messen();
    const beobachter = new ResizeObserver(messen);
    if (flaeche.current) beobachter.observe(flaeche.current);
    return () => beobachter.disconnect();
  }, [raeume]);

  /* Der Zeiger, entkoppelt vom Ereignis: Mausbewegungen kommen häufiger als
     Bilder, und jedes davon zu einem Zustand zu machen hieße, mehrfach je
     Bild zu zeichnen. */
  const [zeiger, setZeiger] = useState<Platz | null>(null);
  const wartend = useRef<Platz | null>(null);
  const gemeldet = useRef(false);
  const aufZeiger = (e: React.PointerEvent<HTMLDivElement>) => {
    if (ruhig.current) return;
    const fr = e.currentTarget.getBoundingClientRect();
    wartend.current = { x: e.clientX - fr.left, y: e.clientY - fr.top };
    if (gemeldet.current) return;
    gemeldet.current = true;
    requestAnimationFrame(() => {
      gemeldet.current = false;
      setZeiger(wartend.current);
    });
  };

  const [gewaehlt, setGewaehlt] = useState<string | null>(null);
  useEffect(() => {
    if (!gewaehlt) return;
    const zu = (e: KeyboardEvent) => {
      if (e.key === "Escape") setGewaehlt(null);
    };
    window.addEventListener("keydown", zu);
    return () => window.removeEventListener("keydown", zu);
  }, [gewaehlt]);

  const laufendVon = new Map(laufend.map((l) => [l.agent_id, l]));
  const gewaehlterAgent = agents.find((a) => a.id === gewaehlt);
  const gewaehltesZentrum = gewaehlt ? zentren.get(gewaehlt) : undefined;

  /* Die Blickrichtung: normiert, damit ein Kollege am anderen Ende des Büros
     genauso hinsieht wie einer daneben — es geht um die Richtung, nicht um
     die Entfernung. */
  const blickAuf = (id: string) => {
    const z = zentren.get(id);
    if (!zeiger || !z) return undefined;
    const dx = zeiger.x - z.x;
    const dy = zeiger.y - z.y;
    const laenge = Math.hypot(dx, dy);
    if (laenge < 1) return undefined;
    return { x: (dx / laenge) * BLICK_WEITE, y: (dy / laenge) * BLICK_WEITE };
  };

  return (
    <div className="bu">
      {/* Der Tresen: hier stehen die, die auf eine Entscheidung warten. Er
          liegt vorn, weil das die einzige Gruppe ist, die etwas von Ihnen
          will. */}
      <BuTresen
        leute={agents.filter((a) => wartetBei.has(a.id))}
        gewaehlt={gewaehlt}
        onWaehlen={setGewaehlt}
        blickAuf={blickAuf}
      />

      <div
        className="bu-flaeche"
        ref={flaeche}
        onPointerMove={aufZeiger}
        onPointerLeave={() => setZeiger(null)}
        /* Ein Klick auf den Boden schließt die Karte. Die Karte selbst hält
           ihn auf (stopPropagation) — sonst schlösse sie sich, während man in
           ihr Feld tippt. */
        onPointerDown={() => setGewaehlt(null)}
      >
        <div className="bu-zimmer">
        {raeume.map((raum, ri) => {
          /* Zwei Auskünfte, die der Raum als Ganzes trägt: Brennt hier Licht
             (arbeitet jemand), und ist hier alles zu (schlafen alle). Der
             dritte Fall — wach, aber ohne Vorgang — ist der Normalfall und
             braucht keine Markierung. */
          const arbeiten = raum.leute.some((a) => laufendVon.has(a.id));
          const alleSchlafen = raum.leute.every((a) => a.status === "sleeping" || a.killed);
          /* Wer wach ist, nichts zu tun hat und nicht vorn am Tresen steht.
             Nur die wandern. */
          const frei = ruhig.current
            ? []
            : raum.leute.filter(
                (a) => !laufendVon.has(a.id) && !wartetBei.has(a.id) && a.status !== "sleeping" && !a.killed,
              );
          const versatz = wanderung(raum, frei, takt);
          return (
            <section
              key={raum.id || "ohne"}
              className={`bu-raum${arbeiten ? " hell" : ""}${alleSchlafen ? " dunkel" : ""}`}
              style={{
                /* Die Breite gibt die Spalte vor, nicht der Inhalt; das
                   Raster steht mittig darin (app.css, .bu-zimmer). */
                ["--i" as string]: ri,
                minHeight: RASTER_OBEN + raum.rasterH + RAUM_UNTEN,
              }}
              aria-label={raum.name}
            >
              <h3 className="bu-raum-name">
                {raum.color && <span className="bu-punkt" style={{ background: raum.color }} aria-hidden="true" />}
                {raum.name}
              </h3>

              <div className="bu-raster" style={{ width: raum.rasterB, height: raum.rasterH }}>
                {/* Die Pflanze in der Lücke der letzten Reihe. Sie steht für
                    nichts — sie ist der Teil des Raums, der kein Zustand ist,
                    und ohne ihn ist ein Zimmer ein Diagramm. */}
                {raum.luecke && <BuPflanze am={raum.luecke} />}
                {/* Die Plätze stehen fest und bleiben stehen, auch wenn
                    niemand an ihnen sitzt. Ein leerer Schreibtisch ist eine
                    Auskunft: der Kollege ist vorn am Tresen oder läuft
                    herum. */}
                {raum.leute.map((a) => {
                  const platz = raum.plaetze.get(a.id)!;
                  const laeuft = laufendVon.get(a.id);
                  return (
                    <span
                      key={`p-${a.id}`}
                      className={`bu-platz${laeuft ? " belegt" : ""}`}
                      style={{ transform: `translate(${platz.x}px, ${platz.y}px)` }}
                      aria-hidden="true"
                    >
                      {/* Auf dem Tisch. Die Tasse steht, wo gearbeitet wird;
                          die Mappe liegt dort, wo sie immer liegt. */}
                      {laeuft && <BuTasse />}
                      {streu(a.slug + "#", 3) === 0 && <BuMappe />}
                    </span>
                  );
                })}

                {raum.leute.map((a) => {
                  const platz = raum.plaetze.get(a.id)!;
                  if (wartetBei.has(a.id)) return null; // steht vorn am Tresen
                  const laeuft = laufendVon.get(a.id);
                  const schlaeft = a.status === "sleeping";
                  const tot = a.killed;
                  const geht = versatz.get(a.id);
                  return (
                    <button
                      key={a.id}
                      data-wer={a.id}
                      className={`bu-wer${laeuft ? " arbeitet" : ""}${tot ? " gestoppt" : ""}${geht ? " geht" : ""}${gewaehlt === a.id ? " gewaehlt" : ""}`}
                      style={{
                        transform: `translate(${platz.x + (geht?.x ?? 0)}px, ${platz.y + (geht?.y ?? 0)}px)`,
                        /* Alle traten auf denselben Takt. Ein Raum, in dem
                           sich sieben Leute im selben Augenblick bewegen, ist
                           eine Marschkolonne; mit einer Verzögerung je Person
                           wird daraus ein Rascheln. */
                        ["--zoegern" as string]: geht ? `${streu(a.slug + "!", 12) * 0.12}s` : "0s",
                      }}
                      onClick={() => setGewaehlt(gewaehlt === a.id ? null : a.id)}
                      aria-expanded={gewaehlt === a.id}
                      title={laeuft ? `${a.display_name}: ${laeuft.title}` : a.display_name}
                    >
                      {/* Die Gedankenblase: der zuletzt aufgezeichnete
                          Schritt, über dem Kopf dessen, der ihn gerade tut.
                          Der Schlüssel ist der Schritt selbst — so läuft das
                          Aufziehen erneut, wenn sich der Schritt ändert, und
                          der Wechsel ist zu sehen statt nur zu lesen. */}
                      {laeuft?.step && (
                        <span key={laeuft.step} className="bu-denkt">
                          {t(`team.schritt.${laeuft.step}`, laeuft.step)}
                        </span>
                      )}
                      <span className="bu-kopf">
                        <Gesicht
                          schluessel={a.slug}
                          zustand={tot ? "killed" : schlaeft ? "sleeping" : "working"}
                          groesse={30}
                          blick={schlaeft || tot ? undefined : blickAuf(a.id)}
                        />
                      </span>
                      <span className="bu-name">{a.display_name.split(/\s+/)[0]}</span>
                    </button>
                  );
                })}
              </div>
            </section>
          );
        })}
        </div>

        {gewaehlterAgent && gewaehltesZentrum && (
          <BuKarte
            agent={gewaehlterAgent}
            laeuft={laufendVon.get(gewaehlterAgent.id)}
            wartet={wartetBei.has(gewaehlterAgent.id)}
            am={gewaehltesZentrum}
            breite={flaeche.current?.clientWidth ?? 0}
            me={me}
            onOeffnen={() => navigate(`/team/${gewaehlterAgent.id}`)}
            onSchliessen={() => setGewaehlt(null)}
          />
        )}
      </div>

      <p className="bu-legende">{t("team.bueroLegende")}</p>
    </div>
  );
}

/* Das Beiwerk. Drei kleine Zeichnungen, gemalt und nicht aus einer Schrift
   geliehen — ein Zeichen aus dem Unicode-Vorrat trägt die Strichstärke seiner
   Schrift und nicht die dieser Oberfläche. Sie stehen alle an --bu-deko, der
   einzigen Farbe, die sie haben dürfen: Beiwerk, das ins Auge fällt, ist
   Beiwerk zu viel. */

function BuPflanze({ am }: { am: Platz }) {
  return (
    <svg
      className="bu-pflanze"
      style={{ transform: `translate(${am.x}px, ${am.y}px)` }}
      viewBox="0 0 24 30"
      width="24"
      height="30"
      aria-hidden="true"
    >
      <path d="M12 22V13" />
      <path d="M12 15c-5-1-7-5-6.5-9C9 6.5 11.5 10 12 15Z" />
      <path d="M12 17c5-1.5 6.5-5.5 6-9.5-3.5.5-6 4-6 9.5Z" />
      <path className="bu-topf" d="M7 22h10l-1.2 6.5a1 1 0 0 1-1 .5H9.2a1 1 0 0 1-1-.5Z" />
    </svg>
  );
}

function BuTasse() {
  return (
    <svg className="bu-tasse" viewBox="0 0 14 12" width="14" height="12" aria-hidden="true">
      <path d="M2 3h8v5a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2Z" />
      <path d="M10 4.5h1.5a1.5 1.5 0 0 1 0 3H10" />
    </svg>
  );
}

function BuMappe() {
  return (
    <svg className="bu-mappe" viewBox="0 0 14 11" width="14" height="11" aria-hidden="true">
      <rect x="1.5" y="2.5" width="10" height="7.5" rx="1" />
      <path d="M4 1.5h9a1 1 0 0 1 1 1V8" />
    </svg>
  );
}

/* Die Karte am Platz.
 *
 * Sie ersetzt den Sprung in den Verlauf nicht, sie kommt ihm zuvor: In neun
 * von zehn Fällen will man wissen, woran jemand sitzt, und in dem zehnten
 * will man ihm einen Satz sagen. Beides hier zu haben heißt, dass das Büro
 * eine Arbeitsfläche ist und nicht ein Bild davon.
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
  am: Platz;
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
        <Gesicht schluessel={agent.slug} zustand={agent.killed ? "killed" : agent.status === "sleeping" ? "sleeping" : "working"} groesse={28} />
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

/* Der Tresen. Eine eigene Reihe, weil „wartet auf Sie" die einzige Gruppe
   ist, die nicht nur Zustand, sondern Aufforderung ist. */
function BuTresen({
  leute,
  gewaehlt,
  onWaehlen,
  blickAuf,
}: {
  leute: Agent[];
  gewaehlt: string | null;
  onWaehlen: (id: string | null) => void;
  blickAuf: (id: string) => { x: number; y: number } | undefined;
}) {
  const { t } = useTranslation();
  if (leute.length === 0) return null;
  return (
    <div className="bu-tresen">
      <span className="bu-tresen-schild">{t("team.wartet")}</span>
      <div className="bu-tresen-leute">
        {leute.map((a) => (
          <button
            key={a.id}
            data-wer={a.id}
            className={`bu-wer am-tresen${gewaehlt === a.id ? " gewaehlt" : ""}`}
            onClick={() => onWaehlen(gewaehlt === a.id ? null : a.id)}
            aria-expanded={gewaehlt === a.id}
            title={a.display_name}
          >
            <span className="bu-kopf">
              <Gesicht schluessel={a.slug} zustand="working" groesse={30} blick={blickAuf(a.id)} />
            </span>
            <span className="bu-name">{a.display_name.split(/\s+/)[0]}</span>
          </button>
        ))}
      </div>
    </div>
  );
}

import { createContext, useContext, useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "react-router";
import { api, type Agent, type Department, type Verlauf } from "../api";
import Gesicht from "./Gesicht";
import { NavIcon } from "./navicons";

/* Die Suche als Überblendung, nicht als Feld in der Spalte.
 *
 * Ein Suchfeld links oben ist ein Möbelstück: Es steht immer da, nimmt einer
 * Liste die Zeile, die sie zum Atmen braucht, und wird trotzdem selten
 * benutzt — man scrollt eher, als dass man hinfasst. Als Überblendung ist es
 * umgekehrt: unsichtbar, solange man es nicht braucht, und dann die ganze
 * Aufmerksamkeit.
 *
 * Drei Wege hinein, weil drei verschiedene Leute drei verschiedene kennen:
 * das Lupensymbol neben der Wortmarke, ⌘K (auf Windows Strg+K) und der
 * Schrägstrich. Der Schrägstrich ist der, den die Agentenliste der Konsole
 * schon benutzt — wer ihn dort gelernt hat, darf ihn hier nicht verlieren.
 *
 * ZWEI SUCHEN, EINE STELLE. Ein Kollege lässt sich markieren (Tabulator oder
 * Pfeil nach rechts), und dann sucht dasselbe Feld nicht mehr die Belegschaft,
 * sondern das Gespräch mit ihm. Vorher gab es dafür ein zweites Feld in der
 * Kopfzeile des Verlaufs — zwei Felder, zwei Tastenwege, zwei Arten, nichts
 * zu finden. Die Frage „wo war das noch?" ist dieselbe Frage, ob die Antwort
 * ein Mensch oder ein Satz ist; sie gehört an eine Stelle.
 */

/* Wer die Suche aufmacht, sagt womit. Die Überblendung hängt an der Schale,
   der Verlauf liegt tief in den Routen darunter — ein Kontext ist hier der
   kurze Weg, und der einzige, der ohne durchgereichte Rückrufe auskommt. */
const SucheKontext = createContext<(agent?: Agent) => void>(() => {});
export const SucheProvider = SucheKontext.Provider;
export const useSucheOeffnen = () => useContext(SucheKontext);

export type Treffer = { agent: Agent; abteilung: string; wartet: boolean };

export default function Suche({
  offen,
  onClose,
  agents,
  departments,
  wartetBei,
  pfad,
  fokus,
  onFokus,
}: {
  offen: boolean;
  onClose: () => void;
  agents: Agent[];
  departments: Department[];
  wartetBei: Set<string>;
  /** Wohin ein Treffer führt — die Schale entscheidet das, nicht die Suche. */
  pfad: (a: Agent) => string;
  /** Der markierte Kollege: dann wird sein Gespräch durchsucht, nicht die Liste. */
  fokus: Agent | null;
  onFokus: (a: Agent | null) => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [begriff, setBegriff] = useState("");
  const [gewaehlt, setGewaehlt] = useState(0);
  const feld = useRef<HTMLInputElement>(null);
  const liste = useRef<HTMLDivElement>(null);

  const treffer = useMemo(() => {
    const q = begriff.trim().toLowerCase();
    const name = (id?: string) => departments.find((d) => d.id === id)?.name ?? "";
    return agents
      .filter((a) => a.status !== "applicant")
      .map((a) => ({ agent: a, abteilung: name(a.department_id), wartet: wartetBei.has(a.id) }))
      .filter(
        (e) =>
          !q ||
          [e.agent.display_name, e.agent.job_title, e.agent.slug, e.abteilung].some((f) =>
            (f ?? "").toLowerCase().includes(q),
          ),
      )
      /* Wer wartet, steht oben — die Suche ist auch der schnellste Weg zu dem
         Kollegen, der eine Antwort braucht. */
      .sort((a, b) => Number(b.wartet) - Number(a.wartet))
      .slice(0, 40);
  }, [agents, departments, wartetBei, begriff]);

  /* Das Gespräch des markierten Kollegen. Ein kurzer Aufschub, weil hier bei
     jedem Tastendruck eine Abfrage stünde — und eine Suche, die schneller
     fragt als jemand tippt, fragt vor allem nach Halbwörtern. */
  const [verzoegert, setVerzoegert] = useState("");
  useEffect(() => {
    const id = setTimeout(() => setVerzoegert(begriff.trim()), 180);
    return () => clearTimeout(id);
  }, [begriff]);

  const gespraech = useQuery({
    queryKey: ["thread-suche", fokus?.id, verzoegert],
    queryFn: () => api<Verlauf>(`/agents/${fokus!.id}/thread?q=${encodeURIComponent(verzoegert)}`),
    enabled: !!fokus && verzoegert.length > 0,
    staleTime: 15_000,
  });
  /* Neueste zuerst: Im Verlauf liest man von oben nach unten, in einer Liste
     von Fundstellen sucht man das Letzte zuerst. */
  const zeilen = useMemo(() => [...(gespraech.data?.entries ?? [])].reverse().slice(0, 40), [gespraech.data]);

  // Beim Öffnen zurücksetzen und ins Feld springen.
  useEffect(() => {
    if (!offen) return;
    setBegriff("");
    setGewaehlt(0);
    const id = requestAnimationFrame(() => feld.current?.focus());
    return () => cancelAnimationFrame(id);
  }, [offen]);

  useEffect(() => setGewaehlt(0), [begriff, fokus]);

  /* Die gewählte Zeile im Blick halten, wenn man mit den Pfeiltasten durch
     vierzig Kollegen fährt. */
  useEffect(() => {
    liste.current?.querySelector('[aria-selected="true"]')?.scrollIntoView?.({ block: "nearest" });
  }, [gewaehlt]);

  if (!offen) return null;

  const oeffnen = (e: Treffer) => {
    onClose();
    navigate(pfad(e.agent));
  };

  /* Ein Treffer im Gespräch führt zu der Stelle, an der er steht — an den
     Vorgang, nicht an die Zeile: Die Zeilen eines Vorgangs gehören zusammen,
     und wer eine Notiz sucht, will sehen, wozu sie gehört. */
  const hinspringen = (gruppe: string) => {
    onClose();
    navigate(`/team/${fokus!.id}?zu=${gruppe}`);
  };

  const anzahl = fokus ? zeilen.length : treffer.length;

  const taste = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      e.preventDefault();
      if (fokus) {
        onFokus(null);
        return;
      }
      onClose();
      return;
    }
    /* Markieren: Tabulator, weil er in jeder Suchmaske „übernimm den
       Vorschlag" heißt, und Pfeil nach rechts, weil das die Richtung ist, in
       die man geht. */
    if ((e.key === "Tab" || e.key === "ArrowRight") && !fokus && treffer[gewaehlt]) {
      e.preventDefault();
      onFokus(treffer[gewaehlt].agent);
      setBegriff("");
      return;
    }
    /* Und zurück: Rücktaste im leeren Feld nimmt die Markierung wieder ab —
       dieselbe Geste, mit der man in jedem Mailfeld einen Empfänger löscht. */
    if (e.key === "Backspace" && fokus && begriff === "") {
      e.preventDefault();
      onFokus(null);
      return;
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (anzahl === 0) return;
      const richtung = e.key === "ArrowDown" ? 1 : -1;
      setGewaehlt((g) => (g + richtung + anzahl) % anzahl);
      return;
    }
    if (e.key === "Enter") {
      if (fokus && zeilen[gewaehlt]) {
        e.preventDefault();
        hinspringen(zeilen[gewaehlt].task_id ?? zeilen[gewaehlt].id);
        return;
      }
      if (!fokus && treffer[gewaehlt]) {
        e.preventDefault();
        oeffnen(treffer[gewaehlt]);
      }
    }
  };

  return (
    <div className="suche-schleier" onMouseDown={onClose}>
      <div
        className="suche"
        role="dialog"
        aria-modal="true"
        aria-label={t("team.suche")}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="suche-feld">
          <NavIcon name="search" />
          {fokus && (
            <span className="suche-marke">
              <Gesicht
                schluessel={fokus.slug}
                zustand={fokus.killed ? "killed" : fokus.status === "sleeping" ? "sleeping" : "working"}
                groesse={18}
              />
              {fokus.display_name}
              <button onClick={() => onFokus(null)} aria-label={t("team.abbrechen")}>
                ×
              </button>
            </span>
          )}
          <input
            ref={feld}
            type="text"
            value={begriff}
            onChange={(e) => setBegriff(e.target.value)}
            onKeyDown={taste}
            placeholder={fokus ? t("team.imGespraechSuchen", { name: fokus.display_name }) : t("team.suche")}
            aria-label={fokus ? t("team.imGespraechSuchen", { name: fokus.display_name }) : t("team.suche")}
            aria-controls="suche-treffer"
            aria-activedescendant={!fokus && treffer[gewaehlt] ? `suche-${treffer[gewaehlt].agent.id}` : undefined}
          />
          <kbd>esc</kbd>
        </div>

        <div className="suche-treffer" id="suche-treffer" role="listbox" ref={liste}>
          {fokus ? (
            <>
              {verzoegert === "" && <p className="suche-leer">{t("team.imGespraechLead")}</p>}
              {verzoegert !== "" && zeilen.length === 0 && !gespraech.isFetching && (
                <p className="suche-leer">{t("team.nichtsGefunden")}</p>
              )}
              {zeilen.map((z, i) => (
                <button
                  key={`${z.kind}-${z.id}-${z.at}`}
                  role="option"
                  aria-selected={i === gewaehlt}
                  className={`suche-zeile satz ${i === gewaehlt ? "on" : ""}`}
                  onMouseEnter={() => setGewaehlt(i)}
                  onClick={() => hinspringen(z.task_id ?? z.id)}
                >
                  <span className={`suche-art k-${z.kind}`}>{t(`chat.kind.${z.kind}`, t("team.auftrag"))}</span>
                  <span className="suche-fund">
                    <span className="suche-satz">{z.text}</span>
                    {/* Wozu die Zeile gehört. Gesucht wird auch über den Titel
                        des Vorgangs, und eine Notiz „test" unter einem Treffer
                        für „Globex" sieht ohne ihn aus wie ein Fehler. */}
                    {z.task_title && <span className="suche-worin">{z.task_title}</span>}
                  </span>
                  <time className="suche-wann" dateTime={z.at}>
                    {new Date(z.at).toLocaleDateString()}
                  </time>
                </button>
              ))}
              {verzoegert !== "" && (
                <Link
                  className="suche-weiter"
                  to={`/agents/${fokus.id}?q=${encodeURIComponent(verzoegert)}`}
                  onClick={onClose}
                >
                  {t("team.imBacklogSuchen")}
                </Link>
              )}
            </>
          ) : (
            <>
          {treffer.length === 0 && <p className="suche-leer">{t("team.nichtsGefunden")}</p>}
          {treffer.map((e, i) => (
            <button
              key={e.agent.id}
              id={`suche-${e.agent.id}`}
              role="option"
              aria-selected={i === gewaehlt}
              className={`suche-zeile ${i === gewaehlt ? "on" : ""}`}
              onMouseEnter={() => setGewaehlt(i)}
              onClick={() => oeffnen(e)}
            >
              <Gesicht
                schluessel={e.agent.slug}
                zustand={e.agent.killed ? "killed" : e.agent.status === "sleeping" ? "sleeping" : "working"}
                groesse={26}
              />
              <span className="suche-wer">
                <span className="suche-name">{e.agent.display_name}</span>
                <span className="suche-rolle">{e.agent.job_title || e.agent.slug}</span>
              </span>
              {e.abteilung && <span className="suche-abteilung">{e.abteilung}</span>}
              {e.wartet && <span className="suche-wartet">{t("team.wartet")}</span>}
            </button>
          ))}
            </>
          )}
        </div>

        <div className="suche-fuss">
          <span>
            <kbd>↑</kbd>
            <kbd>↓</kbd> {t("team.sucheBlaettern")}
          </span>
          <span>
            <kbd>↵</kbd> {t("team.sucheOeffnen")}
          </span>
          {!fokus && (
            <span>
              <kbd>⇥</kbd> {t("team.sucheMarkieren")}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}

/* Die Tastenkürzel, die die Suche öffnen. Sie hängen an der Schale und nicht
   an der Überblendung: Eine Komponente, die nicht gerendert wird, hört auch
   keine Taste. */
export function useSucheKuerzel(oeffnen: () => void) {
  useEffect(() => {
    const taste = (e: KeyboardEvent) => {
      const ziel = e.target as HTMLElement | null;
      const tippt = ziel && (/^(INPUT|TEXTAREA)$/.test(ziel.tagName) || ziel.isContentEditable);
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        oeffnen();
        return;
      }
      if (e.key === "/" && !tippt) {
        e.preventDefault();
        oeffnen();
      }
    };
    window.addEventListener("keydown", taste);
    return () => window.removeEventListener("keydown", taste);
  }, [oeffnen]);
}

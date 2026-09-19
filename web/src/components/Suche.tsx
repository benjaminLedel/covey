import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import type { Agent, Department } from "../api";
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
 */

export type Treffer = { agent: Agent; abteilung: string; wartet: boolean };

export default function Suche({
  offen,
  onClose,
  agents,
  departments,
  wartetBei,
  pfad,
}: {
  offen: boolean;
  onClose: () => void;
  agents: Agent[];
  departments: Department[];
  wartetBei: Set<string>;
  /** Wohin ein Treffer führt — die Schale entscheidet das, nicht die Suche. */
  pfad: (a: Agent) => string;
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

  // Beim Öffnen zurücksetzen und ins Feld springen.
  useEffect(() => {
    if (!offen) return;
    setBegriff("");
    setGewaehlt(0);
    const id = requestAnimationFrame(() => feld.current?.focus());
    return () => cancelAnimationFrame(id);
  }, [offen]);

  useEffect(() => setGewaehlt(0), [begriff]);

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

  const taste = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
      return;
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (treffer.length === 0) return;
      const richtung = e.key === "ArrowDown" ? 1 : -1;
      setGewaehlt((g) => (g + richtung + treffer.length) % treffer.length);
      return;
    }
    if (e.key === "Enter" && treffer[gewaehlt]) {
      e.preventDefault();
      oeffnen(treffer[gewaehlt]);
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
          <input
            ref={feld}
            type="text"
            value={begriff}
            onChange={(e) => setBegriff(e.target.value)}
            onKeyDown={taste}
            placeholder={t("team.suche")}
            aria-label={t("team.suche")}
            aria-controls="suche-treffer"
            aria-activedescendant={treffer[gewaehlt] ? `suche-${treffer[gewaehlt].agent.id}` : undefined}
          />
          <kbd>esc</kbd>
        </div>

        <div className="suche-treffer" id="suche-treffer" role="listbox" ref={liste}>
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
        </div>

        <div className="suche-fuss">
          <span>
            <kbd>↑</kbd>
            <kbd>↓</kbd> {t("team.sucheBlaettern")}
          </span>
          <span>
            <kbd>↵</kbd> {t("team.sucheOeffnen")}
          </span>
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

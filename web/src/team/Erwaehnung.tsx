import { useState, type KeyboardEvent, type RefObject } from "react";
import { useTranslation } from "react-i18next";
import { Avatar } from "../components/person";

/* Mentions in a group (#440): an agent there answers only when it is
 * addressed, so "@" is the way to address it, and typing the whole handle
 * from memory is where it goes wrong. Typing "@" opens the members that
 * match what follows; arrows choose, Enter or Tab puts the handle in. What
 * goes in is what the server matches (chat.Addressed): an agent's slug,
 * a person's first name.
 *
 * The component owns only the list. The textarea stays the caller's; it
 * passes its keydown here first, and only what this does not take is its
 * own (Enter still sends when the list is closed). */
export type Kandidat = { key: string; name: string; handle: string; human: boolean; slug?: string; unter?: string };

/** The "@…" the caret stands in, or null. */
export function offeneErwaehnung(text: string, caret: number): { start: number; frage: string } | null {
  const vor = text.slice(0, caret);
  const m = /(^|\s)@([\p{L}\p{N}_.-]*)$/u.exec(vor);
  if (!m) return null;
  return { start: vor.length - m[2].length - 1, frage: m[2].toLowerCase() };
}

export function useErwaehnung({
  text,
  setText,
  feld,
  kandidaten,
}: {
  text: string;
  setText: (t: string) => void;
  feld: RefObject<HTMLTextAreaElement | null>;
  kandidaten: Kandidat[];
}) {
  const [caret, setCaret] = useState(0);
  const [aktiv, setAktiv] = useState(0);
  const [zu, setZu] = useState(false);

  const offen = offeneErwaehnung(text, caret);
  const treffer = offen
    ? kandidaten
        .filter((k) => !offen.frage || k.handle.toLowerCase().startsWith(offen.frage) || k.name.toLowerCase().includes(offen.frage))
        .slice(0, 6)
    : [];
  const sichtbar = !zu && offen !== null && treffer.length > 0;
  const index = Math.min(aktiv, Math.max(0, treffer.length - 1));

  const einsetzen = (k: Kandidat) => {
    if (!offen) return;
    const vorher = text.slice(0, offen.start);
    const nachher = text.slice(caret);
    const neu = `${vorher}@${k.handle} ${nachher.replace(/^\s+/, "")}`;
    setText(neu);
    const pos = vorher.length + k.handle.length + 2;
    setCaret(pos);
    requestAnimationFrame(() => {
      feld.current?.focus();
      feld.current?.setSelectionRange(pos, pos);
    });
  };

  /** The caller's onChange/onSelect/onClick keep the caret known. */
  const merke = () => {
    setCaret(feld.current?.selectionStart ?? text.length);
    setZu(false);
  };

  /** True when the key was the list's. */
  const taste = (e: KeyboardEvent<HTMLTextAreaElement>): boolean => {
    if (!sichtbar) return false;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const n = treffer.length;
      setAktiv((i) => (e.key === "ArrowDown" ? (i + 1) % n : (i - 1 + n) % n));
      return true;
    }
    if (e.key === "Enter" || e.key === "Tab") {
      e.preventDefault();
      einsetzen(treffer[index]);
      return true;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      setZu(true);
      return true;
    }
    return false;
  };

  return { sichtbar, treffer, index, einsetzen, merke, taste, setAktiv };
}

export function ErwaehnungsListe({
  e,
  id,
}: {
  e: ReturnType<typeof useErwaehnung>;
  id: string;
}) {
  const { t } = useTranslation();
  if (!e.sichtbar) return null;
  return (
    <ul className="tm-erw" role="listbox" id={id} aria-label={t("conversation.mentionList")}>
      {e.treffer.map((k, i) => (
        <li
          key={k.key}
          role="option"
          id={`${id}-${i}`}
          aria-selected={i === e.index}
          className={i === e.index ? "an" : undefined}
          onMouseDown={(ev) => {
            ev.preventDefault();
            e.einsetzen(k);
          }}
          onMouseEnter={() => e.setAktiv(i)}
        >
          <Avatar name={k.name} human={k.human} slug={k.slug} size={20} />
          <span className="nm truncate">{k.name}</span>
          <span className="hd">@{k.handle}</span>
        </li>
      ))}
    </ul>
  );
}
